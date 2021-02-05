package mainnet_test

import (
	"github.com/iand/filecoin-kernel/networks"
	"github.com/iand/filecoin-kernel/networks/mainnet"
)

var _ networks.Network = mainnet.Network
