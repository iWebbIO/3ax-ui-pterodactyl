# ========================================================
# 3AX-UI — Pterodactyl "yolk" image
#
# Runs the panel as an unprivileged Pterodactyl server (uid 988), with every
# runtime binary (x-ui, xray-core, geo data, mtg/mtg-multi) BAKED IN. No root,
# no /dev/net/tun, no capabilities, no install-time downloads on the node.
#
# Build (from repo root):
#   docker buildx build -f pterodactyl/Dockerfile \
#     --platform linux/amd64,linux/arm64 \
#     -t ghcr.io/iwebbio/3ax-ui-pterodactyl:latest --push .
# ========================================================

# -------- Stage: build the panel + fetch binaries --------
FROM golang:1.26-alpine AS builder
WORKDIR /app
ARG TARGETARCH
# WG_USERSPACE=1 (default) compiles the in-process AmneziaWG/WireGuard engine
# (shared/wgengine, amneziawg-go + gVisor netstack) so tunnels run with no root,
# no /dev/net/tun and no capabilities. Build with --build-arg WG_USERSPACE=0 to
# skip it and get a guaranteed-compiling image (panel + Xray + MTProto +
# cloudflared all still work; WireGuard/AmneziaWG inbounds just won't start).
ARG WG_USERSPACE=1
RUN apk add --no-cache build-base gcc git curl unzip bash tar

COPY . .

# CGO is required for the SQLite driver; alpine => musl-linked binary that runs
# directly on the alpine runtime stage below.
ENV CGO_ENABLED=1
ENV CGO_CFLAGS="-D_LARGEFILE64_SOURCE"

# The userspace tunnel deps (amneziawg-go + gVisor, gVisor on its rolling "go"
# branch) live outside the committed go.mod; `go get` records them here where the
# network is available, then we build with the tag. Skipped when WG_USERSPACE=0.
RUN if [ "$WG_USERSPACE" = "1" ]; then \
      go get github.com/amnezia-vpn/amneziawg-go@latest gvisor.dev/gvisor@go && \
      go build -tags wg_userspace -ldflags "-w -s" -o build/x-ui main.go ; \
    else \
      go build -ldflags "-w -s" -o build/x-ui main.go ; \
    fi

# Xray-core + geo data — reuse the repo's own fetcher so filenames match exactly
# what the panel looks up at runtime (bin/xray-linux-<fname>, geo*.dat).
RUN ./DockerInit.sh "${TARGETARCH}"

# MTProto sidecars (mtg / mtg-multi). Non-fatal if unavailable for the arch.
RUN bash pterodactyl/fetch-bins.sh "${TARGETARCH}" build/bin

# Fail the build loudly if the essential binaries didn't download. DockerInit.sh /
# fetch-bins.sh are non-fatal (a transient download failure returns 0), which
# could otherwise ship a "working" image whose Xray never starts — exactly the
# kind of silent breakage that makes every inbound dead while the panel is fine.
RUN set -e; \
    echo "=== baked bin/ contents ==="; ls -la build/bin; \
    ls build/bin/xray-linux-* >/dev/null 2>&1 || { echo "FATAL: xray-core binary missing — aborting build"; exit 1; }; \
    ls build/bin/geoip.dat build/bin/geosite.dat >/dev/null 2>&1 || { echo "FATAL: geo data missing — aborting build"; exit 1; }; \
    ls build/bin/mtg-linux-* build/bin/mtg-multi-linux-* >/dev/null 2>&1 || echo "WARN: no mtg/mtg-multi — MTProto inbounds will not start"; \
    ls build/bin/cloudflared-linux-* >/dev/null 2>&1 || echo "WARN: no cloudflared — Cloudflare Tunnel disabled"

# -------- Stage: minimal unprivileged runtime --------
FROM alpine:3.20
LABEL org.opencontainers.image.title="3AX-UI (Pterodactyl)" \
      org.opencontainers.image.source="https://github.com/iWebbIO/3ax-ui-pterodactyl" \
      org.opencontainers.image.description="3AX-UI panel packaged as a Pterodactyl egg (userspace, no root)."

ENV TZ=UTC \
    XUI_ENABLE_FAIL2BAN=false

# bash/curl are used by the entrypoint; ca-certificates/tzdata by the panel.
RUN apk add --no-cache ca-certificates tzdata bash curl \
 && adduser -D -h /home/container -u 988 container

# Baked, immutable runtime assets. The entrypoint copies bin/* onto the writable
# server volume on boot; the panel manages config.json etc. there.
COPY --from=builder /app/build/x-ui /app/x-ui
COPY --from=builder /app/build/bin/ /app/bin/
COPY pterodactyl/entrypoint.sh /entrypoint.sh

RUN chmod +x /app/x-ui /entrypoint.sh \
 && chmod -R a+rX /app/bin

USER container
WORKDIR /home/container
ENTRYPOINT ["/entrypoint.sh"]
