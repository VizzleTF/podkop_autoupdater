[Русская версия](./README_ru.md)

# podkop_updater

Telegram bot for OpenWrt/ImmortalWrt routers that watches
[podkop](https://github.com/itdoginfo/podkop) for new releases and lets you
trigger updates and restarts from chat. Implemented as a single static Go
binary managed by procd.

## Features
- Persistent Telegram menu: check podkop version, check updater version,
  check DNS, restart podkop, plus **status** and **log** buttons
- Inline-edit flow: every action edits the same message through busy →
  result transitions
- Slash commands mirror the buttons: `/menu`, `/check_podkop`,
  `/check_self`, `/check_dns`, `/restart`, `/status`, `/log`
- Periodic version check (default every 6h) for **both** podkop and the
  updater; a new release delivers a fresh notification message
- Optional **auto-update**: when enabled, new podkop releases install
  automatically instead of only notifying (the updater itself stays
  notify-only)
- **Access control**: restrict commands to specific Telegram user IDs;
  a **concurrency guard** prevents a double-click from launching two
  `install.sh` runs as root at once
- **Supply-chain hardening**: podkop's `install.sh` is fetched pinned to
  the detected release tag (not branch HEAD); self-update verifies the
  binary against a published `.sha256`
- Tiered HTTP transport: Podkop SOCKS5 → direct → emergency Telegram IPs,
  with a sticky last-known-good tier that periodically resets to prefer
  the primary path again
- Emergency IPs refreshed daily via DoH (concurrent queries) using
  podkop's configured DNS server; persisted in UCI so they survive reboots
- Atomic self-update with `.bak` rollback file; procd respawns into the
  new binary
- DNS health check after every restart/update polls
  `fakeip.podkop.fyi` until it resolves into podkop's fakeip range; a
  post-update failure is surfaced prominently

## Requirements
- OpenWrt or ImmortalWrt router (supported archs: amd64, arm64, armv7,
  mipsle softfloat, mips softfloat)
- Telegram bot token (from [@BotFather](https://t.me/BotFather)) and chat
  ID (from [@get_id_bot](https://t.me/get_id_bot))

## Installation

```sh
sh -c "$(curl -sfL https://raw.githubusercontent.com/VizzleTF/podkop_autoupdater/main/install.sh)"
```

The installer detects the architecture, stops any previous version, downloads
the matching binary from the latest GitHub release, configures the procd
service, and prompts for the bot token + chat ID if they aren't already in
UCI.

If the upstream podkop package ships its own `/usr/bin/podkop_bot`, stop and
disable it first — both daemons polling the same bot token will steal
updates from each other.

## Configuration

Settings live in UCI (`/etc/config/podkop_updater`):

```sh
uci set podkop_updater.settings.bot_token="YOUR_TOKEN"
uci set podkop_updater.settings.chat_id="YOUR_CHAT_ID"
uci set podkop_updater.settings.check_interval=6   # hours
uci set podkop_updater.settings.router_label="Home"  # optional: shown in message header
uci set podkop_updater.settings.admin_ids="123456789 987654321"  # optional: allowed user IDs
uci set podkop_updater.settings.auto_update=1   # optional: auto-install podkop releases
uci commit podkop_updater
```

`router_label` is optional. Set it to disambiguate routers when several
daemons (each with its own bot) post into the same chat or supergroup
topic; the label is prepended in bold to every menu message. When empty,
the daemon falls back to the system hostname.

`admin_ids` is an optional space-separated allowlist of Telegram user IDs.
When set, only those users may issue commands (every callback and slash
command is gated by `From.ID`); others get an "access denied" alert. When
empty, anyone in the configured chat may issue commands.

`auto_update` (`1`/`true`) makes the periodic check install new podkop
releases automatically instead of only notifying. The updater never
auto-updates itself, so a bad self-release can't silently brick the bot.

The daemon also writes the discovered emergency IP list back to
`podkop_updater.settings.emergency_ips` (space-separated) and tracks its
menu message id in `podkop_updater.settings.menu_mid`.

## Service

```sh
/etc/init.d/podkop_updater start
/etc/init.d/podkop_updater stop
/etc/init.d/podkop_updater restart
```

Logs: `/tmp/podkop_update.log` (rotates in place at ~200 lines).

## Build from source

```sh
cd go
make build           # current host
make build-all       # cross-compile for the five OpenWrt-relevant archs
make upx             # UPX-compress (requires upx installed)
```

See [`go/DESIGN.md`](./go/DESIGN.md) for the architectural notes.

## License
[MIT](https://opensource.org/licenses/MIT)
