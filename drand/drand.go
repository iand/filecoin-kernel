package drand

import "github.com/filecoin-project/go-state-types/abi"

type Schedule []Point

type Point struct {
	Start  abi.ChainEpoch
	Config Config
}

type Config struct {
	Servers       []string
	Relays        []string
	ChainInfoJSON string
}
