#!/bin/ash

# =============================================================================
# Podkop Auto-Updater Script
# Modes:
#   (default)   - Telegram mode, waits for confirmation
#   --force     - Auto update without confirmation
#   --dry-run   - Simulate full update flow without making changes
#   --daemon    - Persistent Telegram bot with inline menu
# =============================================================================

# Credentials — read from UCI at runtime (/etc/config/podkop_updater)
# To configure:
#   uci set podkop_updater.settings=settings
#   uci set podkop_updater.settings.bot_token="ВАШ_ТОКЕН"
#   uci set podkop_updater.settings.chat_id="ВАШ_CHAT_ID"
#   uci commit podkop_updater

# Log Settings
LOG_FILE="/tmp/podkop_update.log"
LOG_MAX_LINES=200
LOG_KEEP_LINES=100

# Timeouts
POLL_TIMEOUT=3300  # Used only in --dry-run mode; normal mode waits indefinitely (protected by lock)
DNS_CHECK_DELAY=60
DNS_TIMEOUT=2
CURL_TIMEOUT=30
LONG_POLL_TIMEOUT=60

# URLs and Paths
GITHUB_API_URL="https://api.github.com/repos/itdoginfo/podkop/releases/latest"
UPDATE_SCRIPT_URL="https://raw.githubusercontent.com/itdoginfo/podkop/refs/heads/main/install.sh"
TELEGRAM_API_BASE="https://api.telegram.org/bot"
PODKOP_CONSTANTS="/usr/lib/podkop/constants.sh"

# DNS Check Settings
FAKEIP_TEST_DOMAIN="fakeip.podkop.fyi"
DNS_SERVER="127.0.0.42"
EXPECTED_DNS_PATTERN="Address:.*198\.18\."

# Validation
VERSION_PATTERN="^[0-9]+\.[0-9]+\.[0-9]+$"

# Fallback defaults
FALLBACK_VERSION="0.0.0-1"
FALLBACK_HOSTNAME="router"
DEFAULT_CHECK_INTERVAL=6

# Transport: tier1 (Podkop SOCKS5) → tier2 (Direct) → tier3 (Emergency IPs)
TG_EMERGENCY_IPS="149.154.167.220 149.154.166.110 91.108.4.249"
SOCKS_PORT=""
SOCKS_IP=""
CURRENT_TIER=""

# Daemon state
MENU_MSG_ID=""
PENDING_LATEST_VERSION=""
UPDATE_AVAILABLE=0
OFFSET=""

# =============================================================================
# Initialization
# =============================================================================

LOCK_FILE="/tmp/podkop_updater.lock"
PID_FILE="${LOCK_FILE}/pid"

acquire_lock() {
  if mkdir "$LOCK_FILE" 2>/dev/null; then
    echo $$ > "$PID_FILE"
    return 0
  fi
  if [ -f "$PID_FILE" ]; then
    OLD_PID=$(cat "$PID_FILE" 2>/dev/null)
    if [ -n "$OLD_PID" ] && ! kill -0 "$OLD_PID" 2>/dev/null; then
      rm -rf "$LOCK_FILE"
      if mkdir "$LOCK_FILE" 2>/dev/null; then
        echo $$ > "$PID_FILE"
        return 0
      fi
    fi
  fi
  echo "Another instance is already running (lock: $LOCK_FILE), exiting" >> "$LOG_FILE"
  exit 0
}

acquire_lock

cleanup() {
  rm -rf "$LOCK_FILE" 2>/dev/null
  rm -f "${LOG_FILE}.tmp"
}
trap 'cleanup; exit 0' EXIT TERM INT

for cmd in curl jq wget nslookup; do
  command -v "$cmd" >/dev/null 2>&1 || { echo "Error: '$cmd' not found" | tee -a "$LOG_FILE"; exit 1; }
done

# =============================================================================
# Helper Functions
# =============================================================================

log() { echo "$1" >> "$LOG_FILE"; }
log_exit() { log "$1"; exit "${2:-1}"; }
dry_print() { [ "$DRY_RUN" -eq 1 ] && echo "$1"; }

rotate_log() {
  [ -f "$LOG_FILE" ] || return 0
  line_count=$(wc -l < "$LOG_FILE" 2>/dev/null || echo 0)
  if [ "$line_count" -gt "$LOG_MAX_LINES" ]; then
    tail -n "$LOG_KEEP_LINES" "$LOG_FILE" > "${LOG_FILE}.tmp" && mv "${LOG_FILE}.tmp" "$LOG_FILE"
  fi
}

# Returns true (0) if $2 is a newer version than $1
version_gt() {
  [ "$(printf '%s\n' "$1" "$2" | sort -V | tail -n1)" = "$2" ] && [ "$1" != "$2" ]
}

handle_error() {
  log "Error: $1"; dry_print "FAILED: $1"
  [ "$2" = "true" ] && [ "$FORCE_MODE" -eq 0 ] && [ "$DRY_RUN" -eq 0 ] && tg_send "Update to version ${LATEST_VERSION:-unknown} failed: $1"
  [ "$DAEMON_MODE" -eq 1 ] && return 1
  exit "${3:-1}"
}

# =============================================================================
# Transport: tier1 (Podkop SOCKS5) → tier2 (Direct) → tier3 (Emergency IPs)
# =============================================================================

detect_socks_proxy() {
  local sec m_port sb_ip lan_ip
  sec=$(uci -q get podkop.config.active_section 2>/dev/null)
  [ -z "$sec" ] && sec="main"
  m_port=$(uci -q get "podkop.${sec}.mixed_proxy_port" 2>/dev/null)
  [ -z "$m_port" ] && m_port="2080"
  SOCKS_PORT="$m_port"

  if [ -f "/etc/sing-box/config.json" ]; then
    sb_ip=$(jq -r --arg p "$m_port" \
      '.inbounds[]? | select(.listen_port==($p|tonumber)) | .listen // empty' \
      /etc/sing-box/config.json 2>/dev/null | head -n 1)
    if [ -n "$sb_ip" ]; then
      if [ "$sb_ip" = "0.0.0.0" ] || [ "$sb_ip" = "::" ]; then
        lan_ip=$(uci -q get network.lan.ipaddr 2>/dev/null)
        SOCKS_IP="${lan_ip:-127.0.0.1}"
      else
        SOCKS_IP="$sb_ip"
      fi
      return 0
    fi
  fi

  lan_ip=$(uci -q get network.lan.ipaddr 2>/dev/null)
  SOCKS_IP="${lan_ip:-127.0.0.1}"
}

# Try a single curl request, return 0 on success
_try_curl() {
  local extra_args="$1" timeout="$2" url="$3"; shift 3
  curl -sf --max-time "$timeout" $extra_args "$url" "$@" 2>/dev/null
}

# Tiered Telegram API call: SOCKS → Direct → Emergency IPs
tg_api() {
  local endpoint="$1" timeout="${2:-$CURL_TIMEOUT}" url result eip
  shift 2 2>/dev/null || shift
  url="${TELEGRAM_API_BASE}${BOT_TOKEN}/${endpoint}"

  # tier1: Podkop SOCKS5
  if [ -n "$SOCKS_IP" ] && [ -n "$SOCKS_PORT" ]; then
    if result=$(_try_curl "-x socks5h://${SOCKS_IP}:${SOCKS_PORT}" "$timeout" "$url" "$@"); then
      [ "$CURRENT_TIER" != "tier1" ] && { CURRENT_TIER="tier1"; log "Transport: using Podkop SOCKS5 (${SOCKS_IP}:${SOCKS_PORT})"; }
      echo "$result"; return 0
    fi
  fi

  # tier2: Direct
  if result=$(_try_curl "" "$timeout" "$url" "$@"); then
    [ "$CURRENT_TIER" != "tier2" ] && { CURRENT_TIER="tier2"; log "Transport: using direct connection"; }
    echo "$result"; return 0
  fi

  # tier3: Emergency hardcoded IPs
  for eip in $TG_EMERGENCY_IPS; do
    if result=$(_try_curl "--resolve api.telegram.org:443:${eip}" "$timeout" "$url" "$@"); then
      [ "$CURRENT_TIER" != "tier3_${eip}" ] && { CURRENT_TIER="tier3_${eip}"; log "Transport: using emergency IP ${eip}"; }
      echo "$result"; return 0
    fi
  done

  return 1
}

tg_send() {
  local msg prefix=""
  [ "$DRY_RUN" -eq 1 ] && prefix="[DRY RUN] "
  msg=$(printf '%b' "${prefix}$1")
  SEND_RESPONSE=$(tg_api "sendMessage" "$CURL_TIMEOUT" -X POST -d "chat_id=$CHAT_ID" --data-urlencode "text=$msg")
  [ $? -ne 0 ] && { log "Error: Failed to send Telegram notification (network error)"; return 1; }
  echo "$SEND_RESPONSE" | grep -q '"ok":true' && { log "Sent Telegram notification: $1"; return 0; }
  log "Error: Failed to send Telegram notification"; return 1
}

check_reply() {
  echo "$1" | jq -r --arg msgid "$2" '
    .result[] |
    select(.callback_query.message.message_id == ($msgid | tonumber)) |
    select(.callback_query.data == "yes" or .callback_query.data == "no") |
    [.callback_query.data, (.callback_query.id // "")] | join("|")
  ' | head -1
}

tg_answer_callback() {
  tg_api "answerCallbackQuery" "$CURL_TIMEOUT" -X POST \
    -d "callback_query_id=$1" >/dev/null 2>&1
}

# =============================================================================
# Daemon Mode Functions
# =============================================================================

# Send or edit a message with inline keyboard
send_or_edit() {
  local msg_id="$1" text keyboard
  text=$(printf '%b' "$2")
  keyboard="$3"
  local response

  if [ -n "$msg_id" ] && [ "$msg_id" != "null" ]; then
    response=$(tg_api "editMessageText" "$CURL_TIMEOUT" -X POST \
      -d "chat_id=$CHAT_ID" \
      -d "message_id=$msg_id" \
      -d "parse_mode=HTML" \
      --data-urlencode "text=$text" \
      --data-urlencode "reply_markup=$keyboard")
    if echo "$response" | grep -q '"ok":true'; then
      log "Edited message $msg_id"
      return 0
    fi
    log "Warning: editMessageText failed, sending new message"
  fi

  response=$(tg_api "sendMessage" "$CURL_TIMEOUT" -X POST \
    -d "chat_id=$CHAT_ID" \
    -d "parse_mode=HTML" \
    --data-urlencode "text=$text" \
    --data-urlencode "reply_markup=$keyboard")
  if echo "$response" | grep -q '"ok":true'; then
    MENU_MSG_ID=$(echo "$response" | jq -r '.result.message_id')
    log "Sent menu message, ID: $MENU_MSG_ID"
    return 0
  fi
  log "Error: Failed to send menu message"
  return 1
}

# Send the default menu (check version / restart)
send_default_menu() {
  local router_hostname kb text
  router_hostname=$(cat /proc/sys/kernel/hostname 2>/dev/null || echo "$FALLBACK_HOSTNAME")
  kb='{"inline_keyboard":[[{"text":"🔍 Check version","callback_data":"cmd_check"}],[{"text":"🔄 Restart podkop","callback_data":"cmd_restart"}]]}'
  text="<b>Podkop Updater</b> on <b>${router_hostname}</b>"

  if [ -n "$INSTALLED_MAIN_VERSION" ]; then
    text="${text}\nInstalled: ${INSTALLED_MAIN_VERSION}"
  fi

  send_or_edit "$MENU_MSG_ID" "$text" "$kb"
  UPDATE_AVAILABLE=0
  PENDING_LATEST_VERSION=""
}

# Send the update menu (update / cancel)
send_update_menu() {
  local router_hostname kb text
  router_hostname=$(cat /proc/sys/kernel/hostname 2>/dev/null || echo "$FALLBACK_HOSTNAME")
  kb='{"inline_keyboard":[[{"text":"✅ Update","callback_data":"cmd_update"}],[{"text":"❌ Cancel","callback_data":"cmd_cancel"}]]}'
  text="<b>New version available on ${router_hostname}:</b> ${PENDING_LATEST_VERSION}\nCurrent: ${INSTALLED_MAIN_VERSION}"

  send_or_edit "$MENU_MSG_ID" "$text" "$kb"
}

# =============================================================================
# Core Operations (extracted for reuse)
# =============================================================================

# Fetch latest version and installed version, set UPDATE_AVAILABLE
do_version_check() {
  local latest_release

  if ! latest_release=$(curl -sf --max-time "$CURL_TIMEOUT" "$GITHUB_API_URL" 2>/dev/null) || [ -z "$latest_release" ]; then
    log "Error: Failed to fetch GitHub release"
    return 1
  fi

  LATEST_VERSION=$(echo "$latest_release" | jq -r '.tag_name'); LATEST_VERSION=${LATEST_VERSION#v}
  if ! echo "$LATEST_VERSION" | grep -qE "$VERSION_PATTERN"; then
    log "Error: Invalid version format: $LATEST_VERSION"
    return 1
  fi

  INSTALLED_VERSION=$(opkg info podkop 2>/dev/null | grep '^Version:' | cut -d' ' -f2)
  [ -z "$INSTALLED_VERSION" ] && INSTALLED_VERSION=$(apk info podkop 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1)
  [ -z "$INSTALLED_VERSION" ] && INSTALLED_VERSION="$FALLBACK_VERSION"
  INSTALLED_MAIN_VERSION=${INSTALLED_VERSION%-*}; INSTALLED_MAIN_VERSION=${INSTALLED_MAIN_VERSION#v}

  if version_gt "$INSTALLED_MAIN_VERSION" "$LATEST_VERSION"; then
    UPDATE_AVAILABLE=1
    PENDING_LATEST_VERSION="$LATEST_VERSION"
    log "Update check at $(date) - New version available: $LATEST_VERSION (current: $INSTALLED_VERSION)"
  else
    UPDATE_AVAILABLE=0
    PENDING_LATEST_VERSION=""
    log "Update check at $(date) - No new version (latest: $LATEST_VERSION, installed: $INSTALLED_MAIN_VERSION)"
  fi
  return 0
}

# Run DNS check after update/restart
do_dns_check() {
  local nslookup_output dns_result
  [ -f "$PODKOP_CONSTANTS" ] && . "$PODKOP_CONSTANTS"
  log "Waiting $DNS_CHECK_DELAY seconds before DNS check..."; sleep "$DNS_CHECK_DELAY"

  nslookup_output=$(nslookup -timeout="$DNS_TIMEOUT" "$FAKEIP_TEST_DOMAIN" "$DNS_SERVER" 2>&1)
  log "$nslookup_output"

  if echo "$nslookup_output" | grep -q "$EXPECTED_DNS_PATTERN"; then
    log "Post-action check passed: podkop is working"
    DNS_CHECK_RESULT="DNS check passed: $FAKEIP_TEST_DOMAIN resolved to 198.18.x.x"
  else
    log "Post-action check failed: podkop may not be working"
    DNS_CHECK_RESULT="DNS check failed: $FAKEIP_TEST_DOMAIN did not resolve to 198.18.x.x"
  fi
}

# Download and run the podkop update script
do_update() {
  local update_script
  log "Starting update to version $PENDING_LATEST_VERSION"

  update_script=$(curl -sfL "$UPDATE_SCRIPT_URL" 2>>"$LOG_FILE") || update_script=$(wget -O - "$UPDATE_SCRIPT_URL" 2>>"$LOG_FILE")
  if [ -z "$update_script" ]; then
    handle_error "Could not fetch update script" "true"
    return 1
  fi
  if ! printf 'y\ny\n' | sh -c "$update_script" >> "$LOG_FILE" 2>&1; then
    handle_error "Update script execution error" "true"
    return 1
  fi
  log "Update script executed successfully"

  do_dns_check
  tg_send "Update to version $PENDING_LATEST_VERSION completed.\n$DNS_CHECK_RESULT"

  # Re-read installed version after update
  INSTALLED_VERSION=$(opkg info podkop 2>/dev/null | grep '^Version:' | cut -d' ' -f2)
  [ -z "$INSTALLED_VERSION" ] && INSTALLED_VERSION=$(apk info podkop 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1)
  [ -z "$INSTALLED_VERSION" ] && INSTALLED_VERSION="$FALLBACK_VERSION"
  INSTALLED_MAIN_VERSION=${INSTALLED_VERSION%-*}; INSTALLED_MAIN_VERSION=${INSTALLED_MAIN_VERSION#v}

  UPDATE_AVAILABLE=0
  PENDING_LATEST_VERSION=""
  return 0
}

# Restart podkop service
do_restart_podkop() {
  log "Restarting podkop..."
  tg_send "Restarting podkop..."

  if /etc/init.d/podkop restart >> "$LOG_FILE" 2>&1; then
    log "Podkop restarted successfully"
    do_dns_check
    tg_send "Podkop restarted.\n$DNS_CHECK_RESULT"
  else
    log "Error: Failed to restart podkop"
    tg_send "Failed to restart podkop"
  fi
}

# =============================================================================
# Daemon Mode
# =============================================================================

daemon_loop() {
  local check_interval check_interval_secs last_auto_check now
  local get_updates update_count i
  local cb_data cb_id cb_chat_id msg_text msg_chat_id

  check_interval=$(uci -q get podkop_updater.settings.check_interval 2>/dev/null)
  check_interval=${check_interval:-$DEFAULT_CHECK_INTERVAL}
  check_interval_secs=$((check_interval * 3600))

  log "Daemon started, check interval: ${check_interval}h"

  # Test Telegram API
  if ! API_CHECK=$(tg_api "getMe") || ! echo "$API_CHECK" | grep -q '"ok":true'; then
    log_exit "Error: Cannot connect to Telegram API"
  fi
  log "Telegram API connection successful via ${CURRENT_TIER}"

  # Initial version check
  do_version_check
  last_auto_check=$(date +%s)

  # Send initial menu
  if [ "$UPDATE_AVAILABLE" -eq 1 ]; then
    send_update_menu
  else
    send_default_menu
  fi

  # Get initial offset
  if ! get_updates=$(tg_api "getUpdates?offset=-1"); then
    log "Warning: Cannot get initial updates, starting from 0"
    OFFSET=0
  else
    OFFSET=$(($(echo "$get_updates" | jq -r '.result[-1].update_id // 0') + 1))
  fi
  log "Initial offset: $OFFSET"

  # Main polling loop
  while true; do
    rotate_log

    if ! get_updates=$(tg_api "getUpdates?offset=$OFFSET&timeout=$LONG_POLL_TIMEOUT" "$((LONG_POLL_TIMEOUT + 5))"); then
      log "Warning: Telegram polling failed, retrying..."
      sleep 5
      continue
    fi

    update_count=$(echo "$get_updates" | jq '.result | length')

    i=0
    while [ "$i" -lt "$update_count" ]; do
      # Parse callback queries
      cb_data=$(echo "$get_updates" | jq -r ".result[$i].callback_query.data // empty")
      cb_id=$(echo "$get_updates" | jq -r ".result[$i].callback_query.id // empty")
      cb_chat_id=$(echo "$get_updates" | jq -r ".result[$i].callback_query.message.chat.id // empty")

      # Parse text messages
      msg_text=$(echo "$get_updates" | jq -r ".result[$i].message.text // empty")
      msg_chat_id=$(echo "$get_updates" | jq -r ".result[$i].message.chat.id // empty")

      # Handle callback queries (button presses)
      if [ -n "$cb_data" ] && [ "$cb_chat_id" = "$CHAT_ID" ]; then
        [ -n "$cb_id" ] && tg_answer_callback "$cb_id"
        log "Received callback: $cb_data"

        case "$cb_data" in
          cmd_check)
            if do_version_check; then
              if [ "$UPDATE_AVAILABLE" -eq 1 ]; then
                send_update_menu
              else
                send_or_edit "$MENU_MSG_ID" "No updates available.\nInstalled: ${INSTALLED_MAIN_VERSION}\nLatest: ${LATEST_VERSION}" \
                  '{"inline_keyboard":[[{"text":"🔍 Check version","callback_data":"cmd_check"}],[{"text":"🔄 Restart podkop","callback_data":"cmd_restart"}]]}'
              fi
            else
              send_or_edit "$MENU_MSG_ID" "Failed to check for updates. Check logs." \
                '{"inline_keyboard":[[{"text":"🔍 Check version","callback_data":"cmd_check"}],[{"text":"🔄 Restart podkop","callback_data":"cmd_restart"}]]}'
            fi
            last_auto_check=$(date +%s)
            ;;
          cmd_restart)
            do_restart_podkop
            send_default_menu
            ;;
          cmd_update)
            if [ "$UPDATE_AVAILABLE" -eq 1 ] && [ -n "$PENDING_LATEST_VERSION" ]; then
              send_or_edit "$MENU_MSG_ID" "Updating to ${PENDING_LATEST_VERSION}..." \
                '{"inline_keyboard":[]}'
              do_update
              send_default_menu
            else
              send_default_menu
            fi
            ;;
          cmd_cancel)
            log "Update cancelled by user"
            send_default_menu
            ;;
        esac
      fi

      # Handle text messages (/start, /menu)
      if [ -n "$msg_text" ] && [ "$msg_chat_id" = "$CHAT_ID" ]; then
        case "$msg_text" in
          /start|/menu)
            MENU_MSG_ID=""
            if [ "$UPDATE_AVAILABLE" -eq 1 ]; then
              send_update_menu
            else
              send_default_menu
            fi
            ;;
        esac
      fi

      i=$((i + 1))
    done

    # Advance offset
    if [ "$update_count" -gt 0 ]; then
      LAST_ID=$(echo "$get_updates" | jq -r '.result[-1].update_id // 0')
      [ "$LAST_ID" != "0" ] && OFFSET=$((LAST_ID + 1))
    fi

    # Periodic auto-check
    now=$(date +%s)
    if [ $((now - last_auto_check)) -ge "$check_interval_secs" ]; then
      log "Running periodic version check"
      if do_version_check && [ "$UPDATE_AVAILABLE" -eq 1 ]; then
        send_update_menu
      fi
      last_auto_check=$now
    fi
  done
}

# =============================================================================
# Main Script
# =============================================================================

DRY_RUN=0
FORCE_MODE=0
DAEMON_MODE=0

BOT_TOKEN=$(uci -q get podkop_updater.settings.bot_token 2>/dev/null)
CHAT_ID=$(uci -q get podkop_updater.settings.chat_id 2>/dev/null)

case "${1:-}" in
  --dry-run)
    DRY_RUN=1; POLL_TIMEOUT=120; LONG_POLL_TIMEOUT=30; DNS_CHECK_DELAY=0
    echo "=== DRY RUN MODE ==="; echo "Simulating full update flow without making changes."; echo ""
    ;;
  --force) FORCE_MODE=1; log "Running in force mode" ;;
  --daemon) DAEMON_MODE=1 ;;
esac

rotate_log
detect_socks_proxy
log "SOCKS proxy: ${SOCKS_IP}:${SOCKS_PORT}"
dry_print "[Transport] Detected SOCKS proxy: ${SOCKS_IP}:${SOCKS_PORT}"

# Check Telegram configuration
if [ -z "$BOT_TOKEN" ] || [ -z "$CHAT_ID" ]; then
  msg="BOT_TOKEN or CHAT_ID not configured in UCI (/etc/config/podkop_updater)"
  [ "$DRY_RUN" -eq 1 ] && echo "FAILED: $msg" || log "Error: $msg"
  exit 1
fi

# Daemon mode — enter persistent loop
if [ "$DAEMON_MODE" -eq 1 ]; then
  daemon_loop
  exit 0
fi

# =============================================================================
# Legacy cron/one-shot mode (unchanged behavior)
# =============================================================================

dry_print "[Step 1] Checking Telegram configuration..."
dry_print "OK: Credentials configured"

dry_print ""; dry_print "[Step 2] Fetching latest version from GitHub..."
if ! LATEST_RELEASE=$(curl -sf --max-time "$CURL_TIMEOUT" "$GITHUB_API_URL" 2>/dev/null) || [ -z "$LATEST_RELEASE" ]; then
  dry_print "FAILED: Cannot fetch GitHub release"; log_exit "Error: Failed to fetch GitHub release"
fi

LATEST_VERSION=$(echo "$LATEST_RELEASE" | jq -r '.tag_name'); LATEST_VERSION=${LATEST_VERSION#v}
echo "$LATEST_VERSION" | grep -qE "$VERSION_PATTERN" || { dry_print "FAILED: Invalid version format: $LATEST_VERSION"; log_exit "Error: Invalid version format: $LATEST_VERSION"; }
dry_print "OK: Latest version is $LATEST_VERSION"

dry_print ""; dry_print "[Step 3] Getting installed version..."
INSTALLED_VERSION=$(opkg info podkop 2>/dev/null | grep '^Version:' | cut -d' ' -f2)
[ -z "$INSTALLED_VERSION" ] && INSTALLED_VERSION=$(apk info podkop 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1)
[ -z "$INSTALLED_VERSION" ] && [ "$DRY_RUN" -eq 1 ] && { dry_print "WARNING: podkop not installed, using $FALLBACK_VERSION for test"; INSTALLED_VERSION="$FALLBACK_VERSION"; }
INSTALLED_MAIN_VERSION=${INSTALLED_VERSION%-*}; INSTALLED_MAIN_VERSION=${INSTALLED_MAIN_VERSION#v}
dry_print "OK: Installed version is $INSTALLED_MAIN_VERSION"

if [ "$DRY_RUN" -eq 1 ] || version_gt "$INSTALLED_MAIN_VERSION" "$LATEST_VERSION"; then
  UPDATE_AVAILABLE=1
else
  UPDATE_AVAILABLE=0
fi

if [ "$UPDATE_AVAILABLE" -eq 1 ]; then
  log "Update check at $(date) - New version available: $LATEST_VERSION (current: $INSTALLED_VERSION)"

  if [ "$FORCE_MODE" -eq 0 ]; then
    dry_print ""; dry_print "[Step 4] Testing Telegram API connection..."
    if ! API_CHECK=$(tg_api "getMe") || ! echo "$API_CHECK" | grep -q '"ok":true'; then
      dry_print "FAILED: Cannot connect to Telegram API"; log_exit "Error: Cannot connect to Telegram API"
    fi
    dry_print "OK: Connected to bot @$(echo "$API_CHECK" | jq -r '.result.username') via ${CURRENT_TIER}"
    log "Telegram API connection successful via ${CURRENT_TIER}"

    dry_print ""; dry_print "[Step 5] Sending update notification..."
    msg_prefix=$([ "$DRY_RUN" -eq 1 ] && echo '[DRY RUN] ')
    router_hostname=$(cat /proc/sys/kernel/hostname 2>/dev/null || echo "$FALLBACK_HOSTNAME")
    reply_markup='{"inline_keyboard":[[{"text":"✅ Update","callback_data":"yes"},{"text":"❌ Cancel","callback_data":"no"}]]}'
    if ! SEND_RESPONSE=$(tg_api "sendMessage" "$CURL_TIMEOUT" -X POST \
      -d "chat_id=$CHAT_ID" \
      -d "parse_mode=HTML" \
      --data-urlencode "text=${msg_prefix}<b>New version on router ${router_hostname} available:</b> $LATEST_VERSION. Current: $INSTALLED_MAIN_VERSION. Choose an action:" \
      --data-urlencode "reply_markup=$reply_markup"); then
      dry_print "FAILED: Cannot send Telegram message (network error)"; log_exit "Error: Failed to send Telegram message (network error)"
    fi

    MESSAGE_ID=$(echo "$SEND_RESPONSE" | jq -r '.result.message_id')
    if [ -z "$MESSAGE_ID" ] || [ "$MESSAGE_ID" = "null" ]; then
      dry_print "FAILED: Cannot send Telegram message"; log_exit "Error: Failed to send Telegram message"
    fi
    dry_print "OK: Message sent, ID: $MESSAGE_ID"; log "Sent Telegram message, ID: $MESSAGE_ID"

    if ! GET_UPDATES=$(tg_api "getUpdates?offset=-1"); then
      dry_print "FAILED: Cannot get Telegram updates"; log_exit "Error: Failed to get Telegram updates"
    fi
    OFFSET=$(($(echo "$GET_UPDATES" | jq -r '.result[-1].update_id // 0') + 1)); log "Initial offset: $OFFSET"

    dry_print ""; dry_print "[Step 6] Waiting for button press (no timeout)..."
    dry_print "         (Long polling timeout: ${LONG_POLL_TIMEOUT}s)"

    USER_REPLY=""; DRY_START=$(date +%s)
    while true; do
      [ "$DRY_RUN" -eq 1 ] && printf "\r         Polling... (%ds elapsed, offset: %d)   " "$(( $(date +%s) - DRY_START ))" "$OFFSET"
      [ "$DRY_RUN" -eq 1 ] && [ "$(( $(date +%s) - DRY_START ))" -ge "$POLL_TIMEOUT" ] && break

      if ! GET_UPDATES=$(tg_api "getUpdates?offset=$OFFSET&timeout=$LONG_POLL_TIMEOUT" "$((LONG_POLL_TIMEOUT + 5))"); then
        [ "$DRY_RUN" -eq 1 ] && printf "\n         Warning: polling failed, retrying...\n"
        log "Warning: Telegram polling failed, retrying..."; sleep 5; continue
      fi
      log "Polling updates, offset: $OFFSET"

      REPLY_FULL=$(check_reply "$GET_UPDATES" "$MESSAGE_ID")
      if [ -n "$REPLY_FULL" ]; then
        USER_REPLY=$(echo "$REPLY_FULL" | cut -d'|' -f1)
        CB_ID=$(echo "$REPLY_FULL" | cut -d'|' -f2)
        [ -n "$CB_ID" ] && tg_answer_callback "$CB_ID"
        break
      fi
      LAST_ID=$(echo "$GET_UPDATES" | jq -r '.result[-1].update_id // 0'); [ "$LAST_ID" != "0" ] && OFFSET=$((LAST_ID + 1))
    done
    [ "$DRY_RUN" -eq 1 ] && echo ""

    case "$USER_REPLY" in
      yes) dry_print "OK: Received 'yes' reply"; log "Update approved" ;;
      no)  dry_print "OK: Received 'no' reply - update cancelled"; log "Update declined"
           [ "$DRY_RUN" -eq 1 ] && tg_send "Test completed: update cancelled by user"; exit 0 ;;
      *)   dry_print "TIMEOUT: No reply received within $((POLL_TIMEOUT/60)) minutes"
           log_exit "No response within $((POLL_TIMEOUT/60)) minutes" 0 ;;
    esac
  else
    log "Proceeding with automatic update"
  fi

  dry_print ""; dry_print "[Step 7] Downloading and running update script..."
  if [ "$DRY_RUN" -eq 1 ]; then
    curl -sf --max-time "$CURL_TIMEOUT" -I "$UPDATE_SCRIPT_URL" >/dev/null 2>&1 && dry_print "OK: Update script URL is accessible" || dry_print "WARNING: Update script URL may not be accessible"
    dry_print "SKIPPED: Actual download and execution (dry run)"
  else
    UPDATE_SCRIPT=$(curl -sfL "$UPDATE_SCRIPT_URL" 2>>"$LOG_FILE") || UPDATE_SCRIPT=$(wget -O - "$UPDATE_SCRIPT_URL" 2>>"$LOG_FILE")
    if [ -z "$UPDATE_SCRIPT" ]; then
      handle_error "Could not fetch update script" "true"
    fi
    if ! printf 'y\ny\n' | sh -c "$UPDATE_SCRIPT" >> "$LOG_FILE" 2>&1; then
      handle_error "Update script execution error" "true"
    fi
    log "Update script executed successfully"
  fi

  dry_print ""; dry_print "[Step 8] Running DNS check..."
  if [ "$DRY_RUN" -eq 0 ]; then
    [ -f "$PODKOP_CONSTANTS" ] && . "$PODKOP_CONSTANTS"
    log "Waiting $DNS_CHECK_DELAY seconds before DNS check..."; sleep "$DNS_CHECK_DELAY"
  else
    dry_print "SKIPPED: ${DNS_CHECK_DELAY}s delay (dry run)"
  fi

  NSLOOKUP_OUTPUT=$(nslookup -timeout="$DNS_TIMEOUT" "$FAKEIP_TEST_DOMAIN" "$DNS_SERVER" 2>&1); log "$NSLOOKUP_OUTPUT"

  if echo "$NSLOOKUP_OUTPUT" | grep -q "$EXPECTED_DNS_PATTERN"; then
    log "Post-update check passed: podkop is working"; dry_print "OK: DNS check passed - $FAKEIP_TEST_DOMAIN resolves to 198.18.x.x"
    DNS_CHECK_RESULT="DNS check passed: $FAKEIP_TEST_DOMAIN resolved to 198.18.x.x"
  else
    log "Post-update check failed: podkop may not be working"; dry_print "INFO: DNS check failed (expected if podkop not running)"
    DNS_CHECK_RESULT="DNS check failed: $FAKEIP_TEST_DOMAIN did not resolve to 198.18.x.x"
  fi

  dry_print ""; dry_print "[Step 9] Sending completion notification..."
  if [ "$FORCE_MODE" -eq 0 ]; then
    tg_send "Update to version $LATEST_VERSION completed.\n$DNS_CHECK_RESULT" && dry_print "OK: Completion message sent" || dry_print "FAILED: Could not send completion message"
  fi

  [ "$DRY_RUN" -eq 1 ] && { echo ""; echo "=== DRY RUN COMPLETED SUCCESSFULLY ==="; echo "All checks passed. The updater is configured correctly."; }
else
  log "Update check at $(date) - No new version"
fi
