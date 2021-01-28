package kernel

import (
	"context"
	"errors"
	"fmt"

	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/go-state-types/abi"
	"github.com/filecoin-project/go-state-types/big"
	"github.com/filecoin-project/go-state-types/cbor"
	"github.com/filecoin-project/go-state-types/exitcode"
	"github.com/filecoin-project/go-state-types/network"
	"github.com/filecoin-project/go-state-types/rt"
	"github.com/filecoin-project/specs-actors/v3/actors/builtin"
	init_ "github.com/filecoin-project/specs-actors/v3/actors/builtin/init"
	"github.com/filecoin-project/specs-actors/v3/actors/util/adt"
	"github.com/ipfs/go-cid"
)

type ActorMap interface {
	LoadActor(addr address.Address, out cbor.Unmarshaler) (bool, error)
	PutActor(addr address.Address, in cbor.Marshaler) error
	DeleteActor(addr address.Address) error
	Root() (cid.Cid, error)
}

var ErrActorNotFound = errors.New("actor not found")

type Store interface {
	adt.Store
	ActorMap(root cid.Cid) (ActorMap, error) // load state root as actor map
}

type VM struct {
	store        Store
	currentEpoch abi.ChainEpoch
	network      Network
	stateRoot    cid.Cid // The last committed root.
	registry     ActorRegistry
	actors       ActorMap // state tree
	actorsDirty  bool
}

func (vm *VM) GetActor(a address.Address) (*Actor, bool, error) {
	na, found := vm.NormalizeAddress(a)
	if !found {
		return nil, false, nil
	}
	var act Actor
	found, err := vm.actors.LoadActor(na, &act)
	return &act, found, err
}

// SetActor sets the the actor to the given value whether it previously existed or not.
//
// This method will not check if the actor previously existed, it will blindly overwrite it.
func (vm *VM) setActor(ctx context.Context, key address.Address, a *Actor) error {
	if err := vm.actors.PutActor(key, a); err != nil {
		return fmt.Errorf("setting actor in state tree failed: %w", err)
	}
	vm.actorsDirty = true
	return nil
}

// setActorState stores the state and updates the addressed actor
func (vm *VM) setActorState(ctx context.Context, key address.Address, state cbor.Marshaler) error {
	stateCid, err := vm.store.Put(ctx, state)
	if err != nil {
		return err
	}
	a, found, err := vm.GetActor(key)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("could not find actor %s to set state", key)
	}
	a.Head = stateCid
	return vm.setActor(ctx, key, a)
}

// deleteActor remove the actor from the storage.
//
// This method will NOT return an error if the actor was not found.
// This behaviour is based on a principle that some store implementations might not be able to determine
// whether something exists before deleting it.
func (vm *VM) deleteActor(ctx context.Context, key address.Address) error {
	err := vm.actors.DeleteActor(key)
	if err == ErrActorNotFound {
		return nil
	}
	return err
}

func (vm *VM) NetworkVersion() network.Version {
	return vm.network.Version(vm.currentEpoch)
}

func (vm *VM) NormalizeAddress(addr address.Address) (address.Address, bool) {
	// short-circuit if the address is already an ID address
	if addr.Protocol() == address.ID {
		return addr, true
	}

	// resolve the target address via the InitActor, and attempt to load state.
	initActorEntry, found, err := vm.GetActor(builtin.InitActorAddr)
	if err != nil {
		panic(fmt.Errorf("failed to load init actor: %w", err))
	}
	if !found {
		panic(fmt.Errorf("no init actor: %w", err))
	}

	// get a view into the actor state
	var state init_.State
	if err := vm.store.Get(context.TODO(), initActorEntry.Head, &state); err != nil {
		panic(err)
	}

	idAddr, found, err := state.ResolveAddress(vm.store, addr)
	if err != nil {
		panic(err)
	}
	return idAddr, found
}

func (vm *VM) checkpoint() (cid.Cid, error) {
	// commit the vm state
	root, err := vm.actors.Root()
	if err != nil {
		return cid.Undef, err
	}
	vm.stateRoot = root
	vm.actorsDirty = false

	return root, nil
}

func (vm *VM) rollback(root cid.Cid) error {
	var err error
	vm.actors, err = vm.store.ActorMap(root)
	if err != nil {
		return fmt.Errorf("failed to load node for %s: %w", root, err)
	}

	// reset the root node
	vm.stateRoot = root
	vm.actorsDirty = false
	return nil
}

type abort struct {
	code exitcode.ExitCode
	msg  string
}

func (vm *VM) Abortf(errExitCode exitcode.ExitCode, msg string, args ...interface{}) {
	panic(abort{errExitCode, fmt.Sprintf(msg, args...)})
}

// transfer debits money from one account and credits it to another.
// avoid calling this method with a zero amount else it will perform unnecessary actor loading.
//
// WARNING: this method will panic if the the amount is negative, accounts dont exist, or have inssuficient funds.
//
// Note: this is not idiomatic, it follows the Spec expectations for this method.
func (vm *VM) transfer(debitFrom address.Address, creditTo address.Address, amount abi.TokenAmount) (*Actor, *Actor) {
	// allow only for positive amounts
	if amount.LessThan(abi.NewTokenAmount(0)) {
		panic("unreachable: negative funds transfer not allowed")
	}

	ctx := context.Background()

	// retrieve debit account
	fromActor, found, err := vm.GetActor(debitFrom)
	if err != nil {
		panic(err)
	}
	if !found {
		panic(fmt.Errorf("unreachable: debit account not found. %s", err))
	}

	// check that account has enough balance for transfer
	if fromActor.Balance.LessThan(amount) {
		panic("unreachable: insufficient balance on debit account")
	}

	// debit funds
	fromActor.Balance = big.Sub(fromActor.Balance, amount)
	if err := vm.setActor(ctx, debitFrom, fromActor); err != nil {
		panic(err)
	}

	// retrieve credit account
	toActor, found, err := vm.GetActor(creditTo)
	if err != nil {
		panic(err)
	}
	if !found {
		panic(fmt.Errorf("unreachable: credit account not found. %s", err))
	}

	// credit funds
	toActor.Balance = big.Add(toActor.Balance, amount)
	if err := vm.setActor(ctx, creditTo, toActor); err != nil {
		panic(err)
	}
	return toActor, fromActor
}

func (vm *VM) getActorImpl(code cid.Cid) rt.VMActor {
	actorImpl, err := vm.registry.Lookup(code)
	if err != nil {
		vm.Abortf(exitcode.SysErrInvalidReceiver, "actor implementation not found for Exitcode %v", code)
	}
	return actorImpl
}
