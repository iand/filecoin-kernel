package sync

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	bserv "github.com/ipfs/go-blockservice"
	"github.com/libp2p/go-eventbus"
	"github.com/libp2p/go-libp2p-core/event"
	"github.com/libp2p/go-libp2p-core/host"
	"github.com/libp2p/go-libp2p-core/peer"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"golang.org/x/sync/errgroup"

	"github.com/iand/filecoin-kernel/chain"
	"github.com/iand/filecoin-kernel/networks"
	"github.com/iand/filecoin-kernel/protocols/blocks"
	"github.com/iand/filecoin-kernel/protocols/hello"
	"github.com/iand/filecoin-kernel/protocols/messages"
)

// A Coordinator coordinates syncing the chain.
type Coordinator struct {
	cs   *chain.Store
	host host.Host
	ntwk networks.Network
	bsrv bserv.BlockService
}

func NewCoordinator(cs *chain.Store, host host.Host, ntwk networks.Network, bsrv bserv.BlockService) *Coordinator {
	return &Coordinator{
		cs:   cs,
		host: host,
		ntwk: ntwk,
		bsrv: bsrv,
	}
}

// Run starts the coordinator and blocks until the context is cancelled or an error occurs
func (c *Coordinator) Run(ctx context.Context) error {
	// create a new PubSub service using the GossipSub router
	ps, err := pubsub.NewGossipSub(ctx, c.host)
	if err != nil {
		return fmt.Errorf("create pubsub service: %w", err)
	}

	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		return c.pubsubBlocks(ctx, ps)
	})

	g.Go(func() error {
		return c.hello(ctx)
	})

	return g.Wait()
}

func (c *Coordinator) AddPotentialHead(ctx context.Context, pid peer.ID, fts *chain.FullTipSet) error {
	logger := logr.FromContextOrDiscard(ctx).WithValues("from", pid)

	// TODO: validate tipset blocks

	// Persist block headers
	bhs := make([]*chain.BlockHeader, 0, len(fts.Blocks))
	for _, fb := range fts.Blocks {
		bhs = append(bhs, fb.Header)
	}

	if err := c.cs.PutManyBlockHeaders(bhs); err != nil {
		return fmt.Errorf("persist block headers: %w", err)
	}

	// TODO: add peer to chain exchange list

	hts, err := c.cs.HeaviestTipSet(ctx)
	if err != nil {
		return fmt.Errorf("heaviest tipset: %w", err)
	}

	bestParentWeight := hts.Blocks[0].ParentWeight
	potentialParentWeight := fts.Blocks[0].Header.ParentWeight

	if potentialParentWeight.LessThan(bestParentWeight) {
		logger.Info("rejecting potential head since it has lower weight than best chain", "weight", potentialParentWeight, "best_chain_weight", bestParentWeight)
		return nil
	}

	// TODO: add tipset to pool for potential catchup source

	return nil
}

func (c *Coordinator) AddBlock(ctx context.Context, pid peer.ID, fb *chain.FullBlock) error {
	fts := &chain.FullTipSet{
		Blocks: []*chain.FullBlock{fb},
	}
	return c.AddPotentialHead(ctx, pid, fts)
}

func (c *Coordinator) pubsubBlocks(ctx context.Context, ps *pubsub.PubSub) error {
	logger := logr.FromContextOrDiscard(ctx)

	sub, err := blocks.NewSubscription(ps, c.ntwk)
	if err != nil {
		return fmt.Errorf("subscribe to topic: %w", err)
	}
	defer sub.Cancel()

	logger.Info("subscribed to blocks pubsub")

	for {
		msg, err := sub.Next(ctx)
		if err != nil {
			return fmt.Errorf("get next message: %w", err)
		}

		// only forward messages delivered by others
		if msg.ReceivedFrom == c.host.ID() {
			continue
		}

		go func() {
			logger := logger.WithValues("from", msg.ReceivedFrom)
			ctx := logr.NewContext(ctx, logger)

			logger.Info("received new block")

			bm, ok := msg.ValidatorData.(*blocks.BlockMessage)
			if !ok {
				logger.Info("unexpected type received", "type", fmt.Sprintf("%T", msg.ValidatorData))
				return
			}

			logger.Info("block header", "height", bm.Header.Height, "parent_state_root", bm.Header.ParentStateRoot, "miner", bm.Header.Miner)

			fb, err := fetchFullBlock(ctx, msg.ReceivedFrom, c.bsrv, bm)
			if err != nil {
				logger.Error(err, "add block to coordinator")
			}

			if err := c.AddBlock(ctx, msg.ReceivedFrom, fb); err != nil {
				logger.Error(err, "add block to coordinator")
			}
		}()

	}

	return nil
}

func (c *Coordinator) pubsubMessages(ctx context.Context, ps *pubsub.PubSub) error {
	logger := logr.FromContextOrDiscard(ctx)

	sub, err := messages.NewSubscription(c.host, ps, c.ntwk)
	if err != nil {
		return fmt.Errorf("subscribe to topic: %w", err)
	}
	defer sub.Cancel()

	logger.Info("subscribed to messages pubsub")

	for {
		msg, err := sub.Next(ctx)
		if err != nil {
			return fmt.Errorf("next message: %w", err)
		}

		logger.Info("received new message", "from", msg.ReceivedFrom)
	}

	return nil
}

func (c *Coordinator) hello(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)
	greetings := make(chan hello.ReceivedHello)
	c.host.SetStreamHandler(hello.ProtocolID, hello.NewStreamHandler(greetings, logger))

	sub, err := c.host.EventBus().Subscribe(new(event.EvtPeerIdentificationCompleted), eventbus.BufSize(1024))
	if err != nil {
		return fmt.Errorf("failed to subscribe to event bus: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case evt := <-sub.Out():
			// Connected to a new peer

			pic := evt.(event.EvtPeerIdentificationCompleted)
			if err := c.sayHello(ctx, pic.Peer); err != nil {
				protos, _ := c.host.Peerstore().GetProtocols(pic.Peer)
				agent, _ := c.host.Peerstore().Get(pic.Peer, "AgentVersion")
				if stringSliceContains(protos, hello.ProtocolID) {
					logger.Info("failed to say hello", "error", err, "peer", pic.Peer, "supported", protos, "agent", agent)
				} else {
					logger.V(1).Info("failed to say hello", "error", err, "peer", pic.Peer, "supported", protos, "agent", agent)
				}
			}

		case hmsg := <-greetings:
			// Received a hello message from a peer

			logger.Info("incoming hello message", "tipset", hmsg.Message.HeaviestTipSet, "height", hmsg.Message.HeaviestTipSetHeight, "weight", hmsg.Message.HeaviestTipSetWeight, "peer", hmsg.PeerID, "hash", hmsg.Message.GenesisHash)

		}
	}

	return nil
}

func (c *Coordinator) sayHello(ctx context.Context, pid peer.ID) error {
	logger := logr.FromContextOrDiscard(ctx).WithValues("peer", pid)

	logger.Info("saying hello")

	genHash, err := c.cs.GetGenesis(ctx)
	if err != nil {
		return fmt.Errorf("get genesis: %w", err)
	}

	hts, err := c.cs.HeaviestTipSet(ctx)
	if err != nil {
		return fmt.Errorf("heaviest tipset: %w", err)
	}

	req := &hello.Request{
		PeerID: pid,
		Message: hello.HelloMessage{
			HeaviestTipSet:       hts.Cids,
			HeaviestTipSetHeight: hts.Blocks[0].Height,
			HeaviestTipSetWeight: hts.Blocks[0].ParentWeight,
			GenesisHash:          genHash,
		},
	}

	resp, err := hello.Send(ctx, c.host, req)
	if err != nil {
		return fmt.Errorf("send hello message: %w", err)
	}

	if resp != nil {
		logger.Info("hello reported latency", "arrival", resp.Message.TArrival, "sent", resp.Message.TSent)
	}
	return nil
}

func stringSliceContains(haystack []string, needle string) bool {
	for _, p := range haystack {
		if p == needle {
			return true
		}
	}
	return false
}
