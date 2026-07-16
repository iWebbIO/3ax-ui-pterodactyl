// Package wgengine runs WireGuard / AmneziaWG tunnels entirely in userspace,
// with no kernel interface, no /dev/net/tun and no capabilities — the mode the
// panel uses on Pterodactyl.
//
// The heavy lifting (the amneziawg-go device + a gVisor netstack that NATs peer
// traffic out to the internet) lives in tunnel_userspace.go behind the
// `wg_userspace` build tag, so the default build of the panel never pulls in
// gVisor. This file holds only the dependency-free surface: the awg-quick
// config parser, the lifecycle registry, and the value types the awg/ and wg/
// packages consume. When built without the tag, newTunnel returns a clear
// "engine not compiled" error (see tunnel_stub.go).
package wgengine

import (
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"sync"
)

// PeerStat mirrors the fields the panel reads from `awg show <if> dump`, so the
// awg/ and wg/ managers can convert it 1:1 into their own PeerStatus. PublicKey
// is base64 (the same encoding stored on the client row), NOT the hex UAPI form.
type PeerStat struct {
	PublicKey           string
	Endpoint            string
	LatestHandshake     int64 // unix seconds
	TransferRx          int64 // bytes received by the server from this peer
	TransferTx          int64 // bytes sent by the server to this peer
	PersistentKeepalive int
}

// Peer is one [Peer] section of a parsed server config.
type Peer struct {
	PublicKey    string // base64
	PresharedKey string // base64, may be empty
	AllowedIPs   []netip.Prefix
}

// Config is the parsed, engine-ready form of an awg-quick / wg-quick server
// config. Kernel-only directives (PostUp/PostDown/DNS/Endpoint) are ignored:
// routing and NAT are handled by the userspace gateway, not iptables.
type Config struct {
	PrivateKey string // base64
	Addresses  []netip.Prefix
	ListenPort int
	MTU        int

	// AWG holds the AmneziaWG obfuscation parameters exactly as they appear in
	// the config (keys lowercased: jc, jmin, jmax, s1, s2, s3, s4, h1..h4, i1).
	// Empty/absent for a plain WireGuard tunnel, which is byte-compatible.
	AWG map[string]string

	Peers []Peer
}

// tunnelImpl is the running-tunnel handle produced by newTunnel. Its concrete
// type is defined only in the build-tagged implementation file.
type tunnelImpl interface {
	// Stats returns per-peer live counters read from the device.
	Stats() ([]PeerStat, error)
	// Close tears the tunnel down and releases the UDP socket.
	Close() error
}

// newTunnel builds and starts a userspace tunnel from cfg. It is assigned by an
// init() in either tunnel_userspace.go (real) or tunnel_stub.go (not compiled).
var newTunnel func(cfg *Config) (tunnelImpl, error)

// ErrNotBuilt is returned when userspace mode is requested but the binary was
// built without the `wg_userspace` tag.
var ErrNotBuilt = errors.New("wgengine: userspace engine not compiled (build with -tags wg_userspace)")

type registryEntry struct {
	conf string
	impl tunnelImpl
}

var (
	mu       sync.Mutex
	registry = map[string]*registryEntry{}
)

// Up starts (or restarts) the tunnel named by interfaceName from an awg-quick /
// wg-quick config string. Restarting on an already-running interface replaces
// the old tunnel — peer sets are small so a rebuild is cheaper than diffing.
func Up(interfaceName, conf string) error {
	cfg, err := ParseQuickConfig(conf)
	if err != nil {
		return fmt.Errorf("wgengine: parse config for %s: %w", interfaceName, err)
	}
	if newTunnel == nil {
		return ErrNotBuilt
	}
	impl, err := newTunnel(cfg)
	if err != nil {
		return fmt.Errorf("wgengine: start %s: %w", interfaceName, err)
	}

	mu.Lock()
	if old := registry[interfaceName]; old != nil {
		_ = old.impl.Close()
	}
	registry[interfaceName] = &registryEntry{conf: conf, impl: impl}
	mu.Unlock()
	return nil
}

// Reload applies a new config to a running interface (currently a full rebuild).
func Reload(interfaceName, conf string) error {
	return Up(interfaceName, conf)
}

// Down stops the named tunnel. It is a no-op if the tunnel is not running.
func Down(interfaceName string) error {
	mu.Lock()
	entry := registry[interfaceName]
	delete(registry, interfaceName)
	mu.Unlock()
	if entry == nil {
		return nil
	}
	return entry.impl.Close()
}

// Running reports whether the named tunnel is currently up.
func Running(interfaceName string) bool {
	mu.Lock()
	defer mu.Unlock()
	return registry[interfaceName] != nil
}

// Stats returns live per-peer counters for the named tunnel.
func Stats(interfaceName string) ([]PeerStat, error) {
	mu.Lock()
	entry := registry[interfaceName]
	mu.Unlock()
	if entry == nil {
		return nil, fmt.Errorf("wgengine: interface %s is not running", interfaceName)
	}
	return entry.impl.Stats()
}

// ParseQuickConfig parses the subset of the awg-quick / wg-quick INI format that
// the panel generates. It is deterministic and dependency-free so it can be unit
// tested without the gVisor stack. Unknown / kernel-only keys are ignored.
func ParseQuickConfig(conf string) (*Config, error) {
	cfg := &Config{AWG: map[string]string{}}
	section := "" // "interface" | "peer" | ""
	var cur *Peer // current peer being filled

	awgKeys := map[string]bool{
		"jc": true, "jmin": true, "jmax": true,
		"s1": true, "s2": true, "s3": true, "s4": true,
		"h1": true, "h2": true, "h3": true, "h4": true, "i1": true,
	}

	flushPeer := func() {
		if cur != nil {
			cfg.Peers = append(cfg.Peers, *cur)
			cur = nil
		}
	}

	for _, raw := range strings.Split(conf, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			switch strings.ToLower(strings.Trim(line, "[]")) {
			case "interface":
				flushPeer()
				section = "interface"
			case "peer":
				flushPeer()
				section = "peer"
				cur = &Peer{}
			default:
				section = ""
			}
			continue
		}

		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		val = strings.TrimSpace(val)

		switch section {
		case "interface":
			switch key {
			case "privatekey":
				cfg.PrivateKey = val
			case "address":
				cfg.Addresses = appendPrefixes(cfg.Addresses, val)
			case "listenport":
				cfg.ListenPort = atoiSafe(val)
			case "mtu":
				cfg.MTU = atoiSafe(val)
			default:
				if awgKeys[key] {
					cfg.AWG[key] = val
				}
				// PostUp/PostDown/DNS/Table/etc. are intentionally ignored.
			}
		case "peer":
			if cur == nil {
				cur = &Peer{}
			}
			switch key {
			case "publickey":
				cur.PublicKey = val
			case "presharedkey":
				cur.PresharedKey = val
			case "allowedips":
				cur.AllowedIPs = appendPrefixes(cur.AllowedIPs, val)
			}
		}
	}
	flushPeer()

	if cfg.PrivateKey == "" {
		return nil, errors.New("wgengine: config has no PrivateKey")
	}
	if cfg.ListenPort <= 0 {
		return nil, errors.New("wgengine: config has no valid ListenPort")
	}
	if cfg.MTU <= 0 {
		cfg.MTU = 1420
	}
	return cfg, nil
}

// appendPrefixes parses a comma-separated list of CIDRs (or bare IPs, which are
// promoted to /32 or /128) and appends the valid ones.
func appendPrefixes(dst []netip.Prefix, list string) []netip.Prefix {
	for _, part := range strings.Split(list, ",") {
		p := strings.TrimSpace(part)
		if p == "" {
			continue
		}
		if strings.Contains(p, "/") {
			if pref, err := netip.ParsePrefix(p); err == nil {
				dst = append(dst, pref)
			}
			continue
		}
		if addr, err := netip.ParseAddr(p); err == nil {
			bits := 32
			if addr.Is6() {
				bits = 128
			}
			dst = append(dst, netip.PrefixFrom(addr, bits))
		}
	}
	return dst
}

func atoiSafe(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}
