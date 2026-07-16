#!/bin/ash
# 3AX-UI Pterodactyl runtime entrypoint.
# Adapts the panel to the Pterodactyl contract: unprivileged uid, only
# /home/container writable, ports come from allocations, logs to stdout.
set -e

cd /home/container || exit 1
export TZ="${TZ:-UTC}"

# --- Persistent storage: everything lives on the server volume -------------
export XUI_DB_FOLDER="/home/container/db"
export XUI_LOG_FOLDER="/home/container/log"
export XUI_BIN_FOLDER="/home/container/bin"
export XUI_ENABLE_FAIL2BAN="false"   # needs root/iptables — unavailable here

# Run AmneziaWG/WireGuard through the in-process userspace engine (no root, no
# /dev/net/tun, no capabilities). The image is built with the `wg_userspace` tag
# that compiles it in; override to "kernel" only on a privileged host.
export XUI_WG_MODE="${XUI_WG_MODE:-userspace}"

mkdir -p "$XUI_DB_FOLDER" "$XUI_LOG_FOLDER" "$XUI_BIN_FOLDER" /home/container/cert

# --- Refresh immutable binaries from the image on every boot ---------------
# Copies only the shipped assets; leaves config.json, crash logs and user data
# on the volume untouched, and picks up new versions when the image updates.
if [ -d /app/bin ]; then
  for f in /app/bin/xray-linux-* /app/bin/geoip*.dat /app/bin/geosite*.dat \
           /app/bin/mtg-linux-* /app/bin/mtg-multi-linux-*; do
    [ -e "$f" ] && cp -f "$f" "$XUI_BIN_FOLDER/" 2>/dev/null || true
  done
  chmod +x "$XUI_BIN_FOLDER"/xray-linux-* "$XUI_BIN_FOLDER"/mtg-* 2>/dev/null || true
fi

# --- First-boot provisioning (only when no database exists yet) ------------
# Never clobbers changes the operator later makes in the panel UI.
DB_FILE="$XUI_DB_FOLDER/x-ui.db"
if [ ! -f "$DB_FILE" ]; then
  # Panel must listen on the primary allocation to be reachable.
  PORT="${PANEL_PORT:-${SERVER_PORT:-2053}}"
  echo "[3ax-ui] first boot: binding panel to port ${PORT}"
  /app/x-ui setting -port "${PORT}" >/dev/null 2>&1 || true

  if [ -n "${PANEL_USERNAME:-}" ] || [ -n "${PANEL_PASSWORD:-}" ]; then
    echo "[3ax-ui] first boot: setting admin credentials"
    /app/x-ui setting -username "${PANEL_USERNAME:-admin}" \
                      -password "${PANEL_PASSWORD:-admin}" >/dev/null 2>&1 || true
  fi
fi

echo "[3ax-ui] version $(/app/x-ui -v 2>/dev/null || echo 'unknown') starting..."
echo "[3ax-ui] reminder: inbounds are only reachable on ports allocated to this server."

# Panel reads webPort/settings from the DB above and logs a '3AX-UI online'
# marker once it is listening (that line is the egg's startup-detection anchor).
exec /app/x-ui
