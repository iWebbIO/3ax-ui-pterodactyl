# wgengine — userspace WireGuard / AmneziaWG

Runs WG/AWG tunnels **entirely in-process**: no kernel interface, no
`/dev/net/tun`, no `CAP_NET_ADMIN`, no iptables. This is what lets AmneziaWG and
native WireGuard work in an unprivileged Pterodactyl container.

## How it works

```
peers ──UDP──▶ amneziawg-go device ──decrypt──▶ netTun ──inject──▶ gVisor stack
                                                                      │
                                                          tcp/udp Forwarder
                                                                      │
                                                              net.Dial ▶ internet
```

- **amneziawg-go** terminates the WireGuard/AmneziaWG protocol (obfuscation and
  all) on a normal UDP socket bound to the inbound's `ListenPort` — which on
  Pterodactyl is one of the server's UDP allocations.
- Its "TUN" is **`netTun`**, a `tun.Device` backed by a **gVisor netstack** in
  the same process. Decrypted client packets are injected into the stack.
- The stack runs in promiscuous + spoofing mode with a default route, and a
  **TCP/UDP forwarder** NATs every client flow out to the real destination over
  an ordinary Go socket. Replies flow back through the stack, get encrypted, and
  return to the peer.

Result: a full VPN gateway with zero privileges. The tradeoff vs. the kernel
path is that **native-public-IPv6-to-client (NDP proxy) and per-client iptables
port-forwarding are not available** — IPv6 egress is NAT'd.

## Files

| File | Build | Purpose |
|---|---|---|
| `engine.go` | always | Dep-free: config parser, lifecycle registry, value types |
| `engine_test.go` | always | Parser unit tests (run offline) |
| `tunnel_stub.go` | `!wg_userspace` | Leaves `newTunnel` nil → clean `ErrNotBuilt` |
| `tunnel_userspace.go` | `wg_userspace` | amneziawg-go device + gVisor gateway |

The default panel build **excludes** the gVisor code, so it never pulls those
heavy deps. Only a `-tags wg_userspace` build (the Pterodactyl image) compiles it.

## Selection

`config.IsWgUserspace()` (env `XUI_WG_MODE=userspace`) flips the `awg`/`wg`
managers from `awg-quick`/`wg-quick` to this engine. The Pterodactyl entrypoint
sets it; a plain VPS build defaults to `kernel` and is unaffected.

## Building

```bash
# deps live outside the committed go.mod; add them in a networked env:
go get github.com/amnezia-vpn/amneziawg-go@latest gvisor.dev/gvisor@go
go build -tags wg_userspace ./...
```

The Pterodactyl `Dockerfile` does exactly this.

## ⚠️ Verification status

`engine.go` (parser + registry) is unit-tested and compiles in the default
build. `tunnel_userspace.go` is written against recent amneziawg-go /
wireguard-go / gVisor APIs but **has not been compiled here** (this dev
environment has no module network). Confirm these version-sensitive points on
the first real `-tags wg_userspace` build:

1. **`tun.Device` interface** — the batched `Read(bufs, sizes, offset)` /
   `Write(bufs, offset)` / `BatchSize()` shape must match the pinned
   amneziawg-go. If it differs, adjust `netTun`'s methods.
2. **gVisor API** — `channel.Endpoint.ReadContext`, `PacketBuffer.ToBuffer()`/
   `DecRef()`, `gonet.NewTCPConn`/`NewUDPConn` argument order, and
   `tcpip.AddrFromSlice`/`AsSlice` can drift between gVisor revisions.
3. **AmneziaWG UAPI** — that `IpcSet` accepts H-ranges (`h1=100000-800000`) and
   the I1 DSL (`i1=<r 128>`) as passed through from the config. If not, the
   values may need pre-encoding.

These are localized to `tunnel_userspace.go`; the wiring around it is stable.
