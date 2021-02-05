package chain

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-datastore"
	blockstore "github.com/ipfs/go-ipfs-blockstore"
)

var genesisKey = datastore.NewKey("0")

type Blockstore interface {
	blockstore.Viewer
}

type Store struct {
	blocks Blockstore
	logger logr.Logger
}

// func (s *Store) SetGenesis(ctx context.Context, b *BlockHeader) error {
// 	ts, err := NewTipSet([]*BlockHeader{b})
// 	if err != nil {
// 		return fmt.Errorf("new tipset: %w", err)
// 	}

// 	if err := cs.PutTipSet(ctx, ts); err != nil {
// 		return fmt.Errorf("put tipset: %w", err)
// 	}

// 	return cs.ds.Put(genesisKey, b.Cid().Bytes())
// }

// func (s *Store) GetGenesis() (*BlockHeader, error) {
// 	data, err := s.ds.Get(genesisKey)
// 	if err != nil {
// 		return nil, fmt.Errorf("get genesis cid: %w", err)
// 	}

// 	c, err := cid.Cast(data)
// 	if err != nil {
// 		return nil, err
// 	}

// 	return s.GetBlock(c)
// }

func (s *Store) GetBlockHeader(ctx context.Context, c cid.Cid) (*BlockHeader, error) {
	var bh *BlockHeader
	err := s.blocks.View(c, func(b []byte) (err error) {
		bh, err = DecodeBlock(b)
		return fmt.Errorf("decode block: %w", err)
	})
	return bh, err
}

// func (s *Store) PutTipSet(ctx context.Context, ts *TipSet) error {
// 	for _, b := range ts.Blocks() {
// 		if err := cs.PersistBlockHeaders(b); err != nil {
// 			return err
// 		}
// 	}

// 	expanded, err := cs.expandTipset(ts.Blocks()[0])
// 	if err != nil {
// 		return xerrors.Errorf("errored while expanding tipset: %w", err)
// 	}
// 	log.Debugf("expanded %s into %s\n", ts.Cids(), expanded.Cids())

// 	if err := cs.MaybeTakeHeavierTipSet(ctx, expanded); err != nil {
// 		return xerrors.Errorf("MaybeTakeHeavierTipSet failed in PutTipSet: %w", err)
// 	}
// 	return nil
// }

// func (s *Store) expandTipset(b *types.BlockHeader) (*types.TipSet, error) {
// 	// Hold lock for the whole function for now, if it becomes a problem we can
// 	// fix pretty easily
// 	cs.tstLk.Lock()
// 	defer cs.tstLk.Unlock()

// 	all := []*types.BlockHeader{b}

// 	tsets, ok := cs.tipsets[b.Height]
// 	if !ok {
// 		return types.NewTipSet(all)
// 	}

// 	inclMiners := map[address.Address]cid.Cid{b.Miner: b.Cid()}
// 	for _, bhc := range tsets {
// 		if bhc == b.Cid() {
// 			continue
// 		}

// 		h, err := cs.GetBlock(bhc)
// 		if err != nil {
// 			return nil, xerrors.Errorf("failed to load block (%s) for tipset expansion: %w", bhc, err)
// 		}

// 		if cid, found := inclMiners[h.Miner]; found {
// 			log.Warnf("Have multiple blocks from miner %s at height %d in our tipset cache %s-%s", h.Miner, h.Height, h.Cid(), cid)
// 			continue
// 		}

// 		if types.CidArrsEqual(h.Parents, b.Parents) {
// 			all = append(all, h)
// 			inclMiners[h.Miner] = bhc
// 		}
// 	}

// 	// TODO: other validation...?

// 	return types.NewTipSet(all)
// }
