#!/bin/ash

# =============================================================================
# Podkop Auto-Updater Script
# Modes:
#   (default)   - Telegram mode, waits for confirmation
#   --force     - Auto update without confirmation
#   --dry-run   - Simulate full update flow without making changes
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

# =============================================================================
# Initialization
# =============================================================================

LOCK_FILE="/tmp/podkop_updater.lock"
if ! mkdir "$LOCK_FILE" 2>/dev/null; then
  echo "Another instance is already running (lock: $LOCK_FILE), exiting" >> "$LOG_FILE"
  exit 0
fi
cleanup() { rmdir "$LOCK_FILE" 2>/dev/null; rm -f "${LOG_FILE}.tmp"; }
trap cleanup EXIT

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
  exit "${3:-1}"
}

tg_api() {
  local endpoint="$1" timeout="${2:-$CURL_TIMEOUT}"; shift 2 2>/dev/null || shift
  curl -sf --max-time "$timeout" "${TELEGRAM_API_BASE}${BOT_TOKEN}/${endpoint}" "$@" 2>/dev/null
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
# Main Script
# =============================================================================

DRY_RUN=0
FORCE_MODE=0

BOT_TOKEN=$(uci -q get podkop_updater.settings.bot_token 2>/dev/null)
CHAT_ID=$(uci -q get podkop_updater.settings.chat_id 2>/dev/null)

case "${1:-}" in
  --dry-run)
    DRY_RUN=1; POLL_TIMEOUT=120; LONG_POLL_TIMEOUT=30; DNS_CHECK_DELAY=0
    echo "=== DRY RUN MODE ==="; echo "Simulating full update flow without making changes."; echo ""
    ;;
  --force) FORCE_MODE=1; log "Running in force mode" ;;
esac

rotate_log

dry_print "[Step 1] Checking Telegram configuration..."
if [ -z "$BOT_TOKEN" ] || [ -z "$CHAT_ID" ]; then
  msg="BOT_TOKEN or CHAT_ID not configured in UCI (/etc/config/podkop_updater)"
  [ "$DRY_RUN" -eq 1 ] && echo "FAILED: $msg" || log "Error: $msg"
  exit 1
fi
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
    dry_print "OK: Connected to bot @$(echo "$API_CHECK" | jq -r '.result.username')"
    log "Telegram API connection successful"

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
    if ! UPDATE_SCRIPT=$(wget -O - "$UPDATE_SCRIPT_URL" 2>>"$LOG_FILE"); then
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
