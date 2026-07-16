package wg

import (
	"sync"

	appconfig "github.com/coinman-dev/3ax-ui/v2/config"
	"github.com/coinman-dev/3ax-ui/v2/shared/wgengine"
)

// lastConf caches the most recently generated server config per interface, so
// the userspace engine (whose InterfaceUp only receives the interface name) can
// start the tunnel from it. In kernel mode this is unused — the config lives on
// disk and wg-quick reads it.
var lastConf sync.Map // interfaceName(string) -> config(string)

// userspaceMode reports whether WireGuard runs through the in-process userspace
// engine (no root/TUN) instead of wg-quick. Driven by XUI_WG_MODE=userspace.
func userspaceMode() bool { return appconfig.IsWgUserspace() }

func storedConf(interfaceName string) string {
	if v, ok := lastConf.Load(interfaceName); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// toPeerStatus adapts engine stats to this package's PeerStatus type so the
// service layer sees an identical shape in both modes.
func toPeerStatus(in []wgengine.PeerStat) []PeerStatus {
	out := make([]PeerStatus, 0, len(in))
	for _, p := range in {
		out = append(out, PeerStatus{
			PublicKey:           p.PublicKey,
			Endpoint:            p.Endpoint,
			LatestHandshake:     p.LatestHandshake,
			TransferRx:          p.TransferRx,
			TransferTx:          p.TransferTx,
			PersistentKeepalive: p.PersistentKeepalive,
		})
	}
	return out
}
