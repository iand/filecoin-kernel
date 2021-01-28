package kernel

import (
	"github.com/filecoin-project/go-state-types/abi"
	"github.com/filecoin-project/go-state-types/network"

	"github.com/iand/filecoin-kernel/gas"
	"github.com/iand/filecoin-kernel/params/mainnet"
)

type Network interface {
	// Pricelist finds the latest prices for the given epoch
	Pricelist(epoch abi.ChainEpoch) gas.Pricelist

	// Version returns the network version for the given epoch
	Version(epoch abi.ChainEpoch) network.Version

	// ActorsVersion returns the version of actors adt for the given epoch
	ActorsVersion(epoch abi.ChainEpoch) int
}

var Mainnet mainnetNetwork

type mainnetNetwork struct {
}

func (n *mainnetNetwork) PricelistByEpoch(epoch abi.ChainEpoch) gas.Pricelist {
	return mainnet.PricelistByEpoch(epoch)
}

func (n *mainnetNetwork) Version(epoch abi.ChainEpoch) network.Version {
	return mainnet.VersionByEpoch(epoch)
}

func (n *mainnetNetwork) ActorsVersion(epoch abi.ChainEpoch) int {
	return mainnet.ActorsVersionByEpoch(epoch)
}
