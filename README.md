[Русская версия](./README_ru.md)

# podkop_updater

Telegram bot for OpenWrt/ImmortalWrt routers that watches
[podkop](https://github.com/itdoginfo/podkop) for new releases and lets you
trigger updates and restarts from chat. Implemented as a single static Go
binary managed by procd.

## Features
- Persistent Telegram menu with three buttons: check podkop version, check
  updater version, restart podkop
- Inline-edit flow: every action edits the same message through busy →
  result transitions
- Periodic version check (default every 6h); when a new podkop release
  appears the bot delivers a fresh notification message
- Tiered HTTP transport: Podkop SOCKS5 → direct → emergency Telegram IPs,
  with sticky last-known-good tier
- Emergency IPs refreshed daily via DoH using podkop's configured DNS
  server; persisted in UCI so they survive reboots
- Atomic self-update with `.bak` rollback file; procd respawns into the
  new binary
- DNS health check after every restart/update polls
  `fakeip.podkop.fyi` until it resolves into podkop's fakeip range

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
uci commit podkop_updater
```

`router_label` is optional. Set it to disambiguate routers when several
daemons (each with its own bot) post into the same chat or supergroup
topic; the label is prepended in bold to every menu message. When empty,
the daemon falls back to the system hostname.

The daemon also writes the discovered emergency IP list back to
`podkop_updater.settings.emergency_ips` (space-separated).

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
