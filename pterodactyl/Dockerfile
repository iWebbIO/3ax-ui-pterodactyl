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
#     -t ghcr.io/coinman-dev/3ax-ui-pterodactyl:latest --push .
# ========================================================

# -------- Stage: build the panel + fetch binaries --------
FROM golang:1.26-alpine AS builder
WORKDIR /app
ARG TARGETARCH
RUN apk add --no-cache build-base gcc curl unzip bash tar

COPY . .

# CGO is required for the SQLite driver; alpine => musl-linked binary that runs
# directly on the alpine runtime stage below.
ENV CGO_ENABLED=1
ENV CGO_CFLAGS="-D_LARGEFILE64_SOURCE"
RUN go build -ldflags "-w -s" -o build/x-ui main.go

# Xray-core + geo data — reuse the repo's own fetcher so filenames match exactly
# what the panel looks up at runtime (bin/xray-linux-<fname>, geo*.dat).
RUN ./DockerInit.sh "${TARGETARCH}"

# MTProto sidecars (mtg / mtg-multi). Non-fatal if unavailable for the arch.
RUN bash pterodactyl/fetch-bins.sh "${TARGETARCH}" build/bin

# -------- Stage: minimal unprivileged runtime --------
FROM alpine:3.20
LABEL org.opencontainers.image.title="3AX-UI (Pterodactyl)" \
      org.opencontainers.image.source="https://github.com/coinman-dev/3ax-ui-pterodactyl" \
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
