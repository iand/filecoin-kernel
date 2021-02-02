package kernel

import (
	"fmt"
	"io"
	// abi "github.com/filecoin-project/go-state-types/abi"
	// crypto "github.com/filecoin-project/go-state-types/crypto"
	// exitcode "github.com/filecoin-project/go-state-types/exitcode"
	// proof "github.com/filecoin-project/specs-actors/actors/runtime/proof"
	// cid "github.com/ipfs/go-cid"
	cbg "github.com/whyrusleeping/cbor-gen"
	xerrors "golang.org/x/xerrors"
)

// var _ = xerrors.Errorf

var lengthBufActor = []byte{132}

func (t *Actor) MarshalCBOR(w io.Writer) error {
	if t == nil {
		_, err := w.Write(cbg.CborNull)
		return err
	}
	if _, err := w.Write(lengthBufActor); err != nil {
		return err
	}

	scratch := make([]byte, 9)

	// t.Code (cid.Cid) (struct)

	if err := cbg.WriteCidBuf(scratch, w, t.Code); err != nil {
		return xerrors.Errorf("failed to write cid field t.Code: %w", err)
	}

	// t.Head (cid.Cid) (struct)

	if err := cbg.WriteCidBuf(scratch, w, t.Head); err != nil {
		return xerrors.Errorf("failed to write cid field t.Head: %w", err)
	}

	// t.CallSeqNum (uint64) (uint64)

	if err := cbg.WriteMajorTypeHeaderBuf(scratch, w, cbg.MajUnsignedInt, uint64(t.CallSeqNum)); err != nil {
		return err
	}

	// t.Balance (big.Int) (struct)
	if err := t.Balance.MarshalCBOR(w); err != nil {
		return err
	}
	return nil
}

func (t *Actor) UnmarshalCBOR(r io.Reader) error {
	*t = Actor{}

	br := cbg.GetPeeker(r)
	scratch := make([]byte, 8)

	maj, extra, err := cbg.CborReadHeaderBuf(br, scratch)
	if err != nil {
		return err
	}
	if maj != cbg.MajArray {
		return fmt.Errorf("cbor input should be of type array")
	}

	if extra != 4 {
		return fmt.Errorf("cbor input had wrong number of fields")
	}

	// t.Code (cid.Cid) (struct)

	{

		c, err := cbg.ReadCid(br)
		if err != nil {
			return xerrors.Errorf("failed to read cid field t.Code: %w", err)
		}

		t.Code = c

	}
	// t.Head (cid.Cid) (struct)

	{

		c, err := cbg.ReadCid(br)
		if err != nil {
			return xerrors.Errorf("failed to read cid field t.Head: %w", err)
		}

		t.Head = c

	}
	// t.CallSeqNum (uint64) (uint64)

	{

		maj, extra, err = cbg.CborReadHeaderBuf(br, scratch)
		if err != nil {
			return err
		}
		if maj != cbg.MajUnsignedInt {
			return fmt.Errorf("wrong type for uint64 field")
		}
		t.CallSeqNum = uint64(extra)

	}
	// t.Balance (big.Int) (struct)

	{
		if err := t.Balance.UnmarshalCBOR(br); err != nil {
			return xerrors.Errorf("unmarshaling t.Balance: %w", err)
		}
	}
	return nil
}
