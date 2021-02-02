package kernel

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"reflect"
	"runtime/debug"

	"github.com/filecoin-project/go-state-types/cbor"

	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/go-state-types/abi"
	"github.com/filecoin-project/go-state-types/big"
	"github.com/filecoin-project/go-state-types/crypto"
	"github.com/filecoin-project/go-state-types/exitcode"
	"github.com/filecoin-project/go-state-types/network"
	"github.com/filecoin-project/go-state-types/rt"
	"github.com/ipfs/go-cid"
	"github.com/pkg/errors"

	"github.com/filecoin-project/specs-actors/v3/actors/builtin"
	init_ "github.com/filecoin-project/specs-actors/v3/actors/builtin/init"
	"github.com/filecoin-project/specs-actors/v3/actors/runtime"
	"github.com/filecoin-project/specs-actors/v3/actors/runtime/proof"
	"github.com/filecoin-project/specs-actors/v3/actors/util/adt"
	"github.com/filecoin-project/specs-actors/v3/support/testing"
)

func IsSingletonActor(code cid.Cid) bool {
	// TODO: implement
	return false
}

func IsBuiltinActor(code cid.Cid) bool {
	// TODO: implement
	return false
}

type InternalMessage struct {
	from   address.Address
	to     address.Address
	value  abi.TokenAmount
	method abi.MethodNum
	params interface{}
}

var _ runtime.Message = (*InternalMessage)(nil)

// ValueReceived implements runtime.MessageInfo.
func (msg InternalMessage) ValueReceived() abi.TokenAmount {
	return msg.value
}

// Caller implements runtime.MessageInfo.
func (msg InternalMessage) Caller() address.Address {
	return msg.from
}

// Receiver implements runtime.MessageInfo.
func (msg InternalMessage) Receiver() address.Address {
	return msg.to
}

type abort struct {
	code exitcode.ExitCode
	msg  string
}

func (vm *VM) Abortf(errExitCode exitcode.ExitCode, msg string, args ...interface{}) {
	panic(abort{errExitCode, fmt.Sprintf(msg, args...)})
}

var EmptyObjectCid cid.Cid

// Context for an individual message invocation, including inter-actor sends.
type invocationContext struct {
	as                *xActorStore
	topLevel          *topLevelContext
	msg               InternalMessage // The message being processed
	fromActor         *Actor          // The immediate calling actor
	toActor           *Actor          // The actor to which message is addressed
	emptyObject       cid.Cid
	isCallerValidated bool
	allowSideEffects  bool
	callerValidated   bool
}

// Context for a top-level invocation sequence
type topLevelContext struct {
	originatorStableAddress address.Address // Stable (public key) address of the top-level message sender.
	originatorCallSeq       uint64          // Call sequence number of the top-level message.
	newActorAddressCount    uint64          // Count of calls to NewActorAddress (mutable).
	network                 Network
	currentEpoch            abi.ChainEpoch
}

func newInvocationContext(as *xActorStore, topLevel *topLevelContext, msg InternalMessage, fromActor *Actor, emptyObject cid.Cid) invocationContext {
	// Note: the toActor and stateHandle are loaded during the `invoke()`
	return invocationContext{
		as:                as,
		topLevel:          topLevel,
		msg:               msg,
		fromActor:         fromActor,
		emptyObject:       emptyObject,
		isCallerValidated: false,
		allowSideEffects:  true,
		toActor:           nil,
	}
}

var _ runtime.StateHandle = (*invocationContext)(nil)

// runtime aborts are trapped by invoke, it will always return an exit code.
func (ic *invocationContext) invoke() (ret returnWrapper, errcode exitcode.ExitCode) {
	// Checkpoint state, for restoration on rollback
	// Note that changes prior to invocation (sequence number bump and gas prepayment) persist even if invocation fails.
	priorRoot, err := ic.as.Checkpoint()
	if err != nil {
		panic(err)
	}

	// Install handler for abort, which rolls back all state changes from this and any nested invocations.
	// This is the only path by which a non-OK exit code may be returned.
	defer func() {
		if r := recover(); r != nil {
			if err := ic.as.Rollback(priorRoot); err != nil {
				panic(err)
			}
			switch r := r.(type) {
			case abort:
				ret = returnWrapper{abi.Empty} // The Empty here should never be used, but slightly safer than zero value.
				errcode = r.code
				return
			default:
				// do not trap unknown panics
				debug.PrintStack()
				panic(r)
			}
		}
	}()

	// pre-dispatch
	// 1. load target actor
	// 2. transfer optional funds
	// 3. short-circuit _Send_ method
	// 4. load target actor code
	// 5. create target state handle
	// assert from address is an ID address.
	if ic.msg.from.Protocol() != address.ID {
		panic("bad Exitcode: sender address MUST be an ID address at invocation time")
	}

	// 2. load target actor
	// Note: we replace the "to" address with the normalized version
	ic.toActor, ic.msg.to = ic.resolveTarget(ic.msg.to)

	// 3. transfer funds carried by the msg
	if !ic.msg.value.NilOrZero() {
		if ic.msg.value.LessThan(big.Zero()) {
			ic.Abortf(exitcode.SysErrForbidden, "attempt to transfer negative value %s from %s to %s",
				ic.msg.value, ic.msg.from, ic.msg.to)
		}
		if ic.fromActor.Balance.LessThan(ic.msg.value) {
			ic.Abortf(exitcode.SysErrInsufficientFunds, "sender %s insufficient balance %s to transfer %s to %s",
				ic.msg.from, ic.fromActor.Balance, ic.msg.value, ic.msg.to)
		}
		ic.toActor, ic.fromActor = ic.transfer(ic.msg.from, ic.msg.to, ic.msg.value)
	}

	// 4. if we are just sending funds, there is nothing else to do.
	if ic.msg.method == builtin.MethodSend {
		return returnWrapper{abi.Empty}, exitcode.Ok
	}

	// 5. load target actor code
	actorImpl, err := ic.as.GetActorImpl(context.TODO(), ic.toActor.Code)
	if err != nil {
		ic.Abortf(exitcode.SysErrInvalidMethod, "could not find actor implementation")
	}

	// dispatch
	out, err := ic.dispatch(actorImpl, ic.msg.method, ic.msg.params)
	if err != nil {
		ic.Abortf(exitcode.SysErrInvalidMethod, "could not dispatch method")
	}

	// assert output implements expected interface
	var marsh cbor.Marshaler = abi.Empty
	if out != nil {
		var ok bool
		marsh, ok = out.(cbor.Marshaler)
		if !ok {
			ic.Abortf(exitcode.SysErrorIllegalActor, "Returned value is not a CBORMarshaler")
		}
	}
	ret = returnWrapper{inner: marsh}

	// 3. success!
	return ret, exitcode.Ok
}

func (ic *invocationContext) dispatch(actor rt.VMActor, method abi.MethodNum, arg interface{}) (interface{}, error) {
	// get method signature
	exports := actor.Exports()

	// get method entry
	methodIdx := (uint64)(method)
	if len(exports) < (int)(methodIdx) {
		return nil, fmt.Errorf("method undefined. method: %d, Exitcode: %s", method, actor.Code())
	}
	entry := exports[methodIdx]
	if entry == nil {
		return nil, fmt.Errorf("method undefined. method: %d, Exitcode: %s", method, actor.Code())
	}

	ventry := reflect.ValueOf(entry)

	// build args to pass to the method
	args := []reflect.Value{
		// the ctx will be automatically coerced
		reflect.ValueOf(ic),
	}

	t := ventry.Type().In(1)
	if arg == nil {
		args = append(args, reflect.New(t).Elem())
	} else if raw, ok := arg.([]byte); ok {
		obj, err := decodeBytes(t, raw)
		if err != nil {
			return nil, err
		}
		args = append(args, reflect.ValueOf(obj))
	} else if raw, ok := arg.(builtin.CBORBytes); ok {
		obj, err := decodeBytes(t, raw)
		if err != nil {
			return nil, err
		}
		args = append(args, reflect.ValueOf(obj))
	} else {
		args = append(args, reflect.ValueOf(arg))
	}

	// invoke the method
	out := ventry.Call(args)

	// Note: we only support single objects being returned
	if len(out) > 1 {
		return nil, fmt.Errorf("actor method returned more than one object. method: %d, Exitcode: %s", method, actor.Code())
	}

	// method returns unit
	// Note: we need to check for `IsNill()` here because Go doesnt work if you do `== nil` on the interface
	if len(out) == 0 || (out[0].Kind() != reflect.Struct && out[0].IsNil()) {
		return nil, nil
	}

	// forward return
	return out[0].Interface(), nil
}

// Note: this is not idiomatic, it follows the Spec expectations for this method.
func (ic *invocationContext) transfer(debitFrom address.Address, creditTo address.Address, amount abi.TokenAmount) (*Actor, *Actor) {
	// allow only for positive amounts
	if amount.LessThan(abi.NewTokenAmount(0)) {
		panic("unreachable: negative funds transfer not allowed")
	}

	ctx := context.Background()

	// retrieve debit account
	fromActor, found, err := ic.as.GetActor(ctx, debitFrom)
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
	if err := ic.as.SetActor(ctx, debitFrom, fromActor); err != nil {
		panic(err)
	}

	// retrieve credit account
	toActor, found, err := ic.as.GetActor(ctx, creditTo)
	if err != nil {
		panic(err)
	}
	if !found {
		panic(fmt.Errorf("unreachable: credit account not found. %s", err))
	}

	// credit funds
	toActor.Balance = big.Add(toActor.Balance, amount)
	if err := ic.as.SetActor(ctx, creditTo, toActor); err != nil {
		panic(err)
	}
	return toActor, fromActor
}

// func (ic *invocationContext) loadState(obj cbor.Unmarshaler) cid.Cid {
// 	// The actor must be loaded from store every time since the state may have changed via a different state handle
// 	// (e.g. in a recursive call).
// 	actr := ic.loadActor()
// 	c := actr.Head
// 	if !c.Defined() {
// 		ic.Abortf(exitcode.SysErrorIllegalActor, "failed to load undefined state, must construct first")
// 	}
// 	err := ic.vm.store.Get(context.TODO(), c, obj)
// 	if err != nil {
// 		panic(errors.Wrapf(err, "failed to load state for actor %s, CID %s", ic.msg.to, c))
// 	}
// 	return c
// }

// func (ic *invocationContext) loadActor() *Actor {
// 	actr, found, err := ic.vm.GetActor(ic.msg.to)
// 	if err != nil {
// 		panic(err)
// 	}
// 	if !found {
// 		panic(fmt.Errorf("failed to find actor %s for state", ic.msg.to))
// 	}
// 	return actr
// }

// func (ic *invocationContext) storeActor(actr *Actor) {
// 	err := ic.vm.setActor(context.TODO(), ic.msg.to, actr)
// 	if err != nil {
// 		panic(err)
// 	}
// }

/////////////////////////////////////////////
//          Runtime methods
/////////////////////////////////////////////

var _ runtime.Runtime = (*invocationContext)(nil)

// Store implements runtime.Runtime.
func (ic *invocationContext) StoreGet(c cid.Cid, o cbor.Unmarshaler) bool {
	sw := &storeWrapper{s: adt.WrapStore(context.TODO(), ic.as.states), rt: ic}
	return sw.StoreGet(c, o)
}

func (ic *invocationContext) StorePut(x cbor.Marshaler) cid.Cid {
	sw := &storeWrapper{s: adt.WrapStore(context.TODO(), ic.as.states), rt: ic}
	return sw.StorePut(x)
}

// These methods implement
// ValueReceived implements runtime.Message
func (ic *invocationContext) ValueReceived() abi.TokenAmount {
	return ic.msg.ValueReceived()
}

// Caller implements runtime.Message
func (ic *invocationContext) Caller() address.Address {
	return ic.msg.Caller()
}

// Receiver implements runtime.Message
func (ic *invocationContext) Receiver() address.Address {
	return ic.msg.Receiver()
}

func (ic *invocationContext) StateCreate(obj cbor.Marshaler) {
	actr := ic.loadActor()
	if actr.Head.Defined() && !ic.emptyObject.Equals(actr.Head) {
		ic.Abortf(exitcode.SysErrorIllegalActor, "failed to construct actor state: already initialized")
	}
	c, err := ic.as.states.Put(context.TODO(), obj)
	if err != nil {
		ic.Abortf(exitcode.ErrIllegalState, "failed to create actor state")
	}
	actr.Head = c
	ic.storeActor(actr)
}

// Readonly is the implementation of the ActorStateHandle interface.
func (ic *invocationContext) StateReadonly(obj cbor.Unmarshaler) {
	// Load state to obj.
	ic.loadState(obj)
}

// Transaction is the implementation of the ActorStateHandle interface.
func (ic *invocationContext) StateTransaction(obj cbor.Er, f func()) {
	if obj == nil {
		ic.Abortf(exitcode.SysErrorIllegalActor, "Must not pass nil to Transaction()")
	}

	// Load state to obj.
	ic.loadState(obj)

	// Call user code allowing mutation but not side-effects
	ic.allowSideEffects = false
	f()
	ic.allowSideEffects = true

	ic.replace(obj)
}

func (ic *invocationContext) VerifySignature(signature crypto.Signature, signer address.Address, plaintext []byte) error {
	return ic.Syscalls().VerifySignature(signature, signer, plaintext)
}

func (ic *invocationContext) HashBlake2b(data []byte) [32]byte {
	return ic.Syscalls().HashBlake2b(data)
}

func (ic *invocationContext) ComputeUnsealedSectorCID(reg abi.RegisteredSealProof, pieces []abi.PieceInfo) (cid.Cid, error) {
	return ic.Syscalls().ComputeUnsealedSectorCID(reg, pieces)
}

func (ic *invocationContext) VerifySeal(vi proof.SealVerifyInfo) error {
	return ic.Syscalls().VerifySeal(vi)
}

func (ic *invocationContext) BatchVerifySeals(vis map[address.Address][]proof.SealVerifyInfo) (map[address.Address][]bool, error) {
	return ic.Syscalls().BatchVerifySeals(vis)
}

func (ic *invocationContext) VerifyPoSt(vi proof.WindowPoStVerifyInfo) error {
	return ic.Syscalls().VerifyPoSt(vi)
}

func (ic *invocationContext) VerifyConsensusFault(h1, h2, extra []byte) (*runtime.ConsensusFault, error) {
	return ic.Syscalls().VerifyConsensusFault(h1, h2, extra)
}

func (ic *invocationContext) NetworkVersion() network.Version {
	return ic.topLevel.network.Version(ic.topLevel.currentEpoch)
}

func (ic *invocationContext) CurrEpoch() abi.ChainEpoch {
	return ic.topLevel.currentEpoch
}

func (ic *invocationContext) CurrentBalance() abi.TokenAmount {
	return ic.toActor.Balance
}

func (ic *invocationContext) GetActorCodeCID(a address.Address) (cid.Cid, bool) {
	entry, found, err := ic.as.GetActor(context.TODO(), a)
	if !found {
		return cid.Undef, false
	}
	if err != nil {
		panic(err)
	}
	return entry.Code, true
}

func (ic *invocationContext) GetRandomnessFromBeacon(_ crypto.DomainSeparationTag, _ abi.ChainEpoch, _ []byte) abi.Randomness {
	// TODO: implement GetRandomnessFromBeacon
	return []byte("not really random")
}

func (ic *invocationContext) GetRandomnessFromTickets(_ crypto.DomainSeparationTag, _ abi.ChainEpoch, _ []byte) abi.Randomness {
	// TODO: implement GetRandomnessFromTickets
	return []byte("not really random")
}

func (ic *invocationContext) ValidateImmediateCallerAcceptAny() {
	ic.assertf(!ic.callerValidated, "caller has been double validated")
	ic.callerValidated = true
}

func (ic *invocationContext) ValidateImmediateCallerIs(addrs ...address.Address) {
	ic.assertf(!ic.callerValidated, "caller has been double validated")
	ic.callerValidated = true
	for _, addr := range addrs {
		if ic.msg.from == addr {
			return
		}
	}
	ic.Abortf(exitcode.ErrForbidden, "caller address %v forbidden, allowed: %v", ic.msg.from, addrs)
}

func (ic *invocationContext) ValidateImmediateCallerType(types ...cid.Cid) {
	ic.assertf(!ic.callerValidated, "caller has been double validated")
	ic.callerValidated = true
	for _, t := range types {
		if t.Equals(ic.fromActor.Code) {
			return
		}
	}
	ic.Abortf(exitcode.ErrForbidden, "caller type %v forbidden, allowed: %v", ic.fromActor.Code, types)
}

func (ic *invocationContext) Abortf(errExitCode exitcode.ExitCode, msg string, args ...interface{}) {
	panic(abort{errExitCode, fmt.Sprintf(msg, args...)})
}

func (ic *invocationContext) assertf(condition bool, msg string, args ...interface{}) {
	if !condition {
		panic(fmt.Errorf(msg, args...))
	}
}

func (ic *invocationContext) ResolveAddress(address address.Address) (address.Address, bool) {
	return ic.as.NormalizeAddress(address)
}

func (ic *invocationContext) NewActorAddress() address.Address {
	var buf bytes.Buffer

	b1, err := ic.topLevel.originatorStableAddress.Marshal()
	if err != nil {
		panic(err)
	}
	_, err = buf.Write(b1)
	if err != nil {
		panic(err)
	}

	err = binary.Write(&buf, binary.BigEndian, ic.topLevel.originatorCallSeq)
	if err != nil {
		panic(err)
	}

	err = binary.Write(&buf, binary.BigEndian, ic.topLevel.newActorAddressCount)
	if err != nil {
		panic(err)
	}

	actorAddress, err := address.NewActorAddress(buf.Bytes())
	if err != nil {
		panic(err)
	}
	return actorAddress
}

// Send implements runtime.InvocationContext.
func (ic *invocationContext) Send(toAddr address.Address, methodNum abi.MethodNum, params cbor.Marshaler, value abi.TokenAmount, out cbor.Er) (errcode exitcode.ExitCode) {
	// check if side-effects are allowed
	if !ic.allowSideEffects {
		ic.Abortf(exitcode.SysErrorIllegalActor, "Calling Send() is not allowed during side-effect lock")
	}
	from := ic.msg.to
	fromActor := ic.toActor
	newMsg := InternalMessage{
		from:   from,
		to:     toAddr,
		value:  value,
		method: methodNum,
		params: params,
	}

	newCtx := newInvocationContext(ic.as, ic.topLevel, newMsg, fromActor, ic.emptyObject)
	ret, code := newCtx.invoke()
	err := ret.Into(out)
	if err != nil {
		ic.Abortf(exitcode.ErrSerialization, "failed to serialize send return value into output parameter")
	}
	return code
}

// CreateActor implements runtime.ExtendedInvocationContext.
func (ic *invocationContext) CreateActor(codeID cid.Cid, addr address.Address) {
	if !IsBuiltinActor(codeID) {
		ic.Abortf(exitcode.SysErrorIllegalArgument, "Can only create built-in actors.")
	}

	if IsSingletonActor(codeID) {
		ic.Abortf(exitcode.SysErrorIllegalArgument, "Can only have one instance of singleton actors.")
	}

	// Check existing address. If nothing there, create empty actor.
	//
	// Note: we are storing the actors by ActorID *address*
	_, found, err := ic.as.GetActor(context.TODO(), addr)
	if err != nil {
		panic(err)
	}
	if found {
		ic.Abortf(exitcode.SysErrorIllegalArgument, "Actor address already exists")
	}

	newActor := &Actor{
		Head:    ic.emptyObject,
		Code:    codeID,
		Balance: abi.NewTokenAmount(0),
	}
	if err := ic.as.SetActor(context.TODO(), addr, newActor); err != nil {
		panic(err)
	}
}

// deleteActor implements runtime.ExtendedInvocationContext.
func (ic *invocationContext) DeleteActor(beneficiary address.Address) {
	receiver := ic.msg.to
	receiverActor, found, err := ic.as.GetActor(context.TODO(), receiver)
	if err != nil {
		panic(err)
	}
	if !found {
		ic.Abortf(exitcode.SysErrorIllegalActor, "delete non-existent actor %v", receiverActor)
	}

	// Transfer any remaining balance to the beneficiary.
	// This looks like it could cause a problem with gas refund going to a non-existent actor, but the gas payer
	// is always an account actor, which cannot be the receiver of this message.
	if receiverActor.Balance.GreaterThan(big.Zero()) {
		ic.transfer(receiver, beneficiary, receiverActor.Balance)
	}

	if err := ic.as.DeleteActor(context.TODO(), receiver); err != nil {
		panic(err)
	}
}

func (ic *invocationContext) TotalFilCircSupply() abi.TokenAmount {
	return big.Mul(big.NewInt(1e9), big.NewInt(1e18))
}

func (ic *invocationContext) Context() context.Context {
	return context.TODO()
}

func (ic *invocationContext) ChargeGas(_ string, _ int64, _ int64) {
	// no-op
}

// Starts a new tracing span. The span must be End()ed explicitly, typically with a deferred invocation.
func (ic *invocationContext) StartSpan(_ string) func() {
	return fakeTraceSpanEnd
}

// Provides the system call interface.
func (ic *invocationContext) Syscalls() runtime.Syscalls {
	return fakeSyscalls{receiver: ic.msg.to, epoch: ic.topLevel.currentEpoch}
}

type returnWrapper struct {
	inner cbor.Marshaler
}

func (r returnWrapper) Into(o cbor.Unmarshaler) error {
	if r.inner == nil {
		return fmt.Errorf("failed to unmarshal nil return (did you mean abi.Empty?)")
	}
	b := bytes.Buffer{}
	if err := r.inner.MarshalCBOR(&b); err != nil {
		return err
	}
	return o.UnmarshalCBOR(&b)
}

/////////////////////////////////////////////
//          Fake syscalls
/////////////////////////////////////////////

type fakeSyscalls struct {
	receiver address.Address
	epoch    abi.ChainEpoch
}

func (s fakeSyscalls) VerifySignature(_ crypto.Signature, _ address.Address, _ []byte) error {
	return nil
}

func (s fakeSyscalls) HashBlake2b(_ []byte) [32]byte {
	return [32]byte{}
}

func (s fakeSyscalls) ComputeUnsealedSectorCID(_ abi.RegisteredSealProof, _ []abi.PieceInfo) (cid.Cid, error) {
	return testing.MakeCID("presealedSectorCID", nil), nil
}

func (s fakeSyscalls) VerifySeal(_ proof.SealVerifyInfo) error {
	return nil
}

func (s fakeSyscalls) BatchVerifySeals(vi map[address.Address][]proof.SealVerifyInfo) (map[address.Address][]bool, error) {
	res := map[address.Address][]bool{}
	for addr, infos := range vi { //nolint:nomaprange
		verified := make([]bool, len(infos))
		for i := range infos {
			// everyone wins
			verified[i] = true
		}
		res[addr] = verified
	}
	return res, nil
}

func (s fakeSyscalls) VerifyPoSt(_ proof.WindowPoStVerifyInfo) error {
	return nil
}

func (s fakeSyscalls) VerifyConsensusFault(_, _, _ []byte) (*runtime.ConsensusFault, error) {
	return &runtime.ConsensusFault{
		Target: s.receiver,
		Epoch:  s.epoch - 1,
		Type:   runtime.ConsensusFaultDoubleForkMining,
	}, nil
}

/////////////////////////////////////////////
//          Fake trace span
/////////////////////////////////////////////

func fakeTraceSpanEnd() {
}

/////////////////////////////////////////////
//          storeWrapper
/////////////////////////////////////////////

type aborter interface {
	Abortf(errExitCode exitcode.ExitCode, msg string, args ...interface{})
}

type storeWrapper struct {
	s  adt.Store
	rt aborter
}

func (s storeWrapper) StoreGet(c cid.Cid, o cbor.Unmarshaler) bool {
	err := s.s.Get(context.TODO(), c, o)
	// assume all errors are not found errors (bad assumption, but ok for testing)
	return err == nil
}

func (s storeWrapper) StorePut(x cbor.Marshaler) cid.Cid {
	c, err := s.s.Put(context.TODO(), x)
	if err != nil {
		s.rt.Abortf(exitcode.ErrIllegalState, "could not put object in store")
	}
	return c
}

// resolveTarget loads an actor and returns its ActorID address.
//
// If the target actor does not exist, and the target address is a pub-key address,
// a new account actor will be created.
// Otherwise, this method will abort execution.
func (ic *invocationContext) resolveTarget(target address.Address) (*Actor, address.Address) {
	// resolve the target address via the InitActor, and attempt to load state.
	initActorEntry, found, err := ic.as.GetActor(context.TODO(), builtin.InitActorAddr)
	if err != nil {
		panic(err)
	}
	if !found {
		ic.Abortf(exitcode.SysErrSenderInvalid, "init actor not found")
	}

	if target == builtin.InitActorAddr {
		return initActorEntry, target
	}

	// get a view into the actor state
	var state init_.State
	if err := ic.as.states.Get(context.TODO(), initActorEntry.Head, &state); err != nil {
		panic(err)
	}

	// lookup the ActorID based on the address
	targetIDAddr, found, err := state.ResolveAddress(adt.WrapStore(context.TODO(), ic.as.states), target)
	created := false
	if err != nil {
		panic(err)
	} else if !found {
		if target.Protocol() != address.SECP256K1 && target.Protocol() != address.BLS {
			// Don't implicitly create an account actor for an address without an associated key.
			ic.Abortf(exitcode.SysErrInvalidReceiver, "cannot create account for address type")
		}

		targetIDAddr, err = state.MapAddressToNewID(adt.WrapStore(context.TODO(), ic.as.states), target)
		if err != nil {
			panic(err)
		}
		// store new state
		initHead, err := ic.as.states.Put(context.TODO(), &state)
		if err != nil {
			panic(err)
		}

		// update init actor
		initActorEntry.Head = initHead
		if err := ic.as.SetActor(context.TODO(), builtin.InitActorAddr, initActorEntry); err != nil {
			panic(err)
		}

		ic.CreateActor(builtin.AccountActorCodeID, targetIDAddr)

		// call constructor on account
		newMsg := InternalMessage{
			from:   builtin.SystemActorAddr,
			to:     targetIDAddr,
			value:  big.Zero(),
			method: builtin.MethodsAccount.Constructor,
			// use original address as constructor params
			// Note: constructor takes a pointer
			params: &target,
		}

		newCtx := newInvocationContext(ic.as, ic.topLevel, newMsg, nil, ic.emptyObject)
		_, code := newCtx.invoke()
		if code.IsError() {
			// we failed to construct an account actor..
			ic.Abortf(code, "failed to construct account actor")
		}

		created = true
	}

	// load actor
	targetActor, found, err := ic.as.GetActor(context.TODO(), targetIDAddr)
	if err != nil {
		panic(err)
	}
	if !found && created {
		panic(fmt.Errorf("unreachable: actor is supposed to exist but it does not. addr: %s, idAddr: %s", target, targetIDAddr))
	}
	if !found {
		ic.Abortf(exitcode.SysErrInvalidReceiver, "actor at address %s registered but not found", targetIDAddr.String())
	}

	return targetActor, targetIDAddr
}

func (ic *invocationContext) replace(obj cbor.Marshaler) cid.Cid {
	actr, found, err := ic.as.GetActor(context.TODO(), ic.msg.to)
	if err != nil {
		panic(err)
	}
	if !found {
		ic.Abortf(exitcode.ErrIllegalState, "failed to find actor %s for state", ic.msg.to)
	}
	c, err := ic.as.states.Put(context.TODO(), obj)
	if err != nil {
		ic.Abortf(exitcode.ErrIllegalState, "could not save new state")
	}
	actr.Head = c
	err = ic.as.SetActor(context.TODO(), ic.msg.to, actr)
	if err != nil {
		ic.Abortf(exitcode.ErrIllegalState, "could not save actor %s", ic.msg.to)
	}
	return c
}

func (ic *invocationContext) loadState(obj cbor.Unmarshaler) cid.Cid {
	// The actor must be loaded from store every time since the state may have changed via a different state handle
	// (e.g. in a recursive call).
	actr := ic.loadActor()
	c := actr.Head
	if !c.Defined() {
		ic.Abortf(exitcode.SysErrorIllegalActor, "failed to load undefined state, must construct first")
	}
	err := ic.as.states.Get(context.TODO(), c, obj)
	if err != nil {
		panic(errors.Wrapf(err, "failed to load state for actor %s, CID %s", ic.msg.to, c))
	}
	return c
}

// loadActor loads the actor the message is being sent to
func (ic *invocationContext) loadActor() *Actor {
	actr, found, err := ic.as.GetActor(context.TODO(), ic.msg.to)
	if err != nil {
		panic(err)
	}
	if !found {
		panic(fmt.Errorf("failed to find actor %s for state", ic.msg.to))
	}
	return actr
}

// storeActor stores the actor the message is being sent to
func (ic *invocationContext) storeActor(actr *Actor) {
	err := ic.as.SetActor(context.TODO(), ic.msg.to, actr)
	if err != nil {
		panic(err)
	}
}

func decodeBytes(t reflect.Type, argBytes []byte) (interface{}, error) {
	// decode arg1 (this is the payload for the actor method)
	v := reflect.New(t)

	// This would be better fixed in then encoding library.
	obj := v.Elem().Interface()
	if _, ok := obj.(cbor.Unmarshaler); !ok {
		return nil, errors.New("method argument cannot be decoded")
	}

	buf := bytes.NewBuffer(argBytes)
	auxv := reflect.New(t.Elem())
	obj = auxv.Interface()

	unmarsh := obj.(cbor.Unmarshaler)
	if err := unmarsh.UnmarshalCBOR(buf); err != nil {
		return nil, err
	}
	return unmarsh, nil
}

func (ic *invocationContext) Log(level rt.LogLevel, msg string, args ...interface{}) {
}
