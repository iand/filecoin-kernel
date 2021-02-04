package chainexchange

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p-core/host"
	"github.com/libp2p/go-libp2p-core/network"
	"github.com/libp2p/go-libp2p-core/peer"

	"github.com/iand/filecoin-kernel/chain"
)

const (
	DefaultWriteRequestTimeout  = 5 * time.Second
	DefaultReadResponseTimeout  = 300 * time.Second
	DefaultWriteResponseTimeout = 60 * time.Second
)

type PeerProvider interface {
	// Peers provides a list of peers that requests should be sent to
	Peers() []peer.ID
}

type Client struct {
	Host         host.Host
	Logger       logr.Logger
	PeerProvider PeerProvider
}

func (c *Client) WithLogger(l logr.Logger) *Client {
	return &Client{
		Host:         c.Host,
		PeerProvider: c.PeerProvider,
		Logger:       l,
	}
}

func (c *Client) GetBlocks(ctx context.Context, tsk []cid.Cid, count int) ([]*chain.TipSet, error) {
	req := &SyncMessage{
		Head:    tsk,
		Length:  uint64(count),
		Options: Headers,
	}

	validRes, err := c.doRequest(ctx, req, nil)
	if err != nil {
		return nil, err
	}

	return validRes.tipsets, nil
}

func (c *Client) GetFullTipSet(ctx context.Context, tsk []cid.Cid) (*FullTipSet, error) {
	req := &SyncMessage{
		Head:    tsk,
		Length:  1,
		Options: Headers | Messages,
	}

	validRes, err := c.doRequest(ctx, req, nil)
	if err != nil {
		return nil, err
	}

	return validRes.toFullTipSets()[0], nil
}

func (c *Client) GetChainMessages(ctx context.Context, tipsets []*chain.TipSet) ([]*CompactedMessages, error) {
	head := tipsets[0]
	length := uint64(len(tipsets))

	req := &SyncMessage{
		Head:    head.Cids,
		Length:  length,
		Options: Messages,
	}

	validRes, err := c.doRequest(ctx, req, tipsets)
	if err != nil {
		return nil, err
	}

	return validRes.messages, nil
}

// Main logic of the client request service. The provided `Request`
// is sent to the `singlePeer` if one is indicated or to all available
// ones otherwise. The response is processed and validated according
// to the `Request` options. Either a `validatedResponse` is returned
// (which can be safely accessed), or an `error` that may represent
// either a response error status, a failed validation or an internal
// error.
//
// This is the internal single point of entry for all external-facing
// APIs, currently we have 3 very heterogeneous services exposed:
// * GetBlocks:         Headers
// * GetFullTipSet:     Headers | Messages
// * GetChainMessages:            Messages
// This function handles all the different combinations of the available
// request options without disrupting external calls. In the future the
// consumers should be forced to use a more standardized service and
// adhere to a single API derived from this function.
func (c *Client) doRequest(ctx context.Context, msg *SyncMessage, tipsets []*chain.TipSet) (*validatedResponse, error) {
	for _, peer := range c.PeerProvider.Peers() {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		req := &Request{
			PeerID:        peer,
			Message:       *msg,
			ReadDeadline:  time.Now().Add(DefaultReadResponseTimeout),
			WriteDeadline: time.Now().Add(DefaultWriteRequestTimeout),
		}

		// Send request, read response.
		res, err := Send(ctx, c.Host, req)
		if err != nil {
			if !errors.Is(err, network.ErrNoConn) {
				c.Logger.Error(err, "could not send request to peer", "peer", peer.String())
			}
			continue
		}

		// Process and validate response.
		validRes, err := c.validateResponse(msg, &res.Message, tipsets)
		if err != nil {
			c.Logger.Error(err, "processing peer response failed", "peer", peer.String())
			continue
		}

		return validRes, nil
	}

	errString := "doRequest failed for all peers"
	return nil, fmt.Errorf(errString)
}

// Process and validate response. Check the status, the integrity of the
// information returned, and that it matches the request. Extract the information
// into a `validatedResponse` for the external-facing APIs to select what they
// need.
//
// We are conflating in the single error returned both status and validation
// errors. Peer penalization should happen here then, before returning, so
// we can apply the correct penalties depending on the cause of the error.
// FIXME: Add the `peer` as argument once we implement penalties.
func (c *Client) validateResponse(smsg *SyncMessage, cmsg *ChainMessage, tipsets []*chain.TipSet) (*validatedResponse, error) {
	err := StatusToError(cmsg)
	if err != nil {
		return nil, fmt.Errorf("status error: %s", err)
	}

	options := parseOptions(smsg.Options)
	if options.noOptionsSet() {
		// Safety check: this shouldn't have been sent, and even if it did
		// it should have been caught by the peer in its error status.
		return nil, fmt.Errorf("nothing was requested")
	}

	// Verify that the chain segment returned is in the valid range.
	// Note that the returned length might be less than requested.

	resChain := cmsg.Chain

	resLength := len(resChain)
	if resLength == 0 {
		return nil, fmt.Errorf("got no chain in successful response")
	}
	if resLength > int(smsg.Length) {
		return nil, fmt.Errorf("got longer response (%d) than requested (%d)",
			resLength, smsg.Length)
	}
	if resLength < int(smsg.Length) && cmsg.Status != Partial {
		return nil, fmt.Errorf("got less than requested without a proper status: %d", cmsg.Status)
	}

	validRes := &validatedResponse{}
	if options.IncludeHeaders {
		// Check for valid block sets and extract them into `TipSet`s.
		validRes.tipsets = make([]*chain.TipSet, resLength)
		for i := 0; i < resLength; i++ {
			if resChain[i] == nil {
				return nil, fmt.Errorf("response with nil tipset in pos %d", i)
			}
			for blockIdx, block := range resChain[i].Blocks {
				if block == nil {
					return nil, fmt.Errorf("tipset with nil block in pos %d", blockIdx)
					// FIXME: Maybe we should move this check to `NewTipSet`.
				}
			}

			validRes.tipsets[i], err = chain.NewTipSet(resChain[i].Blocks)
			if err != nil {
				return nil, fmt.Errorf("invalid tipset blocks at height (head - %d): %w", i, err)
			}
		}

		// Check that the returned head matches the one requested.
		if !cidArrsEqual(validRes.tipsets[0].Cids, smsg.Head) {
			return nil, fmt.Errorf("returned chain head does not match request")
		}

		// Check `TipSet`s are connected (valid chain).
		for i := 0; i < len(validRes.tipsets)-1; i++ {
			if !validRes.tipsets[i].IsChildOf(validRes.tipsets[i+1]) {
				return nil, fmt.Errorf("tipsets are not connected at height (head - %d)/(head - %d)",
					i, i+1)
				// FIXME: Maybe give more information here, like CIDs.
			}
		}
	}

	if options.IncludeMessages {
		validRes.messages = make([]*CompactedMessages, resLength)
		for i := 0; i < resLength; i++ {
			if resChain[i].Messages == nil {
				return nil, fmt.Errorf("no messages included for tipset at height (head - %d)", i)
			}
			validRes.messages[i] = resChain[i].Messages
		}

		if options.IncludeHeaders {
			// If the headers were also returned check that the compression
			// indexes are valid before `toFullTipSets()` is called by the
			// consumer.
			err := c.validateCompressedIndices(resChain)
			if err != nil {
				return nil, err
			}
		} else {
			// If we didn't request the headers they should have been provided
			// by the caller.
			if len(tipsets) < len(resChain) {
				return nil, fmt.Errorf("not enought tipsets provided for message response validation, needed %d, have %d", len(resChain), len(tipsets))
			}
			chain := make([]*BSTipSet, 0, resLength)
			for i, resChain := range resChain {
				next := &BSTipSet{
					Blocks:   tipsets[i].Blocks,
					Messages: resChain.Messages,
				}
				chain = append(chain, next)
			}

			err := c.validateCompressedIndices(chain)
			if err != nil {
				return nil, err
			}
		}
	}

	return validRes, nil
}

func (c *Client) validateCompressedIndices(chain []*BSTipSet) error {
	resLength := len(chain)
	for tipsetIdx := 0; tipsetIdx < resLength; tipsetIdx++ {
		msgs := chain[tipsetIdx].Messages
		blocksNum := len(chain[tipsetIdx].Blocks)

		if len(msgs.BlsIncludes) != blocksNum {
			return fmt.Errorf("BlsIncludes (%d) does not match number of blocks (%d)",
				len(msgs.BlsIncludes), blocksNum)
		}

		if len(msgs.SecpkIncludes) != blocksNum {
			return fmt.Errorf("SecpkIncludes (%d) does not match number of blocks (%d)",
				len(msgs.SecpkIncludes), blocksNum)
		}

		for blockIdx := 0; blockIdx < blocksNum; blockIdx++ {
			for _, mi := range msgs.BlsIncludes[blockIdx] {
				if int(mi) >= len(msgs.Bls) {
					return fmt.Errorf("index in BlsIncludes (%d) exceeds number of messages (%d)",
						mi, len(msgs.Bls))
				}
			}

			for _, mi := range msgs.SecpkIncludes[blockIdx] {
				if int(mi) >= len(msgs.Secpk) {
					return fmt.Errorf("index in SecpkIncludes (%d) exceeds number of messages (%d)",
						mi, len(msgs.Secpk))
				}
			}
		}
	}

	return nil
}

func cidArrsEqual(a, b []cid.Cid) bool {
	if len(a) != len(b) {
		return false
	}

	// order ignoring compare...
	s := make(map[cid.Cid]bool)
	for _, c := range a {
		s[c] = true
	}

	for _, c := range b {
		if !s[c] {
			return false
		}
	}
	return true
}

func parseOptions(optfield uint64) *parsedOptions {
	return &parsedOptions{
		IncludeHeaders:  optfield&(uint64(Headers)) != 0,
		IncludeMessages: optfield&(uint64(Messages)) != 0,
	}
}

// Decompressed options into separate struct members for easy access
// during internal processing..
type parsedOptions struct {
	IncludeHeaders  bool
	IncludeMessages bool
}

func (o *parsedOptions) noOptionsSet() bool {
	return !o.IncludeHeaders && !o.IncludeMessages
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

func StatusToError(msg *ChainMessage) error {
	switch msg.Status {
	case Ok, Partial:
		return nil
		// FIXME: Consider if we want to not process `Partial` responses
		//  and return an error instead.
	case NotFound:
		return fmt.Errorf("not found")
	case GoAway:
		return fmt.Errorf("not handling 'go away' chainxchg responses yet")
	case InternalError:
		return fmt.Errorf("block sync peer errored: %s", msg.ErrorMessage)
	case BadRequest:
		return fmt.Errorf("block sync request invalid: %s", msg.ErrorMessage)
	default:
		return fmt.Errorf("unrecognized response code: %d", msg.Status)
	}
}

// FullTipSet is an expanded version of the TipSet that contains all the blocks and messages
type FullTipSet struct {
	Blocks []*FullBlock
}

type FullBlock struct {
	Header        *chain.BlockHeader
	BlsMessages   []*chain.Message
	SecpkMessages []*chain.SignedMessage
}

type CompactedMessages struct {
	Bls         []*chain.Message
	BlsIncludes [][]uint64

	Secpk         []*chain.SignedMessage
	SecpkIncludes [][]uint64
}
