package gas

import (
	"github.com/filecoin-project/go-state-types/abi"
	"github.com/filecoin-project/go-state-types/crypto"
	proof2 "github.com/filecoin-project/specs-actors/v3/actors/runtime/proof"
)

type Charge interface {
	Total() int64
}

// Pricelist provides prices for operations in the VM.
type Pricelist interface {
	// OnChainMessage returns the gas used for storing a message of a given size in the chain.
	OnChainMessage(msgSize int) Charge
	// OnChainReturnValue returns the gas used for storing the response of a message in the chain.
	OnChainReturnValue(dataSize int) Charge

	// OnMethodInvocation returns the gas used when invoking a method.
	OnMethodInvocation(value abi.TokenAmount, methodNum abi.MethodNum) Charge

	// OnIpldGet returns the gas used for storing an object
	OnIpldGet() Charge
	// OnIpldPut returns the gas used for storing an object
	OnIpldPut(dataSize int) Charge

	// OnCreateActor returns the gas used for creating an actor
	OnCreateActor() Charge
	// OnDeleteActor returns the gas used for deleting an actor
	OnDeleteActor() Charge

	OnVerifySignature(sigType crypto.SigType, plainTextSize int) (Charge, error)
	OnHashing(dataSize int) Charge
	OnComputeUnsealedSectorCid(proofType abi.RegisteredSealProof, pieces []abi.PieceInfo) Charge
	OnVerifySeal(info proof2.SealVerifyInfo) Charge
	OnVerifyPost(info proof2.WindowPoStVerifyInfo) Charge
	OnVerifyConsensusFault() Charge
}
