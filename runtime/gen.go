// +build ignore

package main

import (
	"fmt"
	"os"

	gen "github.com/whyrusleeping/cbor-gen"

	"github.com/iand/filecoin-kernel/runtime"
)

func main() {
	err := gen.WriteTupleEncodersToFile("cbor_gen.go", "runtime",
		runtime.Actor{},
	)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
