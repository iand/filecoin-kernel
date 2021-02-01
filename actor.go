package kernel

import (
	"context"
	"fmt"
	"io"

	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/go-state-types/abi"
	"github.com/filecoin-project/go-state-types/big"
	"github.com/filecoin-project/go-state-types/cbor"
	"github.com/filecoin-project/go-state-types/rt"
	"github.com/filecoin-project/specs-actors/v3/actors/util/adt"
	"github.com/ipfs/go-cid"
	ipldcbor "github.com/ipfs/go-ipld-cbor"
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

type ActorStore interface {
	// SetActorState stores the state and updates the on-chain state of the addressed actor
	SetActorState(ctx context.Context, addr address.Address, state cbor.Marshaler) error

	GetActor(ctx context.Context, addr address.Address) (*Actor, bool, error)
	SetActor(ctx context.Context, addr address.Address, act *Actor) error
	MutateActor(ctx context.Context, addr address.Address, f func(*Actor) error) error
}

var _ ActorStore = (*xActorStore)(nil)

type xActorStore struct {
	states   ipldcbor.IpldStore // off-chain states
	actors   *adt.Map           // on-chain actor representations
	registry ActorRegistry      // registry of actor types
}

// SetActorState stores the state and updates the on-chain state of the addressed actor
func (as *xActorStore) SetActorState(ctx context.Context, addr address.Address, state cbor.Marshaler) error {
	if addr.Protocol() != address.ID {
		return fmt.Errorf("address must use ID protocol")
	}
	return as.MutateActor(ctx, addr, func(act *Actor) error {
		stateCid, err := as.states.Put(ctx, state)
		if err != nil {
			return err
		}

		act.Head = stateCid
		return nil
	})
}

func (as *xActorStore) GetActor(ctx context.Context, addr address.Address) (*Actor, bool, error) {
	if addr.Protocol() != address.ID {
		return nil, false, fmt.Errorf("address must use ID protocol")
	}
	var act Actor
	found, err := as.actors.Get(abi.AddrKey(addr), &act)
	return &act, found, err
}

func (as *xActorStore) SetActor(ctx context.Context, addr address.Address, act *Actor) error {
	if addr.Protocol() != address.ID {
		return fmt.Errorf("address must use ID protocol")
	}
	if err := as.actors.Put(abi.AddrKey(addr), act); err != nil {
		return fmt.Errorf("failed to put actor: %v", err)
	}
	return nil
}

func (as *xActorStore) MutateActor(ctx context.Context, addr address.Address, f func(*Actor) error) error {
	if addr.Protocol() != address.ID {
		return fmt.Errorf("address must use ID protocol")
	}
	var act Actor
	found, err := as.actors.Get(abi.AddrKey(addr), &act)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("could not find actor %s to set state", addr)
	}

	if err := f(&act); err != nil {
		return err
	}
	return as.actors.Put(abi.AddrKey(addr), &act)
}
