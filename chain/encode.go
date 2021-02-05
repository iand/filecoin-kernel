package chain

import (
	"bytes"

	"github.com/filecoin-project/go-state-types/abi"
	"github.com/filecoin-project/go-state-types/cbor"
	block "github.com/ipfs/go-block-format"
)

func EncodeAsBlock(v cbor.Marshaler) (block.Block, error) {
	buf := new(bytes.Buffer)
	if err := v.MarshalCBOR(buf); err != nil {
		return nil, err
	}

	data := buf.Bytes()

	c, err := abi.CidBuilder.Sum(data)
	if err != nil {
		return nil, err
	}

	return block.NewBlockWithCid(data, c)
}
