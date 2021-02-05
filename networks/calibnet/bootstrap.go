package calibnet

import (
	"fmt"

	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/multiformats/go-multiaddr"
)

var BootstrapPeers []peer.AddrInfo

var bootstrapAddresses = []string{
	"/dns4/bootstrap-0.calibration.fildev.network/tcp/1347/p2p/12D3KooWK1QYsm6iqyhgH7vqsbeoNoKHbT368h1JLHS1qYN36oyc",
	"/dns4/bootstrap-1.calibration.fildev.network/tcp/1347/p2p/12D3KooWKDyJZoPsNak1iYNN1GGmvGnvhyVbWBL6iusYfP3RpgYs",
	"/dns4/bootstrap-2.calibration.fildev.network/tcp/1347/p2p/12D3KooWJRSTnzABB6MYYEBbSTT52phQntVD1PpRTMh1xt9mh6yH",
	"/dns4/bootstrap-3.calibration.fildev.network/tcp/1347/p2p/12D3KooWQLi3kY6HnMYLUtwCe26zWMdNhniFgHVNn1DioQc7NiWv",
}

func init() {
	for _, addr := range bootstrapAddresses {
		maddr := multiaddr.StringCast(addr)
		p, err := peer.AddrInfoFromP2pAddr(maddr)
		if err != nil {
			panic(fmt.Errorf("failed to parse bootstrap peer address %q: %w", maddr, err))
		}
		BootstrapPeers = append(BootstrapPeers, *p)
	}
}
