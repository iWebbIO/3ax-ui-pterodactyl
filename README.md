[English](/README.md) | [Русский](/README.ru_RU.md)

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="./media/3ax-ui-dark.png">
    <img alt="3ax-ui" src="./media/3ax-ui-light.png">
  </picture>
</p>

<p align="center"><b>3AX-UI for Pterodactyl</b> — the 3ax-ui proxy panel packaged as a <a href="https://pterodactyl.io">Pterodactyl</a> egg that runs <b>fully unprivileged</b>: no root, no <code>/dev/net/tun</code>, no extra capabilities.</p>

[![Go Version](https://img.shields.io/github/go-mod/go-version/iWebbIO/3ax-ui-pterodactyl.svg)](#)
[![License](https://img.shields.io/badge/license-GPL%20V3-blue.svg?longCache=true)](https://www.gnu.org/licenses/gpl-3.0.en.html)

> [!IMPORTANT]
> This project is intended for personal use only. Please do not use it for illegal purposes.

---

## What this is

This repository is a **downstream fork of [3ax-ui](https://github.com/coinman-dev/3ax-ui)** whose **sole purpose is to run the panel on Pterodactyl**. Game-panel containers are unprivileged — no root, only `/home/container` is writable, and ports come from allocations — so this fork specializes the panel for exactly that environment.

Everything runs as a single supervised process inside a custom Docker image ("yolk") where all runtime binaries (the panel, Xray-core, geo data, `mtg`/`mtg-multi`) are **baked in**. The node downloads nothing at install time; it only prepares the server volume.

If you want a traditional VPS install (systemd, `install.sh`, kernel AmneziaWG), use the upstream project [coinman-dev/3ax-ui](https://github.com/coinman-dev/3ax-ui) instead — that path is inherited here but is **not** what this fork targets.

---

## Protocol support on Pterodactyl

| Protocol / feature | Status | Notes |
|---|---|---|
| Web panel + subscriptions | ✅ Available | Panel binds the server's primary allocation |
| **VLESS, VMess, Trojan, Shadowsocks** | ✅ Available | Full Xray transports: TCP, WS, gRPC, HTTPUpgrade, XHTTP, mKCP, QUIC — plus **REALITY** and **XTLS** |
| **SOCKS5 & HTTP** proxies | ✅ Available | Full per-user infrastructure (traffic, quota, expiry, IP limit) |
| **MTProto** (Telegram FakeTLS) | ✅ Available | `mtg-multi` (multi-user) on amd64/arm64, single-secret `mtg` elsewhere |
| **Cloudflare Tunnel** (cloudflared) | ✅ Available | Built-in, supervised. Expose the panel/inbounds at an HTTPS hostname with no inbound port — quick (`trycloudflare.com`) or token (your domain) |
| **AmneziaWG** (1.x & 2.0) | ✅ Userspace¹ | In-process amneziawg-go + gVisor netstack — obfuscation preserved, no root |
| **native WireGuard** | ✅ Userspace¹ | Same in-process engine |
| Native public IPv6 without NAT (NDP proxy) | ❌ Not on Pterodactyl | Needs `CAP_NET_ADMIN` + a routed prefix on the host; egress is NAT/routed instead |
| Per-client iptables port forwarding | ❌ Not on Pterodactyl | Needs root/iptables in the container |
| fail2ban | ❌ Disabled | Needs root/iptables |

Everything runs with zero privileges. ¹ AmneziaWG / native WireGuard use an **in-process userspace engine** (`amneziawg-go` + a gVisor netstack, `shared/wgengine`) compiled into the Pterodactyl image via the `wg_userspace` build tag — no kernel module, no `/dev/net/tun`. See [`shared/wgengine/README.md`](shared/wgengine/README.md) for its design and the build-verification notes, and [`.ai/PTERODACTYL_EGG_PLAN.md`](.ai/PTERODACTYL_EGG_PLAN.md) for the full plan.

---

## Quick start

Full operator guide: **[`pterodactyl/README.md`](pterodactyl/README.md)**.

**1. Build & push the image** (from the repo root):

```bash
docker buildx build -f pterodactyl/Dockerfile \
  --platform linux/amd64,linux/arm64 \
  -t ghcr.io/iwebbio/3ax-ui-pterodactyl:latest --push .
```

**2. Import the egg:** Pterodactyl admin → **Nests → Import Egg** → upload [`pterodactyl/egg-3ax-ui.json`](pterodactyl/egg-3ax-ui.json).

**3. Create a server:** assign the **primary allocation** for the panel plus **one extra high port per inbound** you plan to run. Only allocated ports are reachable from the internet (TCP+UDP).

**4. Log in:** start the server; when the console shows `3AX-UI online — panel listening on port <N>`, open `http://<node-ip>:<primary-port>/`.

---

## Protocol notes

### MTProto — Telegram proxy (FakeTLS)
A FakeTLS proxy for Telegram, run as a standalone `mtg` / `mtg-multi` sidecar (not Xray) and managed from the **Inbounds** page like any other protocol.

- Pick a public (allocated) port and a **FakeTLS fronting domain** — the connection is disguised as TLS 1.3 to that domain; a **↻** button fills a random domain from a curated list.
- The FakeTLS secret is generated automatically and shared as a **`tg://proxy` deep link** + QR.
- **Multi-user** on amd64/arm64 via [mtg-multi](https://github.com/dolonet/mtg-multi): many clients on one port, each with a unique UID, name, secret, link/QR, traffic, quota, expiry, and online status. Other arches fall back to single-secret [mtg](https://github.com/9seconds/mtg).
- Optional **Route through Xray**: mtg dials Telegram through a loopback SOCKS bridge injected into the running Xray config, so egress obeys Xray routing (useful when Telegram is blocked on the node itself).

### Cloudflare Tunnel (cloudflared)
A **built-in, supervised** Cloudflare Tunnel — the cleanest way to reach the panel on Pterodactyl, since it's outbound-only (no inbound allocation, no public IP, no cert; Cloudflare terminates TLS). **Quick mode** (`XUI_CF_ENABLE=true`) prints an instant `*.trycloudflare.com` URL in the console; **token mode** (`XUI_CF_TOKEN=…`) runs a named tunnel on your own domain via the Cloudflare Zero Trust dashboard. The `cloudflared` binary is baked into the image; the panel starts/stops it with the process and auto-restarts it on drops. Configure it in the panel under **Settings → Cloudflare** (with live status + the quick-tunnel URL), via `XUI_CF_*` env vars, or the `/panel/cloudflared/*` API. See [`pterodactyl/README.md`](pterodactyl/README.md#cloudflare-tunnel-cloudflared--recommended).

### SOCKS5 & HTTP proxies with per-user infrastructure
Xray's `mixed` (SOCKS5) and `http` inbounds share the same VLESS-style stack as VLESS/VMess/Trojan/Shadowsocks: expandable peer table with per-client traffic, expiry, quota, IP limit, and enable toggle; auto-generated credentials (regenerable); per-user stats via Xray's standard traffic keys, so quota/expiry jobs handle them automatically. Usernames stay editable after creation without resetting traffic counters.

### AmneziaWG & native WireGuard (userspace)
AmneziaWG is WireGuard with packet obfuscation that makes traffic indistinguishable from random noise (defeats DPI in Russia/Iran/China). On a normal host this fork drives it via the kernel (`awg-quick` + iptables + NDP) — impossible in an unprivileged Pterodactyl container.

This fork adds an **in-process userspace engine** (`shared/wgengine`): `amneziawg-go` runs the WireGuard/AmneziaWG protocol on a normal UDP socket, its "TUN" is a **gVisor netstack** in the same process, and a TCP/UDP forwarder NATs client flows out to the internet over ordinary sockets — no TUN device, no capabilities. Obfuscation is fully preserved; the tradeoff is that native-public-IPv6 handout (NDP) and per-client iptables port-forwarding are unavailable, and IPv6 egress is NAT'd. It's selected by `XUI_WG_MODE=userspace` and compiled into the image with `-tags wg_userspace`. Design + build notes: [`shared/wgengine/README.md`](shared/wgengine/README.md).

---

## What's different from a normal 3ax-ui install

| Area | Normal (VPS) | This fork (Pterodactyl) |
|---|---|---|
| Process supervision | systemd + `x-ui.sh` menu | Single `/app/x-ui` process, supervised by Wings; `^C` = graceful stop |
| Storage | `/etc/x-ui`, `/var/log/x-ui`, `bin/` | All under `/home/container` via `XUI_DB_FOLDER` / `XUI_LOG_FOLDER` / `XUI_BIN_FOLDER` |
| Ports | any port, incl. privileged | Only server **allocations** (high ports); panel on the primary |
| Binaries | fetched by `install.sh` | Baked into the Docker image |
| WireGuard/AmneziaWG | kernel (`awg-quick`, TUN, iptables) | In-process userspace engine, no root (`shared/wgengine`) |
| fail2ban | enabled | disabled (no root) |
| TLS / access | ACME HTTP-01 on :80/:443 | **Cloudflare Tunnel** (built-in, recommended — HTTPS hostname, no inbound port), or upload certs to `/home/container/cert` |

Set the **external address** (node IP or domain) in the panel settings so client links and subscription URLs resolve correctly — the container can't auto-detect the node's public address.

---

## Repository layout (Pterodactyl-specific)

```
pterodactyl/
├── Dockerfile        — unprivileged (uid 988) yolk; bakes panel + Xray + mtg
├── entrypoint.sh     — maps XUI_* to /home/container, first-boot port/admin, exec panel
├── fetch-bins.sh     — bakes MTProto sidecars per arch
├── egg-3ax-ui.json   — Pterodactyl egg (PTDL_v2)
└── README.md         — operator guide
.ai/PTERODACTYL_EGG_PLAN.md — full porting plan, decisions, and phase status
```

The upstream panel source (`web/`, `xray/`, `awg/`, `wg/`, `mtproto/`, `sub/`, …) is unchanged except for the small Pterodactyl adaptations tracked in the plan.

---

## Compatible clients

| Protocol | Client | Platforms |
|----------|--------|-----------|
| VLESS / VMess / Trojan / Shadowsocks | v2rayN, v2rayNG, Nekoray, sing-box, Streisand, etc. | All platforms |
| SOCKS5 / HTTP | any standard proxy client | All platforms |
| MTProto | Telegram (built-in proxy support) | All platforms |
| AmneziaWG | AmneziaVPN — [amnezia.org](https://amnezia.org) | Android, iOS, Windows, macOS, Linux |
| native WireGuard | Official WireGuard | All platforms |

> Standard WireGuard clients are **not** compatible with AmneziaWG — they don't support obfuscation parameters.

---

## Based on

- **[coinman-dev/3ax-ui](https://github.com/coinman-dev/3ax-ui)** — the direct upstream (adds AmneziaWG, native WireGuard, MTProto).
- **[MHSanaei/3x-ui](https://github.com/MHSanaei/3x-ui)** — the panel 3ax-ui itself forks (VLESS, VMess, Trojan, Shadowsocks, Xray, subscriptions, Telegram bot).
- MTProto sidecars: **[9seconds/mtg](https://github.com/9seconds/mtg)** (single-secret) and **[dolonet/mtg-multi](https://github.com/dolonet/mtg-multi)** (multi-user).

## Acknowledgements

- [MHSanaei](https://github.com/MHSanaei/) — author of the original 3x-ui
- [alireza0](https://github.com/alireza0/) — author of the original x-ui
- [coinman-dev](https://github.com/coinman-dev/3ax-ui) — the 3ax-ui fork this project builds on
- [9seconds/mtg](https://github.com/9seconds/mtg) and [dolonet/mtg-multi](https://github.com/dolonet/mtg-multi) — MTProto sidecars
- [Iran v2ray rules](https://github.com/chocolate4u/Iran-v2ray-rules) (GPL-3.0) · [Russia v2ray rules](https://github.com/runetfreedom/russia-v2ray-rules-dat) (GPL-3.0)

---

## License

Distributed under the same license as the original 3x-ui — [GNU GPL v3](LICENSE).
