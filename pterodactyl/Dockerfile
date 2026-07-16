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
RUN apk add --no-cache build-base gcc git curl unzip bash tar

COPY . .

# Pull the userspace tunnel dependencies (amneziawg-go + gVisor netstack). These
# are only needed for the `wg_userspace` build below, so they live outside the
# committed go.mod; `go get` records them here where the network is available.
# gVisor is versioned on its rolling "go" branch.
RUN go get github.com/amnezia-vpn/amneziawg-go@latest gvisor.dev/gvisor@go

# CGO is required for the SQLite driver; alpine => musl-linked binary that runs
# directly on the alpine runtime stage below. The `wg_userspace` tag compiles the
# in-process AmneziaWG/WireGuard engine (shared/wgengine) so tunnels run with no
# root, no /dev/net/tun and no capabilities.
ENV CGO_ENABLED=1
ENV CGO_CFLAGS="-D_LARGEFILE64_SOURCE"
RUN go build -tags wg_userspace -ldflags "-w -s" -o build/x-ui main.go

# Xray-core + geo data — reuse the repo's own fetcher so filenames match exactly
# what the panel looks up at runtime (bin/xray-linux-<fname>, geo*.dat).
RUN ./DockerInit.sh "${TARGETARCH}"

# MTProto sidecars (mtg / mtg-multi). Non-fatal if unavailable for the arch.
RUN bash pterodactyl/fetch-bins.sh "${TARGETARCH}" build/bin

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
