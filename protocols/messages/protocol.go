package messages

import (
	"bytes"
	"context"
	"fmt"

	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p-core/host"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/libp2p/go-libp2p-pubsub"

	"github.com/iand/filecoin-kernel/chain"
	"github.com/iand/filecoin-kernel/networks"
)

//go:generate go run gen.go

type MsgMessage struct {
	Header        *chain.BlockHeader
	BlsMessages   []cid.Cid
	SecpkMessages []cid.Cid
}

// ErrorLogger is the subset of the logr.Logger interface needed for reporting errors
type ErrorLogger interface {
	Error(err error, msg string, keysAndValues ...interface{})
}

func TopicName(ntwk networks.Network) string {
	return "/fil/msgs/" + ntwk.Name()
}

func NewSubscription(host host.Host, ps *pubsub.PubSub, ntwk networks.Network, logger ErrorLogger) (*pubsub.Subscription, error) {
	topicName := TopicName(ntwk)

	topic, err := ps.Join(topicName)
	if err != nil {
		return nil, fmt.Errorf("join topic: %w", err)
	}

	v := &MessageValidator{
		logger: logger,
		ntwk:   ntwk,
		self:   host.ID(),
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
	logger ErrorLogger
	ntwk   networks.Network
	self   peer.ID
}

func (v *MessageValidator) Validate(ctx context.Context, pid peer.ID, msg *pubsub.Message) pubsub.ValidationResult {
	var m chain.SignedMessage
	if err := m.UnmarshalCBOR(bytes.NewReader(msg.GetData())); err != nil {
		v.logger.Error(err, "failed to decode message during validation")
		return pubsub.ValidationReject
	}

	if err := v.validateMessage(ctx, pid, &m); err != nil {
		v.logger.Error(err, "block failed validation")
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
