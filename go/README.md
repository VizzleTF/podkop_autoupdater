# podkop_updater (Go)

Go rewrite of `../podkop_updater.sh`. Status: **phase 0 scaffolding**, no functional implementation yet.

See [DESIGN.md](DESIGN.md) for architecture and implementation plan.

## Build

```sh
make build          # current host
make build-all      # all OpenWrt-relevant archs
make upx            # compress with UPX (requires upx installed)
```

Binaries land in `dist/`.

## Run (after implementation)

```sh
podkop_updater --daemon       # main mode (procd init.d)
podkop_updater check          # one-shot version check
podkop_updater force-update   # update without TG confirm
podkop_updater self-update    # update the updater binary
```

## Layout

```
cmd/podkop_updater/   binary entrypoint, CLI dispatch
internal/             unexported packages (config, transport, telegram, updater, service, selfupdate, logger)
scripts/init.d/       procd service stub
.github/workflows/    cross-compile CI matrix
DESIGN.md             architecture, packages, phases
```

## Why a Go rewrite

See `../podkop_updater.sh` audit findings (problems P1–P6) — primary motivation is:
- Replace fragile `jq` shell-out pipelines with typed JSON
- Untangle transport tier-fallback logic into a single `http.RoundTripper`
- Atomic self-update with rollback support
- Enable real unit testing
- Single static binary, no `curl`/`jq`/`wget`/`nslookup` runtime dependencies

Cost: ~1.7 MB UPX-compressed binary on mipsle (vs 30 KB bash script). Acceptable on modern routers, tight on legacy 16 MB flash devices.
