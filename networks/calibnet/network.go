package calibnet

import (
	"github.com/filecoin-project/go-state-types/abi"
	"github.com/filecoin-project/go-state-types/network"
	"github.com/libp2p/go-libp2p-core/peer"

	"github.com/iand/filecoin-kernel/gas"
)

var Network = &netwrk{}

type netwrk struct {
}

func (n *netwrk) Name() string {
	return "calibrationnet"
}

func (n *netwrk) Pricelist(epoch abi.ChainEpoch) gas.Pricelist {
	return PricelistByEpoch(epoch)
}

func (n *netwrk) Version(epoch abi.ChainEpoch) network.Version {
	return VersionByEpoch(epoch)
}

func (n *netwrk) ActorsVersion(epoch abi.ChainEpoch) int {
	return ActorsVersionByEpoch(epoch)
}

func (n *netwrk) BootstrapPeers() []peer.AddrInfo {
	return BootstrapPeers
}

func (n *netwrk) BlockMessageLimit() int {
	return 10000
}
