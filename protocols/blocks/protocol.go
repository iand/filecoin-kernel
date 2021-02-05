package blocks

import (
	"bytes"
	"context"
	"fmt"

	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/libp2p/go-libp2p-pubsub"

	"github.com/iand/filecoin-kernel/chain"
	"github.com/iand/filecoin-kernel/networks"
)

//go:generate go run gen.go

type BlockMessage struct {
	Header        *chain.BlockHeader
	BlsMessages   []cid.Cid
	SecpkMessages []cid.Cid
}

func TopicName(ntwk networks.Network) string {
	return "/fil/blocks/" + ntwk.Name()
}

// ErrorLogger is the subset of the logr.Logger interface needed for reporting errors
type ErrorLogger interface {
	Error(err error, msg string, keysAndValues ...interface{})
}

func NewSubscription(ps *pubsub.PubSub, ntwk networks.Network, logger ErrorLogger) (*pubsub.Subscription, error) {
	topicName := TopicName(ntwk)

	topic, err := ps.Join(topicName)
	if err != nil {
		return nil, fmt.Errorf("join topic: %w", err)
	}

	v := &BlockValidator{
		logger: logger,
		ntwk:   ntwk,
	}

	if err := ps.RegisterTopicValidator(topicName, v.Validate); err != nil {
		return nil, fmt.Errorf("register topic validator: %w", err)
	}

	sub, err := topic.Subscribe()
	if err != nil {
		return nil, fmt.Errorf("subscribe to topic: %w", err)
	}

	return sub, nil
}

type BlockValidator struct {
	logger ErrorLogger
	ntwk   networks.Network
}

func (v *BlockValidator) Validate(ctx context.Context, pid peer.ID, msg *pubsub.Message) pubsub.ValidationResult {
	var bm BlockMessage
	if err := bm.UnmarshalCBOR(bytes.NewReader(msg.GetData())); err != nil {
		v.logger.Error(err, "failed to decode block during validation")
		return pubsub.ValidationReject
	}

	if err := v.validateBlock(ctx, pid, &bm); err != nil {
		v.logger.Error(err, "block failed validation")
		return pubsub.ValidationReject
	}

	msg.ValidatorData = bm
	return pubsub.ValidationAccept
}

func (v *BlockValidator) validateBlock(ctx context.Context, pid peer.ID, bm *BlockMessage) error {
	// Basic validation
	// TODO: more validation

	if len(bm.BlsMessages)+len(bm.SecpkMessages) > v.ntwk.BlockMessageLimit() {
		return fmt.Errorf("too many messages")
	}

	// make sure we have a signature
	if bm.Header.BlockSig == nil {
		return fmt.Errorf("missing signature")
	}

	return nil
}
