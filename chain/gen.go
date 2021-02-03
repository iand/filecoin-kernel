// +build ignore

package main

import (
	"fmt"
	"os"

	gen "github.com/whyrusleeping/cbor-gen"

	"github.com/iand/filecoin-kernel/chain"
)

func main() {
	err := gen.WriteTupleEncodersToFile("cbor_gen.go", "chain",
		chain.BlockHeader{},
		chain.Ticket{},
		chain.ElectionProof{},
		chain.Message{},
		chain.SignedMessage{},
		chain.BeaconEntry{},
	)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
