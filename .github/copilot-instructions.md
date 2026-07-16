# 3AX-UI (Pterodactyl) Development Guide

## Project Overview
This repo is **3AX-UI for Pterodactyl** — a fork of [coinman-dev/3ax-ui](https://github.com/coinman-dev/3ax-ui) whose sole purpose is running the panel as an **unprivileged Pterodactyl egg** (no root, no `/dev/net/tun`, only `/home/container` writable, ports from allocations). The panel itself is a Go/Gin app with embedded static assets and a SQLite database, managing VPN/proxy inbounds, traffic, and a Telegram bot.

**Two areas of work:** (1) the shared **panel source** (`web/`, `xray/`, `awg/`, `wg/`, `mtproto/`, `sub/`, …) — keep changes minimal so the fork rebases cleanly on upstream; (2) the **Pterodactyl packaging** in `pterodactyl/`. The porting plan, decisions, and phase status live in `.ai/PTERODACTYL_EGG_PLAN.md` — read it before touching Pterodactyl-facing behavior.

> Constraint reminder: kernel-dependent features (kernel AmneziaWG/WireGuard via `awg-quick`/`wg-quick`, iptables port-forwarding, NDP-proxy IPv6, fail2ban) **do not work** in an unprivileged Pterodactyl container. AmneziaWG/WireGuard are being reimplemented as a userspace netstack engine (Phase 2). Xray protocols and MTProto work as-is.

## Architecture

### Core Components
- **main.go**: Entry point that initializes database, web server, and subscription server. Handles graceful shutdown via SIGHUP/SIGTERM signals
- **web/**: Primary web server with Gin router, HTML templates, and static assets embedded via `//go:embed`
- **xray/**: Xray-core process management and API communication for traffic monitoring
- **database/**: GORM-based SQLite database with models in `database/model/`
- **sub/**: Subscription server running alongside main web server (separate port)
- **web/service/**: Business logic layer containing InboundService, SettingService, TgBot, etc.
- **web/controller/**: HTTP handlers using Gin context (`*gin.Context`)
- **web/job/**: Cron-based background jobs for traffic monitoring, CPU checks, LDAP sync

### Key Architectural Patterns
1. **Embedded Resources**: All web assets (HTML, CSS, JS, translations) are embedded at compile time using `embed.FS`:
   - `web/assets` → `assetsFS`
   - `web/html` → `htmlFS`
   - `web/translation` → `i18nFS`

2. **Dual Server Design**: Main web panel + subscription server run concurrently, managed by `web/global` package

3. **Xray Integration**: Panel generates `config.json` for Xray binary, communicates via gRPC API for real-time traffic stats

4. **Signal-Based Restart**: SIGHUP triggers graceful restart. **Critical**: Always call `service.StopBot()` before restart to prevent Telegram bot 409 conflicts

5. **Database Seeders**: Uses `HistoryOfSeeders` model to track one-time migrations (e.g., password bcrypt migration)

## Development Workflows

### Building & Running
```bash
# Build (creates bin/3x-ui.exe)
go run tasks.json → "go: build" task

# Run with debug logging
XUI_DEBUG=true go run ./main.go
# Or use task: "go: run"

# Test
go test ./...
```

### Command-Line Operations
The main.go accepts flags for admin tasks:
- `-reset` - Reset all panel settings to defaults
- `-show` - Display current settings (port, paths)
- Use these by running the binary directly, not via web interface

### Database Management
- DB path: Configured via `config.GetDBPath()`, typically `/etc/x-ui/x-ui.db`
- Models: Located in `database/model/model.go` - Auto-migrated on startup
- Seeders: Use `HistoryOfSeeders` to prevent re-running migrations
- Default credentials: admin/admin (hashed with bcrypt)

### Telegram Bot Development
- Bot instance in `web/service/tgbot.go` (3700+ lines)
- Uses `telego` library with long polling
- **Critical Pattern**: Must call `service.StopBot()` before any server restart to prevent 409 bot conflicts
- Bot handlers use `telegohandler.BotHandler` for routing
- i18n via embedded `i18nFS` passed to bot startup

## Code Conventions

### Service Layer Pattern
Services inject dependencies (like xray.XrayAPI) and operate on GORM models:
```go
type InboundService struct {
    xrayApi xray.XrayAPI
}

func (s *InboundService) GetInbounds(userId int) ([]*model.Inbound, error) {
    // Business logic here
}
```

### Controller Pattern
Controllers use Gin context and inherit from BaseController:
```go
func (a *InboundController) getInbounds(c *gin.Context) {
    // Use I18nWeb(c, "key") for translations
    // Check auth via checkLogin middleware
}
```

### Configuration Management
- Environment vars: `XUI_DEBUG`, `XUI_LOG_LEVEL`, and the storage-path overrides `XUI_DB_FOLDER`, `XUI_LOG_FOLDER`, `XUI_BIN_FOLDER` (all defined in `config/config.go`). On Pterodactyl these point under `/home/container`; `XUI_ENABLE_FAIL2BAN=false` there.
- Config embedded files: `config/version`, `config/name`
- Use `config.GetLogLevel()`, `config.GetDBPath()`, `config.GetBinFolderPath()` helpers

### Internationalization
- Translation files: `web/translation/translate.*.toml`
- Access via `I18nWeb(c, "pages.login.loginAgain")` in controllers
- Use `locale.I18nType` enum (Web, Api, etc.)

## External Dependencies & Integration

### Xray-core
- Binary management: Download platform-specific binary (`xray-{os}-{arch}`) to bin folder
- Config generation: Panel creates `config.json` dynamically from inbound/outbound settings
- Process control: Start/stop via `xray/process.go`
- gRPC API: Real-time stats via `xray/api.go` using `google.golang.org/grpc`

### Critical External Paths
- Xray binary: `{bin_folder}/xray-{os}-{arch}`
- Xray config: `{bin_folder}/config.json`
- GeoIP/GeoSite: `{bin_folder}/geoip.dat`, `geosite.dat`
- Logs: `{log_folder}/3xipl.log`, `3xipl-banned.log`

### Job Scheduling
Uses `robfig/cron/v3` for periodic tasks:
- Traffic monitoring: `xray_traffic_job.go`
- CPU alerts: `check_cpu_usage.go`
- IP tracking: `check_client_ip_job.go`
- LDAP sync: `ldap_sync_job.go`

Jobs registered in `web/web.go` during server initialization

## Deployment & Scripts

### Installation Script Pattern
Both `install.sh` and `x-ui.sh` follow these patterns:
- Multi-distro support via `$release` variable (ubuntu, debian, centos, arch, etc.)
- Port detection with `is_port_in_use()` using ss/netstat/lsof
- Systemd service management with distro-specific unit files (`.service.debian`, `.service.arch`, `.service.rhel`)

### Docker Build (upstream, VPS)
Root `Dockerfile` — multi-stage:
1. **Builder**: CGO-enabled build, runs `DockerInit.sh` to download Xray binary
2. **Final**: Alpine-based with fail2ban pre-configured

### Pterodactyl packaging (this fork's target)
Everything Pterodactyl-specific is under `pterodactyl/`:
- `Dockerfile` — unprivileged (uid 988) Alpine/musl yolk; bakes `x-ui` + Xray + geo (`DockerInit.sh`) + `mtg`/`mtg-multi` (`fetch-bins.sh`). No node-side downloads.
- `entrypoint.sh` — sets `XUI_*` to `/home/container`, disables fail2ban, syncs baked binaries onto the volume, first-boot binds the panel to the primary allocation (`SERVER_PORT`) and seeds admin creds, then `exec`s `/app/x-ui`.
- `fetch-bins.sh` — bakes the MTProto sidecars per arch.
- `egg-3ax-ui.json` — PTDL_v2 egg: `startup` `/app/x-ui`, `startup.done` regex `3AX-UI online`, `stop` `^C`, variables `PANEL_PORT`/`PANEL_USERNAME`/`PANEL_PASSWORD`.

**Keep in sync** when changing the runtime contract: the `3AX-UI online` readiness marker in `main.go` ↔ the egg `startup.done` regex; the `stop` signal ↔ the `signal.Notify` set in `main.go` (includes `SIGINT` for `^C`).

### Key File Locations
- **Upstream / VPS:** binary `/usr/local/x-ui/`, DB `/etc/x-ui/x-ui.db`, logs `/var/log/x-ui/`, service `/etc/systemd/system/x-ui.service.*`.
- **Pterodactyl:** baked binary `/app/x-ui` + `/app/bin/`; runtime data under `/home/container/{db,log,bin,cert}` (via `XUI_DB_FOLDER`/`XUI_LOG_FOLDER`/`XUI_BIN_FOLDER`); no systemd.

## Testing & Debugging
- Set `XUI_DEBUG=true` for detailed logging
- Check Xray process: `x-ui.sh` script provides menu for status/logs
- Database inspection: Direct SQLite access to x-ui.db
- Traffic debugging: Check `3xipl.log` for IP limit tracking
- Telegram bot: Logs show bot initialization and command handling

## Common Gotchas
1. **Bot Restart**: Always stop Telegram bot before server restart to avoid 409 conflict
2. **Embedded Assets**: Changes to HTML/CSS require recompilation (not hot-reload)
3. **Password Migration**: Seeder system tracks bcrypt migration - check `HistoryOfSeeders` table
4. **Port Binding**: Subscription server uses different port from main panel
5. **Xray Binary**: Must match OS/arch exactly - managed by installer scripts
6. **Session Management**: Uses `gin-contrib/sessions` with cookie store
7. **IP Limitation**: Implements "last IP wins" - when client exceeds LimitIP, oldest connections are automatically disconnected via Xray API to allow newest IPs
