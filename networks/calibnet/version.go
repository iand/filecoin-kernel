package calibnet

import (
	"github.com/filecoin-project/go-state-types/abi"
	"github.com/filecoin-project/go-state-types/network"
	builtin3 "github.com/filecoin-project/specs-actors/v3/actors/builtin"
)

const (
	GenesisFile = "calibnet.car"
)

const (
	UpgradeBreezeHeight      = -1
	BreezeGasTampingDuration = 120
)

const UpgradeSmokeHeight = -2

const (
	UpgradeIgnitionHeight = -3
	UpgradeRefuelHeight   = -4
)

var UpgradeActorsV2Height = abi.ChainEpoch(30)

const UpgradeTapeHeight = 60

// This signals our tentative epoch for mainnet launch. Can make it later, but not earlier.
// Miners, clients, developers, custodians all need time to prepare.
// We still have upgrades and state changes to do, but can happen after signaling timing here.
const UpgradeLiftoffHeight = -5

const UpgradeKumquatHeight = 90

const (
	UpgradeCalicoHeight  = 92000
	UpgradePersianHeight = UpgradeCalicoHeight + (builtin3.EpochsInHour * 60)
)

// 2020-12-17T19:00:00Z
const UpgradeClausHeight = 161386

// 2021-01-17T19:00:00Z
const UpgradeOrangeHeight = 250666

// 2021-01-27T07:00:00Z
const UpgradeActorsV3Height = 278026

const BlockDelaySecs = uint64(builtin3.EpochDurationSeconds)

const PropagationDelaySecs = uint64(6)

// BootstrapPeerThreshold is the minimum number peers we need to track for a sync worker to start
const BootstrapPeerThreshold = 4

func VersionByEpoch(epoch abi.ChainEpoch) network.Version {
	if epoch < UpgradeBreezeHeight {
		return network.Version0
	}

	if epoch < UpgradeSmokeHeight {
		return network.Version1
	}

	if epoch < UpgradeIgnitionHeight {
		return network.Version2
	}

	if epoch < UpgradeActorsV2Height {
		return network.Version3
	}

	if epoch < UpgradeTapeHeight {
		return network.Version4
	}

	if epoch < UpgradeKumquatHeight {
		return network.Version5
	}

	if epoch < UpgradeCalicoHeight {
		return network.Version6
	}

	if epoch < UpgradePersianHeight {
		return network.Version7
	}

	if epoch < UpgradeOrangeHeight {
		return network.Version8
	}

	return network.Version9
}

func ActorsVersionByEpoch(epoch abi.ChainEpoch) int {
	if epoch < UpgradeActorsV2Height {
		return 0
	}
	return 2
}
