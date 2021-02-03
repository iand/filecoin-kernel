// +build ignore

package main

import (
	"fmt"
	"os"

	gen "github.com/whyrusleeping/cbor-gen"

	"github.com/iand/filecoin-kernel/chainexchange"
)

func main() {
	err := gen.WriteTupleEncodersToFile("cbor_gen.go", "chainexchange",
		chainexchange.BSTipSet{},
		chainexchange.CompactedMessages{},
		chainexchange.Request{},
		chainexchange.Response{},
	)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
