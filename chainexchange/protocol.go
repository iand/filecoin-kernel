package chainexchange

import (
	"fmt"
	"time"

	"github.com/ipfs/go-cid"

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
	// Extracted constants from the code.
	// FIXME: Should be reviewed and confirmed.
	SuccessPeerTagValue = 25
	WriteReqDeadline    = 5 * time.Second
	ReadResDeadline     = 300 * time.Second
	ReadResMinSpeed     = 50 << 10
	ShufflePeersPrefix  = 16
	WriteResDeadline    = 60 * time.Second
)

// FullTipSet is an expanded version of the TipSet that contains all the blocks and messages
type FullTipSet struct {
	Blocks []*FullBlock
}

type FullBlock struct {
	Header        *chain.BlockHeader
	BlsMessages   []*chain.Message
	SecpkMessages []*chain.SignedMessage
}

// All messages of a single tipset compacted together instead
// of grouped by block to save space, since there are normally
// many repeated messages per tipset in different blocks.
//
// `BlsIncludes`/`SecpkIncludes` matches `Bls`/`Secpk` messages
// to blocks in the tipsets with the format:
// `BlsIncludes[BI][MI]`
//  * BI: block index in the tipset.
//  * MI: message index in `Bls` list
//
// FIXME: The logic to decompress this structure should belong
//  to itself, not to the consumer.
type CompactedMessages struct {
	Bls         []*chain.Message
	BlsIncludes [][]uint64

	Secpk         []*chain.SignedMessage
	SecpkIncludes [][]uint64
}

type Request struct {
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

func (r *Request) RequiredProtocols() []string {
	return []string{BlockSyncProtocolID, ChainExchangeProtocolID}
}

type status uint64

const (
	Ok status = 0
	// We could not fetch all blocks requested (but at least we returned
	// the `Head` requested). Not considered an error.
	Partial = 101

	// Errors
	NotFound      = 201
	GoAway        = 202
	InternalError = 203
	BadRequest    = 204
)

type Response struct {
	Status status
	// String that complements the error status when converting to an
	// internal error (see `statusToError()`).
	ErrorMessage string

	Chain []*BSTipSet
}

func (r *Response) TipSets() []ResponseTipSet {
	tsl := make([]ResponseTipSet, len(r.Chain))
	for i := range r.Chain {
		tsl[i] = r.Chain[i]
	}

	return tsl
}

// Response that has been validated according to the protocol
// and can be safely accessed.
type validatedResponse struct {
	tipsets []*chain.TipSet
	// List of all messages per tipset (grouped by tipset,
	// not by block, hence a single index like `tipsets`).
	messages []*CompactedMessages
}

// Decompress messages and form full tipsets with them. The headers
// need to have been requested as well.
func (res *validatedResponse) toFullTipSets() []*FullTipSet {
	if len(res.tipsets) == 0 || len(res.tipsets) != len(res.messages) {
		// This decompression can only be done if both headers and
		// messages are returned in the response. (The second check
		// is already implied by the guarantees of `validatedResponse`,
		// added here just for completeness.)
		return nil
	}
	ftsList := make([]*FullTipSet, len(res.tipsets))
	for tipsetIdx := range res.tipsets {
		fts := &FullTipSet{} // FIXME: We should use the `NewFullTipSet` API.
		msgs := res.messages[tipsetIdx]
		for blockIdx, b := range res.tipsets[tipsetIdx].Blocks {
			fb := &FullBlock{
				Header: b,
			}
			for _, mi := range msgs.BlsIncludes[blockIdx] {
				fb.BlsMessages = append(fb.BlsMessages, msgs.Bls[mi])
			}
			for _, mi := range msgs.SecpkIncludes[blockIdx] {
				fb.SecpkMessages = append(fb.SecpkMessages, msgs.Secpk[mi])
			}

			fts.Blocks = append(fts.Blocks, fb)
		}
		ftsList[tipsetIdx] = fts
	}
	return ftsList
}

// FIXME: Rename.
type BSTipSet struct {
	// List of blocks belonging to a single tipset to which the
	// `CompactedMessages` are linked.
	Blocks   []*chain.BlockHeader
	Messages *CompactedMessages
}

func (b *BSTipSet) GetBlocks() []*chain.BlockHeader {
	return b.Blocks
}

func (b *BSTipSet) GetMessages() *CompactedMessages {
	return b.Messages
}

type ExchangeResponse interface {
	GetStatus() status
	StatusToError() error
	TipSets() []ResponseTipSet
}

type ResponseTipSet interface {
	GetBlocks() []*chain.BlockHeader
	GetMessages() *CompactedMessages
}

func (res *Response) GetStatus() status {
	return res.Status
}

// Convert status to internal error.
func (res *Response) StatusToError() error {
	switch res.Status {
	case Ok, Partial:
		return nil
		// FIXME: Consider if we want to not process `Partial` responses
		//  and return an error instead.
	case NotFound:
		return fmt.Errorf("not found")
	case GoAway:
		return fmt.Errorf("not handling 'go away' chainxchg responses yet")
	case InternalError:
		return fmt.Errorf("block sync peer errored: %s", res.ErrorMessage)
	case BadRequest:
		return fmt.Errorf("block sync request invalid: %s", res.ErrorMessage)
	default:
		return fmt.Errorf("unrecognized response code: %d", res.Status)
	}
}
