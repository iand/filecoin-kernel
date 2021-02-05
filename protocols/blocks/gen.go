// +build ignore

package main

import (
	"fmt"
	"os"

	gen "github.com/whyrusleeping/cbor-gen"

	"github.com/iand/filecoin-kernel/protocols/blocks"
)

func main() {
	err := gen.WriteTupleEncodersToFile("cbor_gen.go", "blocks",
		blocks.BlockMessage{},
	)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
