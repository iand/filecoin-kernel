package chainexchange

import (
	"context"
	"fmt"
	"time"

	cborutil "github.com/filecoin-project/go-cbor-util"
	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p-core/host"
	"github.com/libp2p/go-libp2p-core/network"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/libp2p/go-libp2p-core/protocol"

	"github.com/iand/filecoin-kernel/chain"
)

//go:generate go run gen.go

// Request options. When fetching the chain segment we can fetch
// either block headers, messages, or both.
const (
	Headers = 1 << iota
	Messages
)

const (
	// BlockSyncProtocolID is the protocol ID of the former blocksync protocol.
	// Deprecated.
	BlockSyncProtocolID = "/fil/sync/blk/0.0.1"

	// ChainExchangeProtocolID is the protocol ID of the chain exchange
	// protocol.
	ChainExchangeProtocolID = "/fil/chain/xchg/0.0.1"
)

const (
	MaxRequestLength = uint64(900)
)

type SyncMessage struct {
	// List of ordered CIDs comprising a `TipSetKey` from where to start
	// fetching backwards.
	Head []cid.Cid
	// Number of block sets to fetch from `Head` (inclusive, should always
	// be in the range `[1, MaxRequestLength]`).
	Length uint64
	// Request options, see `Options` type for more details. Compressed
	// in a single `uint64` to save space.
	Options uint64
}

type Status uint64

const (
	Ok Status = 0
	// We could not fetch all blocks requested (but at least we returned
	// the `Head` requested). Not considered an error.
	Partial Status = 101

	// Errors
	NotFound      Status = 201
	GoAway        Status = 202
	InternalError Status = 203
	BadRequest    Status = 204
)

type ChainMessage struct {
	Status Status
	// String that complements the error status when converting to an
	// internal error (see `statusToError()`).
	ErrorMessage string

	Chain []*BSTipSet
}

type BSTipSet struct {
	// List of blocks belonging to a single tipset to which the
	// `CompactedMessages` are linked.
	Blocks   []*chain.BlockHeader
	Messages *CompactedMessages
}

type Request struct {
	PeerID        peer.ID
	Message       SyncMessage
	ReadDeadline  time.Time
	WriteDeadline time.Time
}

type Response struct {
	Message ChainMessage
}

func Send(ctx context.Context, h host.Host, req *Request) (*Response, error) {
	if req.Message.Length == 0 {
		return nil, fmt.Errorf("invalid message: zero length")
	}
	if req.Message.Length > MaxRequestLength {
		return nil, fmt.Errorf("invalid message: length exceeds maximum")
	}
	if req.Message.Options == 0 {
		return nil, fmt.Errorf("invalid message: no options")
	}

	supported, err := h.Peerstore().SupportsProtocols(req.PeerID, BlockSyncProtocolID, ChainExchangeProtocolID)
	if err != nil {
		return nil, fmt.Errorf("peer: failed to get protocols for peer: %w", err)
	}

	if len(supported) == 0 {
		return nil, fmt.Errorf("peer: does not support protocols")
	}

	s, err := h.NewStream(network.WithNoDial(ctx, "should already have connection"), req.PeerID, protocol.ID(supported[0]))
	if err != nil {
		return nil, fmt.Errorf("new stream: %w", err)
	}
	defer s.Close()

	_ = s.SetWriteDeadline(req.WriteDeadline)
	if err := cborutil.WriteCborRPC(s, &req.Message); err != nil {
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
