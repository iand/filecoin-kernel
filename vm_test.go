package kernel

import (
	"context"
	"fmt"
	"testing"

	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/go-state-types/abi"
	"github.com/filecoin-project/go-state-types/big"
	"github.com/filecoin-project/go-state-types/cbor"
	"github.com/filecoin-project/go-state-types/exitcode"
	"github.com/filecoin-project/specs-actors/support/ipld"
	"github.com/filecoin-project/specs-actors/v3/actors/builtin"
	"github.com/filecoin-project/specs-actors/v3/actors/builtin/account"
	"github.com/filecoin-project/specs-actors/v3/actors/builtin/cron"
	"github.com/filecoin-project/specs-actors/v3/actors/builtin/exported"
	initactor "github.com/filecoin-project/specs-actors/v3/actors/builtin/init"
	"github.com/filecoin-project/specs-actors/v3/actors/builtin/market"
	"github.com/filecoin-project/specs-actors/v3/actors/builtin/power"
	"github.com/filecoin-project/specs-actors/v3/actors/builtin/reward"
	"github.com/filecoin-project/specs-actors/v3/actors/builtin/system"
	"github.com/filecoin-project/specs-actors/v3/actors/builtin/verifreg"
	"github.com/filecoin-project/specs-actors/v3/actors/runtime"
	"github.com/filecoin-project/specs-actors/v3/actors/util/adt"
	actor_testing "github.com/filecoin-project/specs-actors/v3/support/testing"
	"github.com/ipfs/go-cid"
	"github.com/stretchr/testify/require"
)

var (
	FIL          = big.NewInt(1e18)
	VerifregRoot address.Address
)

func init() {
	var err error
	VerifregRoot, err = address.NewIDAddress(80)
	if err != nil {
		panic("could not create id address 80")
	}
}

// Creates a new VM and initializes all singleton actors plus a root verifier account.
func NewVMWithSingletons(ctx context.Context, t testing.TB, store adt.Store) *VM {
	lookup := map[cid.Cid]runtime.VMActor{}
	for _, ba := range exported.BuiltinActors() {
		lookup[ba.Code()] = ba
	}

	actors, err := adt.MakeEmptyMap(store, builtin.DefaultHamtBitwidth)
	require.NoError(t, err)

	stateRoot, err := actors.Root()
	require.NoError(t, err)

	as := &ActorStore{
		states:    store,
		actors:    actors,
		stateRoot: stateRoot,
		registry:  builtinRegistry,
	}

	initializeActor(ctx, t, as, &system.State{}, builtin.SystemActorCodeID, builtin.SystemActorAddr, big.Zero())

	initState, err := initactor.ConstructState(store, "scenarios")
	require.NoError(t, err)
	initializeActor(ctx, t, as, initState, builtin.InitActorCodeID, builtin.InitActorAddr, big.Zero())

	rewardState := reward.ConstructState(abi.NewStoragePower(0))
	initializeActor(ctx, t, as, rewardState, builtin.RewardActorCodeID, builtin.RewardActorAddr, reward.StorageMiningAllocationCheck)

	cronState := cron.ConstructState(cron.BuiltInEntries())
	initializeActor(ctx, t, as, cronState, builtin.CronActorCodeID, builtin.CronActorAddr, big.Zero())

	powerState, err := power.ConstructState(store)
	require.NoError(t, err)
	initializeActor(ctx, t, as, powerState, builtin.StoragePowerActorCodeID, builtin.StoragePowerActorAddr, big.Zero())

	marketState, err := market.ConstructState(store)
	require.NoError(t, err)
	initializeActor(ctx, t, as, marketState, builtin.StorageMarketActorCodeID, builtin.StorageMarketActorAddr, big.Zero())

	// this will need to be replaced with the address of a multisig actor for the verified registry to be tested accurately
	initializeActor(ctx, t, as, &account.State{Address: VerifregRoot}, builtin.AccountActorCodeID, VerifregRoot, big.Zero())
	vrState, err := verifreg.ConstructState(store, VerifregRoot)
	require.NoError(t, err)
	initializeActor(ctx, t, as, vrState, builtin.VerifiedRegistryActorCodeID, builtin.VerifiedRegistryActorAddr, big.Zero())

	// burnt funds
	initializeActor(ctx, t, as, &account.State{Address: builtin.BurntFundsActorAddr}, builtin.AccountActorCodeID, builtin.BurntFundsActorAddr, big.Zero())

	_, err = as.Checkpoint()
	require.NoError(t, err)

	vm, err := NewVM(as, 1, nil)
	require.NoError(t, err)

	return vm
}

type addrPair struct {
	pubAddr address.Address
	idAddr  address.Address
}

// Creates n account actors in the VM with the given balance
func CreateAccounts(ctx context.Context, t testing.TB, as *ActorStore, n int, balance abi.TokenAmount, seed int64) []address.Address {
	var initState initactor.State
	err := as.LoadActorState(context.TODO(), builtin.InitActorAddr, &initState)
	require.NoError(t, err)

	addrPairs := make([]addrPair, n)
	for i := range addrPairs {
		addr := actor_testing.NewBLSAddr(t, seed+int64(i))
		idAddr, err := initState.MapAddressToNewID(adt.WrapStore(context.TODO(), as.states), addr)
		require.NoError(t, err)

		addrPairs[i] = addrPair{
			pubAddr: addr,
			idAddr:  idAddr,
		}
	}
	err = as.SetActorState(ctx, builtin.InitActorAddr, &initState)
	require.NoError(t, err)

	pubAddrs := make([]address.Address, len(addrPairs))
	for i, addrPair := range addrPairs {
		st := &account.State{Address: addrPair.pubAddr}
		initializeActor(ctx, t, as, st, builtin.AccountActorCodeID, addrPair.idAddr, balance)
		pubAddrs[i] = addrPair.pubAddr
	}
	return pubAddrs
}

func initializeActor(ctx context.Context, t testing.TB, as *ActorStore, state cbor.Marshaler, code cid.Cid, a address.Address, balance abi.TokenAmount) {
	stateCID, err := as.states.Put(ctx, state)
	require.NoError(t, err)
	actor := &Actor{
		Head:    stateCID,
		Code:    code,
		Balance: balance,
	}
	err = as.SetActor(ctx, a, actor)
	require.NoError(t, err)
}

func exitCodeStr(code exitcode.ExitCode) string {
	switch code {
	case exitcode.Ok:
		return "Ok"
	case exitcode.SysErrSenderInvalid:
		return "SysErrSenderInvalid"
	case exitcode.SysErrSenderStateInvalid:
		return "SysErrSenderStateInvalid"
	case exitcode.SysErrInvalidMethod:
		return "SysErrInvalidMethod"
	case exitcode.SysErrReserved1:
		return "SysErrReserved1"
	case exitcode.SysErrInvalidReceiver:
		return "SysErrInvalidReceiver"
	case exitcode.SysErrInsufficientFunds:
		return "SysErrInsufficientFunds"
	case exitcode.SysErrOutOfGas:
		return "SysErrOutOfGas"
	case exitcode.SysErrForbidden:
		return "SysErrForbidden"
	case exitcode.SysErrorIllegalActor:
		return "SysErrorIllegalActor"
	case exitcode.SysErrorIllegalArgument:
		return "SysErrorIllegalArgument"
	case exitcode.SysErrReserved2:
		return "SysErrReserved2"
	case exitcode.SysErrReserved3:
		return "SysErrReserved3"
	case exitcode.SysErrReserved4:
		return "SysErrReserved4"
	case exitcode.SysErrReserved5:
		return "SysErrReserved5"
	case exitcode.SysErrReserved6:
		return "SysErrReserved6"
	case exitcode.ErrIllegalArgument:
		return "ErrIllegalArgument"
	case exitcode.ErrNotFound:
		return "ErrNotFound"
	case exitcode.ErrForbidden:
		return "ErrForbidden"
	case exitcode.ErrInsufficientFunds:
		return "ErrInsufficientFunds"
	case exitcode.ErrIllegalState:
		return "ErrIllegalState"
	case exitcode.ErrSerialization:
		return "ErrSerialization"
	default:
		return code.String()
	}
}

func TestCreateMiner(t *testing.T) {
	ctx := context.Background()
	vm := NewVMWithSingletons(ctx, t, ipld.NewADTStore(ctx))
	addrs := CreateAccounts(ctx, t, vm.store, 1, big.Mul(big.NewInt(10_000), big.NewInt(1e18)), 93837778)

	msg := &Message{
		From:   addrs[0],
		To:     builtin.StoragePowerActorAddr,
		Value:  big.NewInt(1e10),
		Method: builtin.MethodsPower.CreateMiner,
		Params: &power.CreateMinerParams{
			Owner:               addrs[0],
			Worker:              addrs[0],
			WindowPoStProofType: abi.RegisteredPoStProof_StackedDrgWindow32GiBV1,
			Peer:                abi.PeerID("not really a peer id"),
		},
	}

	ret, code := vm.ApplyMessage(ctx, msg)
	require.Equal(t, exitcode.Ok, code, fmt.Sprintf("exit code %d=%s", code, exitCodeStr(code)))

	_ = ret
	// minerAddrs, ok := ret.(*power.CreateMinerReturn)
	// require.True(t, ok)

	// // all expectations implicitly expected to be Ok
	// vm.ExpectInvocation{
	// 	// Original send to storage power actor
	// 	To:     builtin.StoragePowerActorAddr,
	// 	Method: builtin.MethodsPower.CreateMiner,
	// 	Params: vm.ExpectObject(&params),
	// 	Ret:    vm.ExpectObject(ret),
	// 	SubInvocations: []vm.ExpectInvocation{{

	// 		// Storage power requests init actor construct a miner
	// 		To:     builtin.InitActorAddr,
	// 		Method: builtin.MethodsInit.Exec,
	// 		SubInvocations: []vm.ExpectInvocation{{

	// 			// Miner constructor gets params from original call
	// 			To:     minerAddrs.IDAddress,
	// 			Method: builtin.MethodConstructor,
	// 			Params: vm.ExpectObject(&miner.ConstructorParams{
	// 				OwnerAddr:           params.Owner,
	// 				WorkerAddr:          params.Worker,
	// 				WindowPoStProofType: params.WindowPoStProofType,
	// 				PeerId:              params.Peer,
	// 			}),
	// 			SubInvocations: []vm.ExpectInvocation{{
	// 				// Miner calls back to power actor to enroll its cron event
	// 				To:             builtin.StoragePowerActorAddr,
	// 				Method:         builtin.MethodsPower.EnrollCronEvent,
	// 				SubInvocations: []vm.ExpectInvocation{},
	// 			}},
	// 		}},
	// 	}},
	// }.Matches(t, v.Invocations()[0])
}

func TestOnEpochTickEnd(t *testing.T) {
	ctx := context.Background()
	vm := NewVMWithSingletons(ctx, t, ipld.NewADTStore(ctx))
	addrs := CreateAccounts(ctx, t, vm.store, 1, big.Mul(big.NewInt(10_000), big.NewInt(1e18)), 93837778)

	msg := &Message{
		From:   addrs[0],
		To:     builtin.StoragePowerActorAddr,
		Value:  big.NewInt(1e10),
		Method: builtin.MethodsPower.CreateMiner,
		Params: &power.CreateMinerParams{
			Owner: addrs[0], Worker: addrs[0],
			WindowPoStProofType: abi.RegisteredPoStProof_StackedDrgWindow32GiBV1,
			Peer:                abi.PeerID("pid"),
		},
	}

	ret, code := vm.ApplyMessage(ctx, msg)
	require.Equal(t, exitcode.Ok, code, fmt.Sprintf("exit code %d=%s", code, exitCodeStr(code)))

	createRet, ok := ret.(*power.CreateMinerReturn)
	require.True(t, ok)

	_ = createRet
	/*
		// find epoch of miner's next cron task (4 levels deep, first message each level)
		cronParams := vm.ParamsForInvocation(t, v, 0, 0, 0, 0)
		cronConfig, ok := cronParams.(*power.EnrollCronEventParams)
		require.True(t, ok)

		// create new vm at epoch 1 less than epoch requested by miner
		v, err := v.WithEpoch(cronConfig.EventEpoch - 1)
		require.NoError(t, err)

		// run cron and expect a call to miner and a call to update reward actor parameters
		vm.ApplyOk(t, v, builtin.CronActorAddr, builtin.StoragePowerActorAddr, big.Zero(), builtin.MethodsPower.OnEpochTickEnd, abi.Empty)

		// expect miner call to be missing
		vm.ExpectInvocation{
			// Original send to storage power actor
			To:     builtin.StoragePowerActorAddr,
			Method: builtin.MethodsPower.OnEpochTickEnd,
			SubInvocations: []vm.ExpectInvocation{{
				// expect call to reward to update kpi
				To:     builtin.RewardActorAddr,
				Method: builtin.MethodsReward.UpdateNetworkKPI,
				From:   builtin.StoragePowerActorAddr,
			}},
		}.Matches(t, v.Invocations()[0])

		// create new vm at cron epoch with existing state
		v, err = v.WithEpoch(cronConfig.EventEpoch)
		require.NoError(t, err)

		// run cron and expect a call to miner and a call to update reward actor parameters
		vm.ApplyOk(t, v, builtin.CronActorAddr, builtin.StoragePowerActorAddr, big.Zero(), builtin.MethodsPower.OnEpochTickEnd, abi.Empty)

		// expect call to miner
		vm.ExpectInvocation{
			// Original send to storage power actor
			To:     builtin.StoragePowerActorAddr,
			Method: builtin.MethodsPower.OnEpochTickEnd,
			SubInvocations: []vm.ExpectInvocation{{

				// expect call back to miner that was set up in create miner
				To:     minerAddrs.IDAddress,
				Method: builtin.MethodsMiner.OnDeferredCronEvent,
				From:   builtin.StoragePowerActorAddr,
				Value:  vm.ExpectAttoFil(big.Zero()),
				Params: vm.ExpectBytes(cronConfig.Payload),
			}, {

				// expect call to reward to update kpi
				To:     builtin.RewardActorAddr,
				Method: builtin.MethodsReward.UpdateNetworkKPI,
				From:   builtin.StoragePowerActorAddr,
			}},
		}.Matches(t, v.Invocations()[0])
	*/
}
