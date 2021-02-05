package sync

import (
	"bytes"
	"context"
	"fmt"

	"github.com/go-logr/logr"
	bserv "github.com/ipfs/go-blockservice"
	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p-core/peer"

	"github.com/iand/filecoin-kernel/chain"
	"github.com/iand/filecoin-kernel/protocols/blocks"
)

type cidIndex struct {
	cids  []cid.Cid
	index map[cid.Cid]int
}

func newCidIndex(cids []cid.Cid) (*cidIndex, error) {
	ci := &cidIndex{
		cids:  cids,
		index: make(map[cid.Cid]int, len(cids)),
	}

	for i, c := range cids {
		if c.Prefix() != chain.MessageCidPrefix {
			return nil, fmt.Errorf("invalid message cid: %s", c)
		}
		ci.index[c] = i
	}
	if len(cids) != len(ci.index) {
		return nil, fmt.Errorf("duplicate message cid")
	}

	return ci, nil
}

func (ci *cidIndex) Cids() []cid.Cid {
	return ci.cids
}

func (ci *cidIndex) Index(c cid.Cid) (int, bool) {
	i, found := ci.index[c]
	return i, found
}

func fetchFullBlock(ctx context.Context, from peer.ID, bsrv bserv.BlockService, bm *blocks.BlockMessage) (*chain.FullBlock, error) {
	session := bserv.NewSession(ctx, bsrv)

	bCidIndex, err := newCidIndex(bm.BlsMessages)
	if err != nil {
		return nil, fmt.Errorf("indexing bls cids: %w", err)
	}

	bmsgs, err := fetchMessages(ctx, session, bCidIndex)
	if err != nil {
		return nil, fmt.Errorf("fetch bls messages: %w", err)
	}

	sCidIndex, err := newCidIndex(bm.SecpkMessages)
	if err != nil {
		return nil, fmt.Errorf("indexing secpk cids: %w", err)
	}

	smsgs, err := fetchSignedMessages(ctx, session, sCidIndex)
	if err != nil {
		return nil, fmt.Errorf("fetch secpk messages: %w", err)
	}

	return &chain.FullBlock{
		Header:        bm.Header,
		BlsMessages:   bmsgs,
		SecpkMessages: smsgs,
	}, nil
}

func fetchSignedMessages(ctx context.Context, getter bserv.BlockGetter, ci *cidIndex) ([]*chain.SignedMessage, error) {
	logger := logr.FromContextOrDiscard(ctx)

	bmsgs := make([]*chain.SignedMessage, len(ci.Cids()))

	for b := range getter.GetBlocks(ctx, ci.Cids()) {
		logger.Info("fetched block for message", "cid", b.Cid())

		i, expected := ci.Index(b.Cid())
		if !expected {
			logger.Info("received unexpected block", "cid", b.Cid())
			continue
		}

		if bmsgs[i] != nil {
			// already received this message
			continue
		}

		var msg chain.SignedMessage
		if err := msg.UnmarshalCBOR(bytes.NewReader(b.RawData())); err != nil {
			return nil, fmt.Errorf("decode signed message: %w", err)
		}

		bmsgs[i] = &msg
	}

	return bmsgs, nil
}

func fetchMessages(ctx context.Context, getter bserv.BlockGetter, ci *cidIndex) ([]*chain.Message, error) {
	logger := logr.FromContextOrDiscard(ctx)

	bmsgs := make([]*chain.Message, len(ci.Cids()))

	for b := range getter.GetBlocks(ctx, ci.Cids()) {
		logger.Info("fetched block for signed message", "cid", b.Cid())

		i, expected := ci.Index(b.Cid())
		if !expected {
			logger.Info("received unexpected block", "cid", b.Cid())
			continue
		}

		if bmsgs[i] != nil {
			// already received this message
			continue
		}

		var msg chain.Message
		if err := msg.UnmarshalCBOR(bytes.NewReader(b.RawData())); err != nil {
			return nil, fmt.Errorf("decode message: %w", err)
		}

		bmsgs[i] = &msg
	}

	return bmsgs, nil
}
