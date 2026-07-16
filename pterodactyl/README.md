# 3AX-UI on Pterodactyl

Run the 3AX-UI panel as a Pterodactyl egg — fully unprivileged (no root, no
`/dev/net/tun`, no extra capabilities). Every runtime binary (panel, Xray-core,
geo data, `mtg`/`mtg-multi`) is **baked into the Docker image**; the node only
prepares the server volume at install time.

## Status

| Capability | Phase 1 (this) | Notes |
|---|---|---|
| Panel + subscriptions | ✅ | Web UI on the primary allocation |
| Xray protocols (VLESS, VMess, Trojan, Shadowsocks, SOCKS, HTTP, all transports, REALITY, XTLS) | ✅ | Userspace — work as-is |
| MTProto (`mtg` / `mtg-multi`) | ✅ | amd64/arm64 get multi-user; others single-secret |
| AmneziaWG / native WireGuard | 🚧 Phase 2 | Userspace netstack engine (`WG_MODE=userspace`) — see `.ai/PTERODACTYL_EGG_PLAN.md` |

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
  same number, so an allocated port works for them too (Phase 2).
- Set the **external address** (node IP or domain) in the panel's settings so
  client links and subscription URLs resolve correctly — the container can't
  auto-detect the node's public address.

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
