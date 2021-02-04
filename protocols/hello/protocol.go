package hello

import (
	"context"
	"fmt"
	"time"

	cborutil "github.com/filecoin-project/go-cbor-util"
	"github.com/filecoin-project/go-state-types/abi"
	"github.com/filecoin-project/go-state-types/big"
	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p-core/host"
	"github.com/libp2p/go-libp2p-core/network"
	"github.com/libp2p/go-libp2p-core/peer"
)

//go:generate go run gen.go

const ProtocolID = "/fil/hello/1.0.0"

type HelloMessage struct {
	HeaviestTipSet       []cid.Cid
	HeaviestTipSetHeight abi.ChainEpoch
	HeaviestTipSetWeight big.Int
	GenesisHash          cid.Cid
}

type Request struct {
	PeerID        peer.ID
	Message       HelloMessage
	ReadDeadline  time.Time
	WriteDeadline time.Time
}

type LatencyMessage struct {
	TArrival int64
	TSent    int64
}

type Response struct {
	Message LatencyMessage
}

type ReceivedHello struct {
	Message HelloMessage
	PeerID  peer.ID
	Arrived time.Time
}

func Send(ctx context.Context, h host.Host, req *Request) (*Response, error) {
	s, err := h.NewStream(ctx, req.PeerID, ProtocolID)
	if err != nil {
		return nil, fmt.Errorf("new stream: %w", err)
	}
	defer s.Close()

	_ = s.SetWriteDeadline(req.WriteDeadline)
	if err := cborutil.WriteCborRPC(s, req.Message); err != nil {
		return nil, fmt.Errorf("write to peer: %w", err)
	}
	_ = s.SetWriteDeadline(time.Time{})

	_ = s.SetReadDeadline(req.ReadDeadline)
	var resp Response
	if err := cborutil.ReadCborRPC(s, &resp.Message); err != nil {
		return nil, err
	}

	return &resp, nil
}

// ErrorLogger is the subset of the logr.Logger interface needed for reporting errors
type ErrorLogger interface {
	Error(err error, msg string, keysAndValues ...interface{})
}

func NewStreamHandler(messages chan<- ReceivedHello, logger ErrorLogger) network.StreamHandler {
	return func(s network.Stream) {
		defer s.Close() //nolint:errcheck

		var msg HelloMessage
		if err := cborutil.ReadCborRPC(s, &msg); err != nil {
			logger.Error(err, "failed to read hello message")
			return
		}
		rec := ReceivedHello{
			Message: msg,
			PeerID:  s.Conn().RemotePeer(),
			Arrived: time.Now(),
		}

		messages <- rec

		sent := time.Now()
		rmsg := &LatencyMessage{
			TArrival: rec.Arrived.UnixNano(),
			TSent:    sent.UnixNano(),
		}

		if err := cborutil.WriteCborRPC(s, rmsg); err != nil {
			logger.Error(err, "error while responding to latency")
		}
	}
}
