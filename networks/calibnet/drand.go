package calibnet

import (
	"github.com/iand/filecoin-kernel/drand"
)

func DrandSchedule() drand.Schedule {
	// Sorted ascending by start
	return drand.Schedule{
		drand.Point{
			Start: 0,
			Config: drand.Config{
				Servers: []string{
					"https://api.drand.sh",
					"https://api2.drand.sh",
					"https://api3.drand.sh",
					"https://drand.cloudflare.com",
				},
				Relays: []string{
					"/dnsaddr/api.drand.sh/",
					"/dnsaddr/api2.drand.sh/",
					"/dnsaddr/api3.drand.sh/",
				},
				ChainInfoJSON: `{"public_key":"868f005eb8e6e4ca0a47c8a77ceaa5309a47978a7c71bc5cce96366b5d7a569937c529eeda66c7293784a9402801af31","period":30,"genesis_time":1595431050,"hash":"8990e7a9aaed2ffed73dbd7092123d6f289930540d7651336225dc172e51b2ce","groupHash":"176f93498eac9ca337150b46d21dd58673ea4e3581185f869672e59fa4cb390a"}`,
			},
		},
	}
}
