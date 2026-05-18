# podkop_updater (Go source)

The runnable Go daemon for podkop_updater. End-user docs and the install
command live in the [repository root README](../README.md); this directory
is for developers.

## Build

```sh
make build           # current host
make build-all       # cross-compile for amd64, arm64, armv7, mipsle, mips
make upx             # UPX-compress (requires upx installed)
make test
```

Binaries land in `dist/`.

## Layout

```
cmd/podkop_updater/   binary entrypoint, CLI dispatch
internal/
  config/             UCI loader (bot_token, chat_id, check_interval,
                      emergency_ips)
  logger/             timestamped file log with in-place rotation
  telegram/           bot wrapper around github.com/go-telegram/bot
  transport/          three-tier RoundTripper + DoH discovery
  service/            podkop restart, DNS health check, runner
  selfupdate/         atomic binary swap with .bak rollback
  updater/            GitHub release fetch, semver compare, install.sh runner
scripts/init.d/       procd service stub (template; embedded by ../install.sh)
DESIGN.md             architecture notes and design rationale
```

The installer script lives at `../install.sh` (repository root).
The CI release workflow lives at `../.github/workflows/release.yml` —
GitHub only discovers workflows in `.github/workflows/` at the repo root.

## Release

Push an annotated tag matching `v*` from `main`:

```sh
git tag -a v0.1.3 -m "release notes"
git push origin v0.1.3
```

The workflow builds the five-arch matrix, optionally compresses with UPX,
and publishes the assets to a GitHub release.

For local end-to-end testing of the podkop-update path without downgrading
the real package, set `PODKOP_FAKE_INSTALLED` in the daemon's environment.
The value is passed through `Normalize` before comparing to the latest
GitHub release.
