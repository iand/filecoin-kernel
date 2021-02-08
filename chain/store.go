package chain

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/filecoin-project/go-state-types/cbor"
	block "github.com/ipfs/go-block-format"
	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-datastore"
	blockstore "github.com/ipfs/go-ipfs-blockstore"
	"github.com/ipld/go-car"
)

var genesisKey = datastore.NewKey("0")

type Blockstore interface {
	blockstore.Viewer

	Has(cid.Cid) (bool, error)

	Get(cid.Cid) (block.Block, error)
	GetSize(cid.Cid) (int, error)

	Put(block.Block) error
	PutMany(bs []block.Block) error

	DeleteBlock(cid.Cid) error
}

type Metastore interface {
	datastore.Write
	Get(key datastore.Key) (value []byte, err error)
}

type Store struct {
	blocks Blockstore
	meta   Metastore

	heaviestMu sync.Mutex
	heaviest   *TipSet
}

func NewStore(blocks Blockstore, meta Metastore) (*Store, error) {
	return &Store{
		blocks: blocks,
		meta:   meta,
	}, nil
}

func (s *Store) PutCbor(ctx context.Context, v cbor.Marshaler) (cid.Cid, error) {
	b, err := EncodeAsBlock(v)
	if err != nil {
		return cid.Undef, fmt.Errorf("encode as block: %w", err)
	}

	if err := s.blocks.Put(b); err != nil {
		return cid.Undef, fmt.Errorf("put: %w", err)
	}

	return b.Cid(), nil
}

func (s *Store) PutBlockHeader(ctx context.Context, bh *BlockHeader) (cid.Cid, error) {
	return s.PutCbor(ctx, bh)
}

func (s *Store) PutManyBlockHeaders(bhs []*BlockHeader) error {
	bs := make([]block.Block, 0, len(bhs))
	for _, bh := range bhs {
		b, err := EncodeAsBlock(bh)
		if err != nil {
			return fmt.Errorf("encode block header: %w", err)
		}
		bs = append(bs, b)
	}
	return s.blocks.PutMany(bs)
}

func (s *Store) GetBlockHeader(ctx context.Context, c cid.Cid) (*BlockHeader, error) {
	var bh *BlockHeader
	err := s.blocks.View(c, func(b []byte) (err error) {
		bh, err = DecodeBlockHeader(b)
		if err != nil {
			return fmt.Errorf("decode block: %w", err)
		}
		return nil
	})
	return bh, err
}

func (s *Store) GetMessage(ctx context.Context, c cid.Cid) (*Message, error) {
	var msg *Message
	err := s.blocks.View(c, func(b []byte) (err error) {
		return msg.UnmarshalCBOR(bytes.NewReader(b))
	})
	return msg, err
}

func (s *Store) PutMessage(ctx context.Context, msg *Message) (cid.Cid, error) {
	return s.PutCbor(ctx, msg)
}

func (s *Store) GetSignedMessage(ctx context.Context, c cid.Cid) (*SignedMessage, error) {
	var msg *SignedMessage
	err := s.blocks.View(c, func(b []byte) (err error) {
		return msg.UnmarshalCBOR(bytes.NewReader(b))
	})
	return msg, err
}

func (s *Store) PutSignedMessage(ctx context.Context, msg *SignedMessage) (cid.Cid, error) {
	return s.PutCbor(ctx, msg)
}

func (s *Store) LoadTipSet(ctx context.Context, cids []cid.Cid) (*TipSet, error) {
	bhs := make([]*BlockHeader, 0, len(cids))
	for _, c := range cids {
		bh, err := s.GetBlockHeader(ctx, c)
		if err != nil {
			return nil, fmt.Errorf("get block header: %w", err)
		}
		bhs = append(bhs, bh)
	}

	return NewTipSet(bhs)
}

func (s *Store) Import(ctx context.Context, r io.Reader) (*TipSet, error) {
	header, err := car.LoadCar(s.blocks, r)
	if err != nil {
		return nil, fmt.Errorf("load car: %w", err)
	}

	root, err := s.LoadTipSet(ctx, header.Roots)
	if err != nil {
		return nil, fmt.Errorf("load root tipset: %w", err)
	}

	return root, nil
}

func (s *Store) GetGenesis(ctx context.Context) (cid.Cid, error) {
	data, err := s.meta.Get(genesisKey)
	if err != nil {
		return cid.Undef, err
	}

	c, err := cid.Cast(data)
	if err != nil {
		return cid.Undef, err
	}

	return c, nil
}

func (s *Store) SetGenesis(ctx context.Context, c cid.Cid) error {
	return s.meta.Put(genesisKey, c.Bytes())
}

func (s *Store) HeaviestTipSet(ctx context.Context) (*TipSet, error) {
	s.heaviestMu.Lock()
	defer s.heaviestMu.Unlock()
	return s.heaviest, nil
}

func (s *Store) SetHeaviestTipSet(ctx context.Context, ts *TipSet) error {
	s.heaviestMu.Lock()
	defer s.heaviestMu.Unlock()
	s.heaviest = ts
	return nil
}
