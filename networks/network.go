package networks

import (
	"io"

	"github.com/filecoin-project/go-state-types/abi"
	"github.com/filecoin-project/go-state-types/network"
	"github.com/libp2p/go-libp2p-core/peer"

	"github.com/iand/filecoin-kernel/gas"
)

type Network interface {
	Name() string

	// Pricelist finds the latest prices for the given epoch
	Pricelist(epoch abi.ChainEpoch) gas.Pricelist

	// Version returns the network version for the given epoch
	Version(epoch abi.ChainEpoch) network.Version

	// ActorsVersion returns the version of actors adt for the given epoch
	ActorsVersion(epoch abi.ChainEpoch) int

	BootstrapPeers() []peer.AddrInfo

	// Maximum number of blocks per message
	BlockMessageLimit() int

	GenesisData() io.Reader
}
