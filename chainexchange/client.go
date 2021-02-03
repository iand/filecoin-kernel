package chainexchange

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"time"

	cborutil "github.com/filecoin-project/go-cbor-util"
	"github.com/go-logr/logr"
	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p-core/host"
	"github.com/libp2p/go-libp2p-core/network"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/libp2p/go-libp2p-core/protocol"

	"github.com/iand/filecoin-kernel/chain"
)

const MaxRequestLength = uint64(900)

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
	req := &Request{
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
	req := &Request{
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

	req := &Request{
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
func (c *Client) doRequest(ctx context.Context, req *Request, tipsets []*chain.TipSet) (*validatedResponse, error) {
	// Validate request.
	if req.Length == 0 {
		return nil, fmt.Errorf("invalid request of length 0")
	}
	if req.Length > MaxRequestLength {
		return nil, fmt.Errorf("request length (%d) above maximum (%d)",
			req.Length, MaxRequestLength)
	}
	if req.Options == 0 {
		return nil, fmt.Errorf("request with no options set")
	}

	for _, peer := range c.PeerProvider.Peers() {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("context cancelled: %w", ctx.Err())
		default:
		}

		// Send request, read response.
		res, err := c.sendRequestToPeer(ctx, peer, req)
		if err != nil {
			if !errors.Is(err, network.ErrNoConn) {
				c.Logger.Error(err, "could not send request to peer", "peer", peer.String())
			}
			continue
		}

		// Process and validate response.
		validRes, err := c.processResponse(req, res, tipsets)
		if err != nil {
			c.Logger.Error(err, "processing peer response failed", "peer", peer.String())
			continue
		}

		return validRes, nil
	}

	errString := "doRequest failed for all peers"
	return nil, fmt.Errorf(errString)
}

func (c *Client) sendRequestToPeer(ctx context.Context, peer peer.ID, req *Request) (*Response, error) {
	requiredProtocols := req.RequiredProtocols()

	supported, err := c.Host.Peerstore().SupportsProtocols(peer, requiredProtocols...)
	if err != nil {
		return nil, fmt.Errorf("failed to get protocols for peer: %w", err)
	}
	c.Logger.Info("supported protocols", "protocols", supported)
	if len(supported) == 0 {
		return nil, fmt.Errorf("peer %s does not support protocols %s", peer, requiredProtocols)
	}

	chosenProtocol := supported[0]

	// Open stream to peer.
	stream, err := c.Host.NewStream(
		network.WithNoDial(ctx, "should already have connection"),
		peer,
		protocol.ID(chosenProtocol))
	if err != nil {
		return nil, fmt.Errorf("failed to open stream to peer: %w", err)
	}

	defer stream.Close() //nolint:errcheck

	// Write request.
	_ = stream.SetWriteDeadline(time.Now().Add(WriteReqDeadline))
	if err := cborutil.WriteCborRPC(stream, req); err != nil {
		_ = stream.SetWriteDeadline(time.Time{})
		return nil, err
	}
	_ = stream.SetWriteDeadline(time.Time{}) // clear deadline // FIXME: Needs
	//  its own API (https://github.com/libp2p/go-libp2p-core/issues/162).

	// Read response.
	var res Response

	switch chosenProtocol {
	case BlockSyncProtocolID, ChainExchangeProtocolID:
		var resv1 Response
		err = cborutil.ReadCborRPC(bufio.NewReader(stream), &resv1)
		if err != nil {
			return nil, fmt.Errorf("failed to read chainxchg response: %w", err)
		}

		res.Status = resv1.Status
		res.ErrorMessage = resv1.ErrorMessage
		res.Chain = make([]*BSTipSet, len(resv1.Chain))
		for i := range resv1.Chain {
			res.Chain[i] = &BSTipSet{
				Blocks:   resv1.Chain[i].Blocks,
				Messages: resv1.Chain[i].Messages,
			}
		}
	}

	return &res, nil
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
func (c *Client) processResponse(req *Request, res ExchangeResponse, tipsets []*chain.TipSet) (*validatedResponse, error) {
	err := res.StatusToError()
	if err != nil {
		return nil, fmt.Errorf("status error: %s", err)
	}

	options := parseOptions(req.Options)
	if options.noOptionsSet() {
		// Safety check: this shouldn't have been sent, and even if it did
		// it should have been caught by the peer in its error status.
		return nil, fmt.Errorf("nothing was requested")
	}

	// Verify that the chain segment returned is in the valid range.
	// Note that the returned length might be less than requested.

	resChain := res.TipSets()

	resLength := len(resChain)
	if resLength == 0 {
		return nil, fmt.Errorf("got no chain in successful response")
	}
	if resLength > int(req.Length) {
		return nil, fmt.Errorf("got longer response (%d) than requested (%d)",
			resLength, req.Length)
	}
	if resLength < int(req.Length) && res.GetStatus() != Partial {
		return nil, fmt.Errorf("got less than requested without a proper status: %d", res.GetStatus())
	}

	validRes := &validatedResponse{}
	if options.IncludeHeaders {
		// Check for valid block sets and extract them into `TipSet`s.
		validRes.tipsets = make([]*chain.TipSet, resLength)
		for i := 0; i < resLength; i++ {
			if resChain[i] == nil {
				return nil, fmt.Errorf("response with nil tipset in pos %d", i)
			}
			for blockIdx, block := range resChain[i].GetBlocks() {
				if block == nil {
					return nil, fmt.Errorf("tipset with nil block in pos %d", blockIdx)
					// FIXME: Maybe we should move this check to `NewTipSet`.
				}
			}

			validRes.tipsets[i], err = chain.NewTipSet(resChain[i].GetBlocks())
			if err != nil {
				return nil, fmt.Errorf("invalid tipset blocks at height (head - %d): %w", i, err)
			}
		}

		// Check that the returned head matches the one requested.
		if !cidArrsEqual(validRes.tipsets[0].Cids, req.Head) {
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
			if resChain[i].GetMessages() == nil {
				return nil, fmt.Errorf("no messages included for tipset at height (head - %d)", i)
			}
			validRes.messages[i] = resChain[i].GetMessages()
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
			chain := make([]ResponseTipSet, 0, resLength)
			for i, resChain := range resChain {
				next := &BSTipSet{
					Blocks:   tipsets[i].Blocks,
					Messages: resChain.GetMessages(),
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

func (c *Client) validateCompressedIndices(chain []ResponseTipSet) error {
	resLength := len(chain)
	for tipsetIdx := 0; tipsetIdx < resLength; tipsetIdx++ {
		msgs := chain[tipsetIdx].GetMessages()
		blocksNum := len(chain[tipsetIdx].GetBlocks())

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
