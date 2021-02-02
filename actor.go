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
	"github.com/filecoin-project/specs-actors/v3/actors/builtin"
	init_ "github.com/filecoin-project/specs-actors/v3/actors/builtin/init"
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

type xActorStore struct {
	states    ipldcbor.IpldStore // off-chain states
	actors    *adt.Map           // on-chain actor representations
	registry  ActorRegistry      // registry of actor types
	stateRoot cid.Cid
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

func (as *xActorStore) LoadActorState(ctx context.Context, addr address.Address, state cbor.Unmarshaler) error {
	if addr.Protocol() != address.ID {
		return fmt.Errorf("address must use ID protocol")
	}

	var act Actor
	found, err := as.actors.Get(abi.AddrKey(addr), &act)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("could not find actor %s", addr)
	}

	return as.states.Get(ctx, act.Head, state)
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

func (as *xActorStore) DeleteActor(ctx context.Context, addr address.Address) error {
	panic("xActorStore.DeleteActor not implemented")
}

func (as *xActorStore) NormalizeAddress(addr address.Address) (address.Address, bool) {
	// short-circuit if the address is already an ID address
	if addr.Protocol() == address.ID {
		return addr, true
	}

	// get a view into the actor state
	var state init_.State
	if err := as.LoadActorState(context.TODO(), builtin.InitActorAddr, &state); err != nil {
		panic(err)
	}

	idAddr, found, err := state.ResolveAddress(adt.WrapStore(context.TODO(), as.states), addr)
	if err != nil {
		panic(err)
	}
	return idAddr, found
}

func (as *xActorStore) Checkpoint() (cid.Cid, error) {
	// flush
	root, err := as.actors.Root()
	if err != nil {
		return cid.Undef, err
	}
	as.stateRoot = root
	return root, nil
}

func (as *xActorStore) Rollback(root cid.Cid) error {
	actors, err := adt.AsMap(adt.WrapStore(context.TODO(), as.states), root, builtin.DefaultHamtBitwidth)
	if err != nil {
		return err
	}

	as.actors = actors
	as.stateRoot = root
	return nil
}

func (as *xActorStore) GetActorImpl(ctx context.Context, code cid.Cid) (rt.VMActor, error) {
	return as.registry.Lookup(code)
}
