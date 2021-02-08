package calibnet

import (
	"fmt"

	rice "github.com/GeertJohan/go.rice"
)

var genesisBytes []byte

func init() {
	box, err := rice.FindBox("genesis")
	if err != nil {
		panic(fmt.Sprintf("failed to find mainnet genesis data directory: %v", err))
	}
	genesisBytes, err = box.Bytes("calibnet.car")
	if err != nil {
		panic(fmt.Sprintf("failed to read mainnet genesis data: %v", err))
	}
}
