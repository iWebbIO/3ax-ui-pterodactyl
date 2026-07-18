//go:build wg_userspace

// Userspace WireGuard / AmneziaWG engine.
//
// This is the real implementation behind the `wg_userspace` build tag. It runs
// an amneziawg-go device (which speaks both plain WireGuard and AmneziaWG
// obfuscation) whose "TUN" is a gVisor netstack in the same process. Decrypted
// packets from peers are injected into the netstack; a TCP/UDP forwarder there
// NATs each flow out to the real internet through ordinary Go sockets. Nothing
// touches the kernel: no /dev/net/tun, no iptables, no CAP_NET_ADMIN.
//
// ── Dependencies (added to go.mod only for this tagged build) ────────────────
//
//	github.com/amnezia-vpn/amneziawg-go   — userspace AWG/WG device
//	gvisor.dev/gvisor                     — userspace TCP/IP stack (netstack)
//
// Run `GOFLAGS=-tags=wg_userspace go mod tidy` in a networked environment to
// populate go.mod/go.sum before building with `-tags wg_userspace`.
//
// ── Known version-sensitive spots (verify on first real build) ───────────────
//   - tun.Device interface shape (batched Read/Write, BatchSize) tracks
//     wireguard-go; amneziawg-go should match but confirm the signature.
//   - channel.Endpoint.ReadContext and PacketBuffer helpers (ToBuffer/DecRef).
//   - gonet.NewUDPConn / NewTCPConn argument order.
//   - AmneziaWG UAPI keys for H-ranges ("h1=100-800") and the i1 DSL ("<r 128>")
//     must be accepted by the pinned amneziawg-go IpcSet.
package wgengine

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/amnezia-vpn/amneziawg-go/conn"
	"github.com/amnezia-vpn/amneziawg-go/device"
	"github.com/amnezia-vpn/amneziawg-go/tun"

	"github.com/coinman-dev/3ax-ui/v2/logger"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/icmp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"
)

const nicID = 1

// udpTimeout bounds how long an idle userspace UDP flow is kept open.
const udpTimeout = 60 * time.Second

func init() {
	newTunnel = buildTunnel
}

// userspaceTunnel is a running amneziawg-go device + its netstack gateway.
type userspaceTunnel struct {
	dev       *device.Device
	nt        *netTun
	closeOnce sync.Once
}

func buildTunnel(cfg *Config) (tunnelImpl, error) {
	nt, err := newNetTun(cfg.Addresses, cfg.MTU)
	if err != nil {
		return nil, fmt.Errorf("netstack: %w", err)
	}

	dlog := &device.Logger{
		Verbosef: func(string, ...any) {},
		Errorf:   func(f string, a ...any) { logger.Warning("wgengine: " + fmt.Sprintf(f, a...)) },
	}
	dev := device.NewDevice(nt, conn.NewDefaultBind(), dlog)

	uapi, err := buildUAPI(cfg)
	if err != nil {
		dev.Close()
		return nil, err
	}
	if err := dev.IpcSet(uapi); err != nil {
		dev.Close()
		return nil, fmt.Errorf("IpcSet: %w", err)
	}
	if err := dev.Up(); err != nil {
		dev.Close()
		return nil, fmt.Errorf("device up: %w", err)
	}

	return &userspaceTunnel{dev: dev, nt: nt}, nil
}

func (t *userspaceTunnel) Close() error {
	t.closeOnce.Do(func() {
		t.dev.Close() // stops the device and closes the UDP listen socket
		t.nt.Close()  // cancels forwarders and destroys the stack
	})
	return nil
}

func (t *userspaceTunnel) Stats() ([]PeerStat, error) {
	uapi, err := t.dev.IpcGet()
	if err != nil {
		return nil, fmt.Errorf("IpcGet: %w", err)
	}
	return parseIpcStats(uapi), nil
}

// ── UAPI (config → device) ───────────────────────────────────────────────────

// awgOrder is the canonical order of AmneziaWG obfuscation keys in the UAPI.
var awgOrder = []string{"jc", "jmin", "jmax", "s1", "s2", "s3", "s4", "h1", "h2", "h3", "h4", "i1"}

func buildUAPI(cfg *Config) (string, error) {
	priv, err := b64ToHex(cfg.PrivateKey)
	if err != nil {
		return "", fmt.Errorf("private key: %w", err)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "private_key=%s\n", priv)
	fmt.Fprintf(&b, "listen_port=%d\n", cfg.ListenPort)

	// AmneziaWG obfuscation parameters, passed through as amneziawg-go expects
	// them. Absent for a plain WireGuard tunnel (byte-compatible with WG).
	for _, k := range awgOrder {
		if v := strings.TrimSpace(cfg.AWG[k]); v != "" {
			fmt.Fprintf(&b, "%s=%s\n", k, v)
		}
	}

	b.WriteString("replace_peers=true\n")
	for _, p := range cfg.Peers {
		pub, err := b64ToHex(p.PublicKey)
		if err != nil {
			return "", fmt.Errorf("peer public key: %w", err)
		}
		fmt.Fprintf(&b, "public_key=%s\n", pub)
		if p.PresharedKey != "" {
			psk, err := b64ToHex(p.PresharedKey)
			if err != nil {
				return "", fmt.Errorf("peer preshared key: %w", err)
			}
			fmt.Fprintf(&b, "preshared_key=%s\n", psk)
		}
		for _, aip := range p.AllowedIPs {
			fmt.Fprintf(&b, "allowed_ip=%s\n", aip.String())
		}
	}
	return b.String(), nil
}

// parseIpcStats converts a device IpcGet() dump into per-peer stats, translating
// the hex public keys back to the base64 form the panel matches clients on.
func parseIpcStats(uapi string) []PeerStat {
	var peers []PeerStat
	var cur *PeerStat
	flush := func() {
		if cur != nil {
			peers = append(peers, *cur)
			cur = nil
		}
	}
	for _, line := range strings.Split(uapi, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch k {
		case "public_key":
			flush()
			cur = &PeerStat{PublicKey: hexToB64(v)}
		case "endpoint":
			if cur != nil {
				cur.Endpoint = v
			}
		case "last_handshake_time_sec":
			if cur != nil {
				cur.LatestHandshake, _ = strconv.ParseInt(v, 10, 64)
			}
		case "rx_bytes":
			if cur != nil {
				cur.TransferRx, _ = strconv.ParseInt(v, 10, 64)
			}
		case "tx_bytes":
			if cur != nil {
				cur.TransferTx, _ = strconv.ParseInt(v, 10, 64)
			}
		case "persistent_keepalive_interval":
			if cur != nil {
				cur.PersistentKeepalive, _ = strconv.Atoi(v)
			}
		}
	}
	flush()
	return peers
}

func b64ToHex(s string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func hexToB64(s string) string {
	raw, err := hex.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return s
	}
	return base64.StdEncoding.EncodeToString(raw)
}

// ── netTun: a tun.Device backed by a gVisor netstack gateway ─────────────────

type netTun struct {
	ep     *channel.Endpoint
	stack  *stack.Stack
	events chan tun.Event
	mtu    int
	ctx    context.Context
	cancel context.CancelFunc
	once   sync.Once
}

func newNetTun(addrs []netip.Prefix, mtu int) (*netTun, error) {
	s := stack.New(stack.Options{
		NetworkProtocols: []stack.NetworkProtocolFactory{
			ipv4.NewProtocol, ipv6.NewProtocol,
		},
		TransportProtocols: []stack.TransportProtocolFactory{
			tcp.NewProtocol, udp.NewProtocol, icmp.NewProtocol4, icmp.NewProtocol6,
		},
		HandleLocal: false,
	})

	ep := channel.New(1024, uint32(mtu), "")
	if err := s.CreateNIC(nicID, ep); err != nil {
		return nil, fmt.Errorf("CreateNIC: %s", err)
	}

	// Assign the server's tunnel addresses so the stack answers for them, then
	// accept traffic to any destination (promiscuous) and originate from any
	// source (spoofing) so it can act as a forwarding gateway.
	for _, p := range addrs {
		var proto tcpip.NetworkProtocolNumber
		if p.Addr().Is4() {
			proto = ipv4.ProtocolNumber
		} else {
			proto = ipv6.ProtocolNumber
		}
		pa := tcpip.ProtocolAddress{
			Protocol: proto,
			AddressWithPrefix: tcpip.AddrFromSlice(p.Addr().AsSlice()).
				WithPrefix(),
		}
		if err := s.AddProtocolAddress(nicID, pa, stack.AddressProperties{}); err != nil {
			return nil, fmt.Errorf("AddProtocolAddress %s: %s", p, err)
		}
	}
	s.SetPromiscuousMode(nicID, true)
	s.SetSpoofing(nicID, true)
	s.SetRouteTable([]tcpip.Route{
		{Destination: header.IPv4EmptySubnet, NIC: nicID},
		{Destination: header.IPv6EmptySubnet, NIC: nicID},
	})

	ctx, cancel := context.WithCancel(context.Background())
	nt := &netTun{
		ep:     ep,
		stack:  s,
		events: make(chan tun.Event, 1),
		mtu:    mtu,
		ctx:    ctx,
		cancel: cancel,
	}

	installForwarders(s)

	nt.events <- tun.EventUp
	return nt, nil
}

// installForwarders wires the TCP and UDP forwarders that turn the netstack into
// a NAT gateway: any connection a peer opens to any destination is dialed out
// over a normal socket and spliced to the in-stack endpoint.
func installForwarders(s *stack.Stack) {
	tcpFwd := tcp.NewForwarder(s, 0, 2048, func(r *tcp.ForwarderRequest) {
		id := r.ID()
		var wq waiter.Queue
		ep, err := r.CreateEndpoint(&wq)
		if err != nil {
			r.Complete(true)
			return
		}
		r.Complete(false)
		local := gonet.NewTCPConn(&wq, ep)
		go proxyTCP(local, endpointAddr(id.LocalAddress, id.LocalPort))
	})
	s.SetTransportProtocolHandler(tcp.ProtocolNumber, tcpFwd.HandlePacket)

	// gVisor's UDP forwarder handler returns a bool ("was this request
	// handled") and gonet.NewUDPConn no longer takes the *stack.Stack — both
	// changed in recent netstack. We always consume the request (return true);
	// on endpoint-creation failure we just drop it rather than emit ICMP.
	udpFwd := udp.NewForwarder(s, func(r *udp.ForwarderRequest) bool {
		id := r.ID()
		var wq waiter.Queue
		ep, err := r.CreateEndpoint(&wq)
		if err != nil {
			return true
		}
		local := gonet.NewUDPConn(&wq, ep)
		go proxyUDP(local, endpointAddr(id.LocalAddress, id.LocalPort))
		return true
	})
	s.SetTransportProtocolHandler(udp.ProtocolNumber, udpFwd.HandlePacket)
}

func proxyTCP(local net.Conn, dst string) {
	defer local.Close()
	remote, err := net.DialTimeout("tcp", dst, 10*time.Second)
	if err != nil {
		return
	}
	defer remote.Close()
	done := make(chan struct{}, 2)
	go func() { io.Copy(remote, local); done <- struct{}{} }()
	go func() { io.Copy(local, remote); done <- struct{}{} }()
	<-done
}

func proxyUDP(local net.Conn, dst string) {
	defer local.Close()
	remote, err := net.DialTimeout("udp", dst, 10*time.Second)
	if err != nil {
		return
	}
	defer remote.Close()
	done := make(chan struct{}, 2)
	pump := func(dstConn, srcConn net.Conn) {
		buf := make([]byte, 65535)
		for {
			srcConn.SetReadDeadline(time.Now().Add(udpTimeout))
			n, err := srcConn.Read(buf)
			if n > 0 {
				dstConn.SetWriteDeadline(time.Now().Add(udpTimeout))
				if _, werr := dstConn.Write(buf[:n]); werr != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
		done <- struct{}{}
	}
	go pump(remote, local)
	go pump(local, remote)
	<-done
}

// endpointAddr renders a gVisor address+port (the original destination the peer
// dialed) as a host:port string for net.Dial.
func endpointAddr(a tcpip.Address, port uint16) string {
	return net.JoinHostPort(net.IP(a.AsSlice()).String(), strconv.Itoa(int(port)))
}

// --- tun.Device implementation (bridges amneziawg-go <-> the netstack) ---

// Read hands the device packets the stack wants to send back to peers (replies
// from the internet). It blocks until a packet is available or the tun closes.
func (t *netTun) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	pkt := t.ep.ReadContext(t.ctx)
	if pkt == nil {
		return 0, os.ErrClosed
	}
	defer pkt.DecRef()
	data := pkt.ToBuffer()
	b := data.Flatten()
	n := copy(bufs[0][offset:], b)
	sizes[0] = n
	return 1, nil
}

// Write injects decrypted packets from peers (client → internet) into the stack.
func (t *netTun) Write(bufs [][]byte, offset int) (int, error) {
	for _, buf := range bufs {
		pkt := buf[offset:]
		if len(pkt) == 0 {
			continue
		}
		var proto tcpip.NetworkProtocolNumber
		switch pkt[0] >> 4 {
		case 4:
			proto = header.IPv4ProtocolNumber
		case 6:
			proto = header.IPv6ProtocolNumber
		default:
			continue
		}
		pb := stack.NewPacketBuffer(stack.PacketBufferOptions{
			Payload: buffer.MakeWithData(bytes.Clone(pkt)),
		})
		t.ep.InjectInbound(proto, pb)
		pb.DecRef()
	}
	return len(bufs), nil
}

func (t *netTun) MTU() (int, error)        { return t.mtu, nil }
func (t *netTun) Name() (string, error)    { return "wgns", nil }
func (t *netTun) File() *os.File           { return nil }
func (t *netTun) Events() <-chan tun.Event { return t.events }
func (t *netTun) BatchSize() int           { return 1 }

func (t *netTun) Close() error {
	t.once.Do(func() {
		t.cancel()
		t.ep.Close()
		t.stack.Close()
		close(t.events)
	})
	return nil
}
