package mainnet

import (
	"github.com/filecoin-project/go-state-types/abi"
	"github.com/filecoin-project/go-state-types/network"

	"github.com/iand/filecoin-kernel/gas"
)

var Network netwrk

type netwrk struct {
}

func (n *netwrk) PricelistByEpoch(epoch abi.ChainEpoch) gas.Pricelist {
	return PricelistByEpoch(epoch)
}

func (n *netwrk) Version(epoch abi.ChainEpoch) network.Version {
	return VersionByEpoch(epoch)
}

func (n *netwrk) ActorsVersion(epoch abi.ChainEpoch) int {
	return ActorsVersionByEpoch(epoch)
}
