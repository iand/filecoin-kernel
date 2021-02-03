package runtime

import (
	"context"
	"errors"
	// "fmt"

	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/go-state-types/abi"
	// "github.com/filecoin-project/go-state-types/big"
	"github.com/filecoin-project/go-state-types/cbor"
	"github.com/filecoin-project/go-state-types/exitcode"
	"github.com/filecoin-project/go-state-types/network"
	// "github.com/filecoin-project/go-state-types/rt"
	// "github.com/filecoin-project/specs-actors/v3/actors/builtin"
	// init_ "github.com/filecoin-project/specs-actors/v3/actors/builtin/init"
	// "github.com/filecoin-project/specs-actors/v3/actors/util/adt"
	"github.com/ipfs/go-cid"
	// ipldcbor "github.com/ipfs/go-ipld-cbor"

	"github.com/iand/filecoin-kernel/gas"
)

type Network interface {
	// Pricelist finds the latest prices for the given epoch
	Pricelist(epoch abi.ChainEpoch) gas.Pricelist

	// Version returns the network version for the given epoch
	Version(epoch abi.ChainEpoch) network.Version

	// ActorsVersion returns the version of actors adt for the given epoch
	ActorsVersion(epoch abi.ChainEpoch) int
}

var ErrActorNotFound = errors.New("actor not found")

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
	Params interface{}
}

type VM struct {
	store        *ActorStore
	currentEpoch abi.ChainEpoch
	network      Network

	emptyObject cid.Cid
}

func NewVM(store *ActorStore, epoch abi.ChainEpoch, network Network) (*VM, error) {
	emptyObject, err := store.states.Put(context.TODO(), []struct{}{})
	if err != nil {
		return nil, err
	}

	return &VM{
		store:        store,
		currentEpoch: epoch,
		network:      network,
		emptyObject:  emptyObject,
	}, nil
}

// ApplyMessage applies the message to the current state.
func (vm *VM) ApplyMessage(ctx context.Context, msg *Message) (cbor.Marshaler, exitcode.ExitCode) {
	// TODO: verify message values are acceptable

	// TODO: get gas pricelist from network

	// load actor from global state
	fromID, ok := vm.store.NormalizeAddress(msg.From)
	if !ok {
		return nil, exitcode.SysErrSenderInvalid
	}

	fromActor, found, err := vm.store.GetActor(ctx, fromID)
	if err != nil {
		// TODO handle error
		panic(err)
	}
	if !found {
		// Execution error; sender does not exist at time of message execution.
		return nil, exitcode.SysErrSenderInvalid
	}

	// Increment the calling actor nonce
	if err := vm.store.MutateActor(ctx, fromID, func(a *Actor) error {
		a.CallSeqNum++
		return nil
	}); err != nil {
		// TODO handle error
		panic(err)
	}

	// checkpoint state
	// Even if the message fails, the following accumulated changes will be applied:
	// - CallSeqNumber increment
	// - sender balance withheld
	priorRoot, err := vm.store.Checkpoint()
	if err != nil {
		// TODO handle error
		panic(err)
	}

	// send
	// 1. build internal message
	// 2. build invocation context
	// 3. process the msg

	topLevel := topLevelContext{
		originatorStableAddress: fromID,
		network:                 vm.network,
		currentEpoch:            vm.currentEpoch,
		// this should be nonce, but we only care that it creates a unique stable address
		// TODO: originatorCallSeq:    vm.callSequence,
		newActorAddressCount: 0,
		// TODO: statsSource:          vm.statsSource,
		// TODO: circSupply:           vm.circSupply,
	}
	// TODO: vm.callSequence++

	// build internal msg
	imsg := InternalMessage{
		from:   fromID,
		to:     msg.To,
		value:  msg.Value,
		method: msg.Method,
		params: msg.Params,
	}

	// Build invocation context and invoke
	ic := newInvocationContext(vm.store, &topLevel, imsg, fromActor, vm.emptyObject)
	ret, exitCode := ic.invoke()

	// Roll back all state if the receipt's exit code is not ok.
	// This is required in addition to rollback within the invocation context since top level messages can fail for
	// more reasons than internal ones. Invocation context still needs its own rollback so actors can recover and
	// proceed from a nested call failure.
	if exitCode != exitcode.Ok {
		if err := vm.store.Rollback(priorRoot); err != nil {
			panic(err)
		}
	}

	return ret.inner, exitCode
}
