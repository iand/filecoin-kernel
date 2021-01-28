package kernel

import (
	"io"

	"github.com/filecoin-project/go-state-types/big"
	"github.com/filecoin-project/go-state-types/rt"
	"github.com/ipfs/go-cid"
)

type ActorRegistry interface {
	Lookup(cid.Cid) (rt.VMActor, error)
}

// Actor represents the on-chain state of a single actor.
type Actor struct {
	Code       cid.Cid // CID representing the code associated with the actor
	Head       cid.Cid // CID of the head state object for the actor
	CallSeqNum uint64  // nonce for the next message to be received by the actor (non-zero for accounts only)
	Balance    big.Int // Token balance of the actor
}

// TODO: gen this stub
func (a *Actor) MarshalCBOR(w io.Writer) error {
	return nil
}

// TODO: gen this stub
func (a *Actor) UnmarshalCBOR(r io.Reader) error {
	return nil
}
