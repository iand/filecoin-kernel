package messages

import (
	"bytes"
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p-core/host"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/libp2p/go-libp2p-pubsub"

	"github.com/iand/filecoin-kernel/chain"
	"github.com/iand/filecoin-kernel/networks"
)

type MsgMessage struct {
	Header        *chain.BlockHeader
	BlsMessages   []cid.Cid
	SecpkMessages []cid.Cid
}

func TopicName(ntwk networks.Network) string {
	return "/fil/msgs/" + ntwk.Name()
}

func NewSubscription(host host.Host, ps *pubsub.PubSub, ntwk networks.Network) (*pubsub.Subscription, error) {
	topicName := TopicName(ntwk)

	topic, err := ps.Join(topicName)
	if err != nil {
		return nil, fmt.Errorf("join topic: %w", err)
	}

	v := &MessageValidator{
		ntwk: ntwk,
		self: host.ID(),
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

type MessageValidator struct {
	ntwk networks.Network
	self peer.ID
}

func (v *MessageValidator) Validate(ctx context.Context, pid peer.ID, msg *pubsub.Message) pubsub.ValidationResult {
	var m chain.SignedMessage
	if err := m.UnmarshalCBOR(bytes.NewReader(msg.GetData())); err != nil {
		logr.FromContextOrDiscard(ctx).Error(err, "failed to decode message during validation", "from", pid)
		return pubsub.ValidationReject
	}

	if err := v.validateMessage(ctx, pid, &m); err != nil {
		logr.FromContextOrDiscard(ctx).Error(err, "block failed validation", "from", pid)
		return pubsub.ValidationReject
	}

	msg.ValidatorData = m
	return pubsub.ValidationAccept
}

func (v *MessageValidator) validateMessage(ctx context.Context, pid peer.ID, m *chain.SignedMessage) error {
	if pid == v.self {
		return v.validateLocalMessage(ctx, m)
	}

	return nil
}

func (v *MessageValidator) validateLocalMessage(ctx context.Context, m *chain.SignedMessage) error {
	return nil
}
