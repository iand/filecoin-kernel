package blocks

import (
	"bytes"
	"context"
	"fmt"

	"github.com/filecoin-project/go-state-types/big"
	"github.com/go-logr/logr"
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

func NewSubscription(ps *pubsub.PubSub, ntwk networks.Network) (*pubsub.Subscription, error) {
	topicName := TopicName(ntwk)

	topic, err := ps.Join(topicName)
	if err != nil {
		return nil, fmt.Errorf("join topic: %w", err)
	}

	v := &BlockValidator{
		ntwk: ntwk,
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
	ntwk networks.Network
}

func (v *BlockValidator) Validate(ctx context.Context, pid peer.ID, msg *pubsub.Message) pubsub.ValidationResult {
	var bm BlockMessage
	if err := bm.UnmarshalCBOR(bytes.NewReader(msg.GetData())); err != nil {
		logr.FromContextOrDiscard(ctx).Error(err, "failed to decode block during validation", "from", pid)
		return pubsub.ValidationReject
	}

	if err := v.validateBlockSyntax(ctx, pid, &bm); err != nil {
		logr.FromContextOrDiscard(ctx).Error(err, "block failed validation", "from", pid)
		return pubsub.ValidationReject
	}

	msg.ValidatorData = &bm
	return pubsub.ValidationAccept
}

func (v *BlockValidator) validateBlockSyntax(ctx context.Context, pid peer.ID, bm *BlockMessage) error {
	// Light syntax validation following https://spec.filecoin.io/#section-systems.filecoin_blockchain.struct.block.block-syntax-validation
	// Only steps that don't require access to block store
	// TODO: complete validation steps

	if len(bm.BlsMessages)+len(bm.SecpkMessages) > v.ntwk.BlockMessageLimit() {
		return fmt.Errorf("too many messages")
	}

	if bm.Header.BlockSig == nil {
		return fmt.Errorf("missing signature")
	}

	if bm.Header.ParentWeight.LessThan(big.Zero()) {
		return fmt.Errorf("negative parent weight")
	}

	if bm.Header.Height < 0 {
		return fmt.Errorf("negative height")
	}

	return nil
}
