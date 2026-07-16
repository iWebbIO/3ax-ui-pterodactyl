# 3AX-UI on Pterodactyl

Run the 3AX-UI panel as a Pterodactyl egg — fully unprivileged (no root, no
`/dev/net/tun`, no extra capabilities). Every runtime binary (panel, Xray-core,
geo data, `mtg`/`mtg-multi`) is **baked into the Docker image**; the node only
prepares the server volume at install time.

## Status

| Capability | Status | Notes |
|---|---|---|
| Panel + subscriptions | ✅ | Web UI on the primary allocation |
| Xray protocols (VLESS, VMess, Trojan, Shadowsocks, SOCKS, HTTP, all transports, REALITY, XTLS) | ✅ | Userspace — work as-is |
| MTProto (`mtg` / `mtg-multi`) | ✅ | amd64/arm64 get multi-user; others single-secret |
| AmneziaWG / native WireGuard | ✅ userspace | In-process `amneziawg-go` + gVisor engine (`XUI_WG_MODE=userspace`, built with `-tags wg_userspace`). See [`../shared/wgengine/README.md`](../shared/wgengine/README.md) for build-verification notes. IPv6-via-NDP and iptables port-forwarding are kernel-only and unavailable here. |

## 1. Build & push the image

From the repo root:

```bash
docker buildx build -f pterodactyl/Dockerfile \
  --platform linux/amd64,linux/arm64 \
  -t ghcr.io/iwebbio/3ax-ui-pterodactyl:latest --push .
```

(Single-arch local test: `docker build -f pterodactyl/Dockerfile -t 3ax-ui-ptero:test .`)

If you push to a different registry/tag, update `docker_images` in
`egg-3ax-ui.json` to match.

> **Make the image pullable by your node.** GHCR marks new packages **private**,
> so Wings pulling anonymously fails with `error from registry: denied`. Either:
> - make it public — GitHub → your **Packages** → `3ax-ui-pterodactyl` → **Package
>   settings** → **Change visibility → Public**; or
> - keep it private and log the node's Docker in once:
>   `docker login ghcr.io -u <user> -p <PAT-with-read:packages>` (Wings uses the
>   host Docker's credentials).
>
> The image path is all-lowercase (`ghcr.io/iwebbio/...`) even if your GitHub name
> is mixed-case.

> **If `docker build` fails at the `wg_userspace` step** (the in-process
> AmneziaWG/WireGuard engine is the one component compiled against fast-moving
> gVisor/amneziawg-go APIs), build without it to get a working image now — the
> panel, Xray protocols, MTProto and Cloudflare Tunnel all still work:
> ```bash
> docker buildx build -f pterodactyl/Dockerfile --build-arg WG_USERSPACE=0 \
>   -t ghcr.io/iwebbio/3ax-ui-pterodactyl:latest --push .
> ```
> WireGuard/AmneziaWG inbounds simply won't start until the engine is compiled in.

## 2. Import the egg

Pterodactyl admin → **Nests → Import Egg** → upload `pterodactyl/egg-3ax-ui.json`.

## 3. Create a server

- **Allocations:** assign the **primary** allocation for the panel, **plus one
  additional high port for every inbound** you plan to run. Only ports allocated
  to the server are reachable from the internet (both TCP and UDP).
- **Variables:**
  - `PANEL_PORT` — leave blank to bind the panel to the primary allocation
    (recommended). Applied on first boot only.
  - `PANEL_USERNAME` / `PANEL_PASSWORD` — optional first-boot admin seed. If
    blank, the panel default applies — **change it immediately after login.**

## 4. First login

Start the server. When the console shows `3AX-UI online — panel listening on
port <N>`, open `http://<node-ip>:<primary-port>/` and log in.

## 5. Creating inbounds

- Pick a **port that is allocated to this server** — anything else is
  unreachable from outside.
- WireGuard/AmneziaWG use **UDP**; Pterodactyl allocations cover TCP+UDP on the
  same number, so an allocated port works for them too.
- Set the **external address** (node IP or domain) in the panel's settings so
  client links and subscription URLs resolve correctly — the container can't
  auto-detect the node's public address.

## Cloudflare Tunnel (cloudflared) — recommended

A **Cloudflare Tunnel** is the cleanest way to reach the panel on Pterodactyl: it
is outbound-only, so it needs **no inbound allocation, no public IP, no
certificate** — Cloudflare terminates TLS and gives you a real HTTPS hostname.
`cloudflared` is baked into the image and supervised by the panel (auto-restart
on drop). Two modes:

**Quick tunnel (no account, instant):** set the egg variable **`XUI_CF_ENABLE=true`**
(leave the token blank). On boot the console logs a URL like
`https://<random>.trycloudflare.com` — open it to reach the panel. Ephemeral: the
hostname changes on each restart.

**Named tunnel (your domain, persistent):** in the Cloudflare **Zero Trust →
Networks → Tunnels** dashboard, create a tunnel, add a **public hostname** routing
`panel.example.com` → `http://127.0.0.1:<panel port>`, copy the connector token,
and set the egg variable **`XUI_CF_TOKEN=<token>`** (this auto-enables token mode).

Or configure it in the panel: **Settings → Cloudflare** (enable, mode, token/target, live status + the quick-tunnel URL, Save/Restart).

Env variables (env wins over the panel settings when set):

| Variable | Meaning |
|---|---|
| `XUI_CF_ENABLE` | `true`/`false` — enable the tunnel (auto-on when a token is set) |
| `XUI_CF_TOKEN` | connector token for a named tunnel; blank ⇒ quick tunnel |
| `XUI_CF_MODE` | `quick` or `token`; blank ⇒ inferred from whether a token is set |
| `XUI_CF_TARGET` | local service to expose; defaults to the panel (`http://127.0.0.1:<port>`) |

You can also expose an **inbound** (not the panel) by pointing a named tunnel's
hostname at that inbound's local port, or by setting `XUI_CF_TARGET`.

## Subscriptions

You choose in the panel: run the subscription service on its **own dedicated
allocation** (give the server an extra port for it), or **fold it behind the
panel** port/path. Configure this under the panel's subscription settings.

## What differs from a normal 3x-ui install

- No `systemd`, no `x-ui.sh` lifecycle — Pterodactyl supervises the single
  `/app/x-ui` process; stopping the server sends `^C` (graceful shutdown).
- `fail2ban` is disabled (needs root/iptables).
- Storage lives under `/home/container` (`db/`, `log/`, `bin/`, `cert/`) via
  `XUI_DB_FOLDER` / `XUI_LOG_FOLDER` / `XUI_BIN_FOLDER`, set by the entrypoint.
- TLS: no privileged `:80`/`:443`, so ACME HTTP-01 won't work — upload certs to
  `/home/container/cert` and point the panel at them, or use DNS-01 externally.
