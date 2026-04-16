# Changelog

## 2026-04-16 (2)

### Added
- **Daemon mode (`--daemon`)** — persistent Telegram bot with inline menu:
  - Two-button menu: "Check version" and "Restart podkop", always available in Telegram.
  - When a new version is detected, menu switches to "Update" / "Cancel".
  - Continuous long-polling loop with automatic reconnection on failure.
  - Periodic auto-check for new versions (configurable interval via UCI `check_interval`).
  - Only responds to the configured `CHAT_ID`.
- **procd init.d service** (`/etc/init.d/podkop_updater`) for daemon mode:
  - Auto-start on boot, automatic respawn on crash.
  - Install via `install.sh` mode 4 (now the default).
- **Restart podkop** button — restarts podkop service with DNS health check.
- **PID-based lock** — stale lock detection and cleanup for daemon reliability.
- **3-tier transport fallback** for Telegram API calls:
  - **tier1** — Podkop SOCKS5 proxy (auto-detected from `/etc/sing-box/config.json`, default port `2080`)
  - **tier2** — Direct connection
  - **tier3** — Emergency hardcoded Telegram IPs (`149.154.167.220`, `149.154.166.110`, `91.108.4.249`)
- Auto-detection of Podkop mixed proxy IP/port from UCI and sing-box config.

### Fixed
- **Transport layer rewrite** — added sticky route mechanism, `--connect-timeout` / `-k` flags, and proper 3-arg `tg_api` calls. Fixes buttons not responding after the first interaction and constant fallback to emergency IPs.
- **Check version button** — added timestamp to response text so repeated presses always update the message (avoids Telegram "message is not modified" error).

### Changed
- Refactored main flow into reusable functions (`do_version_check`, `do_update`, `do_restart_podkop`, `do_dns_check`).
- `install.sh` default mode changed from cron (mode 3) to daemon (mode 4).
- `install.sh` option 3 ("Update script, keep config") now restarts daemon automatically.
- `install.sh` uses `curl` as primary downloader with `wget` fallback (OpenWrt 25.x ships wget without HTTPS).

---

## 2026-04-16

### Changed
- **UCI-based credentials** — `BOT_TOKEN` and `CHAT_ID` are no longer hardcoded in the script.
  They are now stored in `/etc/config/podkop_updater` and read at runtime via `uci -q get`.
  Configuration:
  ```sh
  uci set podkop_updater.settings=settings
  uci set podkop_updater.settings.bot_token="TOKEN"
  uci set podkop_updater.settings.chat_id="CHAT_ID"
  uci commit podkop_updater
  ```
- **`install.sh`** — replaced `sed`-based token injection with `uci set` / `uci commit`.
  Existing config detection now uses `uci -q get` instead of `grep` on the script file.
  Updating the script (option 3) no longer requires touching credentials — UCI config is preserved automatically.

### Fixed (ASH/POSIX compatibility)
- Replaced `echo -e` with `printf` — `echo -e` is not POSIX and behaves inconsistently in ASH/BusyBox.
- Fixed `||` / `&&` operator precedence ambiguity in credentials check — replaced with explicit `if/fi` block.
- Replaced `VAR=$(cmd); [ $? -ne 0 ]` pattern with `if ! VAR=$(cmd)` throughout — idiomatic POSIX, avoids `$?` clobbering.
- Fixed unquoted variables inside `[ ]` — added quotes to prevent word splitting.
- `case "$1"` → `case "${1:-}"` — prevents unbound variable error when script is called without arguments.

### Refactored
- Extracted log rotation into `rotate_log` function — replaces fragile `&&`-chained one-liner.
- Extracted version comparison into `version_gt` function — intent is now explicit at the call site.
- Renamed local variables `_prefix` / `_hostname` / `_kb` to `msg_prefix` / `router_hostname` / `reply_markup`.
- Added `FALLBACK_VERSION` and `FALLBACK_HOSTNAME` constants — eliminates magic strings `"0.0.0-1"` and `"router"`.
- `handle_error` now uses `${LATEST_VERSION:-unknown}` — safe if called before version is fetched.

---

## 2026-01-27

### Added
- `--dry-run` mode: simulates the full update flow (Telegram polling, DNS check) without making changes.

### Changed
- Removed `--test-telegram` mode in favour of `--dry-run`.
- Streamlined script logic; improved error handling and robustness throughout.

### Docs
- Simplified and condensed README files.
- Added usage instructions for `--dry-run` and `--force` to install output.

---

## 2026-01-26

### Added
- Telegram connection test mode.
- URL-encoded Telegram messages to support special characters.

---

## 2025-09-15

### Fixed
- Use `.` (source) instead of `eval` to load `constants.sh`.
- Improved post-update DNS check reliability.
- Fixed `constants.sh` path.

---

## 2025-08-29

### Changed
- Modular refactor of `install.sh` with dedicated functions and improved error handling.
- Improved log rotation and added timestamps to log messages.
- Prompt for Telegram settings interactively if not configured.

---

## 2025-05-07

### Added
- Post-update DNS check and Telegram notification on completion.
- ImmortalWrt support in `install.sh`.

---

## 2025-05-02

### Added
- Russian README (`README.ru.md`).
- Support for multiple update modes (manual / auto / Telegram-confirmed) in `install.sh`.
