# Contributing

This fork's focus is running 3ax-ui on **Pterodactyl** (see the [README](README.md)
and the porting plan in [`.ai/PTERODACTYL_EGG_PLAN.md`](.ai/PTERODACTYL_EGG_PLAN.md)).
Most changes fall into one of two buckets: the **panel source** (shared with
upstream) or the **Pterodactyl packaging** under [`pterodactyl/`](pterodactyl/).

## Local development (panel)

- Create a directory named `x-ui` in the project root (used as the local data folder).
- Copy `.env.example` to `.env`.
- Run the panel:

  ```bash
  XUI_DEBUG=true go run ./main.go
  ```

  The panel defaults to `http://localhost:2053/` (user `admin`, pass `admin`).

Storage locations are environment-driven (`config/config.go`):
`XUI_DB_FOLDER`, `XUI_LOG_FOLDER`, `XUI_BIN_FOLDER`. On Pterodactyl these all
point under `/home/container`; locally they default to the `x-ui` folder / repo.

## Building & testing the Pterodactyl egg

Build the yolk image and test it against a Pterodactyl node you control:

```bash
# Build (multi-arch, push to your registry)
docker buildx build -f pterodactyl/Dockerfile \
  --platform linux/amd64,linux/arm64 \
  -t ghcr.io/iwebbio/3ax-ui-pterodactyl:latest --push .

# Or a quick single-arch local image
docker build -f pterodactyl/Dockerfile -t 3ax-ui-ptero:test .
```

Then import [`pterodactyl/egg-3ax-ui.json`](pterodactyl/egg-3ax-ui.json), create a
server with a few allocations, and verify per the checklist in the porting plan.
The [`pterodactyl/README.md`](pterodactyl/README.md) has the full operator guide.

When changing the Pterodactyl runtime contract (paths, ports, startup marker,
stop signal), keep these in sync:
- `pterodactyl/entrypoint.sh` (env + first-boot),
- `pterodactyl/egg-3ax-ui.json` (`startup.done` regex, `stop`, variables),
- the readiness marker printed in `main.go` (`3AX-UI online`).

## Conventions

- Go 1.26, `gofmt`-clean. Note the repo currently uses CRLF line endings — don't
  reformat whole files just to flip endings; keep diffs scoped to your change.
- Frontend is plain Vue 2 + Ant Design Vue (no bundler); edit assets directly.
- Prefer minimal, targeted changes to shared panel source so the fork stays easy
  to rebase on upstream [coinman-dev/3ax-ui](https://github.com/coinman-dev/3ax-ui).
