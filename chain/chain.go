package chain

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/go-state-types/abi"
	"github.com/filecoin-project/go-state-types/big"
	"github.com/filecoin-project/go-state-types/crypto"
	proof3 "github.com/filecoin-project/specs-actors/v3/actors/runtime/proof"
	"github.com/minio/blake2b-simd"
	mh "github.com/multiformats/go-multihash"

	"github.com/ipfs/go-cid"
)

//go:generate go run gen.go

type TipSet struct {
	Cids   []cid.Cid
	Blocks []*BlockHeader
	Height abi.ChainEpoch
}

type BlockHeader struct {
	Miner address.Address // 0

	Ticket *Ticket // 1

	ElectionProof *ElectionProof // 2

	BeaconEntries []BeaconEntry // 3

	WinPoStProof []proof3.PoStProof // 4

	Parents []cid.Cid // 5

	ParentWeight big.Int // 6

	Height abi.ChainEpoch // 7

	ParentStateRoot cid.Cid // 8

	ParentMessageReceipts cid.Cid // 8

	Messages cid.Cid // 10

	BLSAggregate *crypto.Signature // 11

	Timestamp uint64 // 12

	BlockSig *crypto.Signature // 13

	ForkSignaling uint64 // 14

	// ParentBaseFee is the base fee after executing parent tipset
	ParentBaseFee abi.TokenAmount // 15
}

func (bh *BlockHeader) Cid() (cid.Cid, error) {
	buf := new(bytes.Buffer)
	if err := bh.MarshalCBOR(buf); err != nil {
		return cid.Undef, fmt.Errorf("marshal to cbor: %w", err)
	}

	c, err := abi.CidBuilder.Sum(buf.Bytes())
	if err != nil {
		return cid.Undef, fmt.Errorf("sum cbor bytes: %w", err)
	}

	return c, nil
}

type FullTipSet struct {
	Blocks []*FullBlock
}

type FullBlock struct {
	Header        *BlockHeader
	BlsMessages   []*Message
	SecpkMessages []*SignedMessage
}

func DecodeBlockHeader(b []byte) (*BlockHeader, error) {
	var bh BlockHeader
	if err := bh.UnmarshalCBOR(bytes.NewReader(b)); err != nil {
		return nil, err
	}

	return &bh, nil
}

type BeaconEntry struct {
	Round uint64
	Data  []byte
}

type ElectionProof struct {
	WinCount int64
	VRFProof []byte
}

type Ticket struct {
	VRFProof []byte
}

func (t *Ticket) Equals(ot *Ticket) bool {
	return bytes.Equal(t.VRFProof, ot.VRFProof)
}

func (t *Ticket) Less(o *Ticket) bool {
	tDigest := blake2b.Sum256(t.VRFProof)
	oDigest := blake2b.Sum256(o.VRFProof)
	return bytes.Compare(tDigest[:], oDigest[:]) < 0
}

type Message struct {
	Version uint64

	To   address.Address
	From address.Address

	Nonce uint64

	Value abi.TokenAmount

	GasLimit   int64
	GasFeeCap  abi.TokenAmount
	GasPremium abi.TokenAmount

	Method abi.MethodNum
	Params []byte
}

type SignedMessage struct {
	Message   Message
	Signature crypto.Signature
}

func NewTipSet(blks []*BlockHeader) (*TipSet, error) {
	if len(blks) == 0 {
		return nil, fmt.Errorf("tipsets must contain at least one block")
	}

	type bc struct {
		bh  *BlockHeader
		cid cid.Cid
	}

	bcs := make([]bc, len(blks))
	for i, b := range blks {
		var err error
		bcs[i].cid, err = b.Cid()
		if err != nil {
			return nil, fmt.Errorf("block %d: invalid cid: %w", i, err)
		}
		bcs[i].bh = b
	}

	sort.Slice(bcs, func(i, j int) bool {
		ti := bcs[i].bh.Ticket
		tj := bcs[j].bh.Ticket

		if ti.Equals(tj) {
			return bytes.Compare(bcs[i].cid.Bytes(), bcs[j].cid.Bytes()) < 0
		}

		return ti.Less(tj)
	})

	var ts TipSet

	ts.Cids = []cid.Cid{bcs[0].cid}
	ts.Blocks = []*BlockHeader{bcs[0].bh}

	for i, b := range bcs[1:] {
		if b.bh.Height != bcs[0].bh.Height {
			return nil, fmt.Errorf("blocks have different heights")
		}

		if len(bcs[0].bh.Parents) != len(b.bh.Parents) {
			return nil, fmt.Errorf("blocks different parents")
		}

		for j, cid := range b.bh.Parents {
			if cid != bcs[0].bh.Parents[j] {
				return nil, fmt.Errorf("blocks have different parents")
			}
		}

		ts.Cids = append(ts.Cids, bcs[i].cid)
		ts.Blocks = append(ts.Blocks, bcs[i].bh)

	}
	ts.Height = blks[0].Height

	return &ts, nil
}

func (ts *TipSet) IsChildOf(parent *TipSet) bool {
	return CidArrsEqual(ts.Blocks[0].Parents, parent.Cids) && ts.Height > parent.Height
}

func CidArrsEqual(a, b []cid.Cid) bool {
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

var DefaultHashFunction = uint64(mh.BLAKE2B_MIN + 31)

var MessageCidPrefix = cid.Prefix{
	Version:  1,
	Codec:    cid.DagCBOR,
	MhType:   DefaultHashFunction,
	MhLength: 32,
}
