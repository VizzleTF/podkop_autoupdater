[Русская версия](./README_ru.md)

# Podkop Updater for OpenWrt

Automatic update checker for [podkop](https://github.com/itdoginfo/podkop) on OpenWrt/ImmortalWrt routers.

## Features
- Checks latest version via GitHub API
- Three modes: manual, automatic (`--force`), Telegram confirmation (default)
- Dry-run mode for testing without changes (`--dry-run`)
- Post-update DNS check to verify podkop functionality
- Long polling for efficient Telegram response handling

## Requirements
- OpenWrt or ImmortalWrt router
- Packages: `curl`, `jq`, `wget`, `nslookup`
- Telegram bot (for confirmation mode): token from [@BotFather](https://t.me/BotFather), chat ID from [@getmyid_bot](https://t.me/getmyid_bot)

## Installation
```sh
sh <(wget -O - https://raw.githubusercontent.com/VizzleTF/podkop_autoupdater/main/install.sh)
```

The installer will guide you through mode selection and configuration.

## Usage

| Command | Description |
|---------|-------------|
| `podkop_updater.sh` | Check for updates (Telegram mode) |
| `podkop_updater.sh --force` | Auto-update without confirmation |
| `podkop_updater.sh --dry-run` | Test full flow without making changes |

In Telegram mode, reply directly to the bot's message with `yes` or `no`.

## Configuration

Edit `/usr/bin/podkop_updater.sh`:
```sh
BOT_TOKEN="your_bot_token"
CHAT_ID="your_chat_id"
```

Key timeouts (in seconds):
- `POLL_TIMEOUT=3300` — Max wait time for Telegram reply (~55 min)
- `DNS_CHECK_DELAY=60` — Delay before DNS check after update

## Troubleshooting

Check logs: `cat /tmp/podkop_update.log`

Common issues:
- **No Telegram message**: Verify `BOT_TOKEN` and `CHAT_ID`, check network access to `api.telegram.org`
- **Reply not detected**: Must reply directly to the message (use Reply function in Telegram)
- **DNS check fails**: Normal if podkop service isn't running yet

## License
[MIT](https://opensource.org/licenses/MIT)
