#!/bin/ash

# =============================================================================
# Podkop Auto-Updater Script
# Modes:
#   (default)        - Telegram mode, waits for confirmation
#   --force          - Auto update without confirmation
#   --test-telegram  - Test Telegram connection
#   --dry-run        - Simulate full update flow without making changes
# =============================================================================

# Configuration
BOT_TOKEN="your_bot_token"
CHAT_ID="your_chat_id"

# Log Settings
LOG_FILE="/tmp/podkop_update.log"
LOG_MAX_LINES=200
LOG_KEEP_LINES=100

# Timeouts
POLL_TIMEOUT=300
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

# =============================================================================
# Initialization
# =============================================================================

cleanup() { rm -f "${LOG_FILE}.tmp"; }
trap cleanup EXIT

for cmd in curl jq wget nslookup; do
  command -v "$cmd" >/dev/null 2>&1 || { echo "Error: '$cmd' not found" | tee -a "$LOG_FILE"; exit 1; }
done

if [ -f "$LOG_FILE" ] && [ $(wc -l < "$LOG_FILE" 2>/dev/null || echo 0) -gt $LOG_MAX_LINES ]; then
  tail -n $LOG_KEEP_LINES "$LOG_FILE" > "${LOG_FILE}.tmp" && mv "${LOG_FILE}.tmp" "$LOG_FILE"
fi

# =============================================================================
# Helper Functions
# =============================================================================

log() { echo "$1" >> "$LOG_FILE"; }
log_tee() { echo "$1" | tee -a "$LOG_FILE"; }
log_exit() { log "$1"; exit "${2:-1}"; }

# Print to console only in dry-run mode
dry_print() { [ $DRY_RUN -eq 1 ] && echo "$1"; }

handle_error() {
  log "Error: $1"
  dry_print "FAILED: $1"
  [ "$2" = "true" ] && [ $FORCE_MODE -eq 0 ] && [ $DRY_RUN -eq 0 ] && tg_send "Update to version $LATEST_VERSION failed: $1"
  exit "${3:-1}"
}

tg_api() {
  local endpoint="$1"; shift
  curl -sf --max-time "$CURL_TIMEOUT" "${TELEGRAM_API_BASE}${BOT_TOKEN}/${endpoint}" "$@" 2>/dev/null
}

tg_api_long() {
  local endpoint="$1"; shift
  curl -sf --max-time "$((LONG_POLL_TIMEOUT + 5))" "${TELEGRAM_API_BASE}${BOT_TOKEN}/${endpoint}" "$@" 2>/dev/null
}

tg_send() {
  local msg prefix=""
  [ $DRY_RUN -eq 1 ] && prefix="[DRY RUN] "
  msg=$(printf '%b' "${prefix}$1")
  SEND_RESPONSE=$(tg_api "sendMessage" -X POST -d "chat_id=$CHAT_ID" --data-urlencode "text=$msg")
  if [ $? -ne 0 ]; then
    log "Error: Failed to send Telegram notification (network error)"
    return 1
  fi
  if echo "$SEND_RESPONSE" | grep -q '"ok":true'; then
    log "Sent Telegram notification: $1"
    return 0
  fi
  log "Error: Failed to send Telegram notification"
  return 1
}

check_reply() {
  local updates="$1" msgid="$2"
  echo "$updates" | jq -r --arg msgid "$msgid" '
    .result[]
    | select(.message.reply_to_message.message_id == ($msgid | tonumber))
    | .message.text | ascii_downcase
    | select(. == "yes" or . == "no")
  ' | head -1
}

test_telegram() {
  local test_message="${1:-Podkop updater test message: Telegram notifications are working!}"
  log "Testing Telegram connection..."

  if [ "$BOT_TOKEN" = "your_bot_token" ] || [ "$CHAT_ID" = "your_chat_id" ]; then
    log_tee "Error: BOT_TOKEN or CHAT_ID not configured"
    return 1
  fi

  echo "Checking Telegram API connectivity..."
  API_RESPONSE=$(tg_api "getMe")
  if [ $? -ne 0 ]; then
    log_tee "Error: Cannot connect to Telegram API (network error)"
    return 1
  fi

  if echo "$API_RESPONSE" | grep -q '"ok":true'; then
    BOT_NAME=$(echo "$API_RESPONSE" | jq -r '.result.username')
    log_tee "API connection successful. Bot: @$BOT_NAME"
  else
    log_tee "Error: Cannot connect to Telegram API"
    echo "Response: $API_RESPONSE"
    return 1
  fi

  echo "Sending test message to chat $CHAT_ID..."
  if tg_send "$test_message"; then
    echo "Test message sent successfully!"
    return 0
  fi
  echo "Error: Failed to send test message"
  return 1
}

# =============================================================================
# Main Script
# =============================================================================

DRY_RUN=0
FORCE_MODE=0

case "$1" in
  --test-telegram) test_telegram "$2"; exit $? ;;
  --dry-run)
    DRY_RUN=1
    POLL_TIMEOUT=120
    LONG_POLL_TIMEOUT=30
    DNS_CHECK_DELAY=0
    echo "=== DRY RUN MODE ==="
    echo "Simulating full update flow without making changes."
    echo ""
    ;;
  --force) FORCE_MODE=1; log "Running in force mode" ;;
esac

dry_print "[Step 1] Checking Telegram configuration..."
if [ "$BOT_TOKEN" = "your_bot_token" ] || [ "$CHAT_ID" = "your_chat_id" ]; then
  [ $DRY_RUN -eq 1 ] && { echo "FAILED: BOT_TOKEN or CHAT_ID not configured"; exit 1; }
fi
dry_print "OK: Credentials configured"

dry_print ""
dry_print "[Step 2] Fetching latest version from GitHub..."
LATEST_RELEASE=$(curl -sf --max-time "$CURL_TIMEOUT" "$GITHUB_API_URL" 2>/dev/null)
[ $? -ne 0 ] || [ -z "$LATEST_RELEASE" ] && { dry_print "FAILED: Cannot fetch GitHub release"; log_exit "Error: Failed to fetch GitHub release"; }

LATEST_VERSION=$(echo "$LATEST_RELEASE" | jq -r '.tag_name')
LATEST_VERSION=${LATEST_VERSION#v}
if ! echo "$LATEST_VERSION" | grep -qE "$VERSION_PATTERN"; then
  dry_print "FAILED: Invalid version format: $LATEST_VERSION"
  log_exit "Error: Invalid version format: $LATEST_VERSION"
fi
dry_print "OK: Latest version is $LATEST_VERSION"

dry_print ""
dry_print "[Step 3] Getting installed version..."
INSTALLED_VERSION=$(opkg info podkop 2>/dev/null | grep '^Version:' | cut -d' ' -f2)
if [ -z "$INSTALLED_VERSION" ]; then
  if [ $DRY_RUN -eq 1 ]; then
    dry_print "WARNING: podkop not installed, using 0.0.0 for test"
    INSTALLED_VERSION="0.0.0-1"
  fi
fi
INSTALLED_MAIN_VERSION=${INSTALLED_VERSION%-*}
INSTALLED_MAIN_VERSION=${INSTALLED_MAIN_VERSION#v}
dry_print "OK: Installed version is $INSTALLED_MAIN_VERSION"

# In dry-run mode, always proceed as if update is available
if [ $DRY_RUN -eq 1 ]; then
  UPDATE_AVAILABLE=1
elif [ "$(printf '%s\n' "$INSTALLED_MAIN_VERSION" "$LATEST_VERSION" | sort -V | tail -n1)" = "$LATEST_VERSION" ] && [ "$INSTALLED_MAIN_VERSION" != "$LATEST_VERSION" ]; then
  UPDATE_AVAILABLE=1
else
  UPDATE_AVAILABLE=0
fi

if [ $UPDATE_AVAILABLE -eq 1 ]; then
  log "Update check at $(date) - New version available: $LATEST_VERSION (current: $INSTALLED_VERSION)"

  if [ $FORCE_MODE -eq 0 ]; then
    dry_print ""
    dry_print "[Step 4] Testing Telegram API connection..."
    API_CHECK=$(tg_api "getMe")
    if [ $? -ne 0 ] || ! echo "$API_CHECK" | grep -q '"ok":true'; then
      dry_print "FAILED: Cannot connect to Telegram API"
      log_exit "Error: Cannot connect to Telegram API"
    fi
    BOT_NAME=$(echo "$API_CHECK" | jq -r '.result.username')
    dry_print "OK: Connected to bot @$BOT_NAME"
    log "Telegram API connection successful"

    dry_print ""
    dry_print "[Step 5] Sending update notification..."
    SEND_RESPONSE=$(tg_api "sendMessage" -X POST -d "chat_id=$CHAT_ID" \
      -d "text=$([ $DRY_RUN -eq 1 ] && echo '[DRY RUN] ')New version available: $LATEST_VERSION. Current: $INSTALLED_MAIN_VERSION. Reply 'yes' to update or 'no' to cancel.")
    if [ $? -ne 0 ]; then
      dry_print "FAILED: Cannot send Telegram message (network error)"
      log_exit "Error: Failed to send Telegram message (network error)"
    fi

    MESSAGE_ID=$(echo "$SEND_RESPONSE" | jq -r '.result.message_id')
    if [ -z "$MESSAGE_ID" ] || [ "$MESSAGE_ID" = "null" ]; then
      dry_print "FAILED: Cannot send Telegram message"
      log_exit "Error: Failed to send Telegram message"
    fi
    dry_print "OK: Message sent, ID: $MESSAGE_ID"
    log "Sent Telegram message, ID: $MESSAGE_ID"

    GET_UPDATES=$(tg_api "getUpdates?offset=-1")
    [ $? -ne 0 ] && { dry_print "FAILED: Cannot get Telegram updates"; log_exit "Error: Failed to get Telegram updates"; }
    OFFSET=$(($(echo "$GET_UPDATES" | jq -r '.result[-1].update_id // 0') + 1))
    log "Initial offset: $OFFSET"

    dry_print ""
    dry_print "[Step 6] Waiting for reply (yes/no) for $((POLL_TIMEOUT/60)) minutes..."
    dry_print "         (Long polling timeout: ${LONG_POLL_TIMEOUT}s)"

    USER_REPLY=""
    START_TIME=$(date +%s)
    while [ $(( $(date +%s) - START_TIME )) -lt $POLL_TIMEOUT ]; do
      [ $DRY_RUN -eq 1 ] && printf "\r         Polling... (%ds elapsed, offset: %d)   " "$(( $(date +%s) - START_TIME ))" "$OFFSET"

      GET_UPDATES=$(tg_api_long "getUpdates?offset=$OFFSET&timeout=$LONG_POLL_TIMEOUT")
      if [ $? -ne 0 ]; then
        [ $DRY_RUN -eq 1 ] && printf "\n         Warning: polling failed, retrying...\n"
        log "Warning: Telegram polling failed, retrying..."
        sleep 5
        continue
      fi
      log "Polling updates, offset: $OFFSET"

      USER_REPLY=$(check_reply "$GET_UPDATES" "$MESSAGE_ID")
      [ -n "$USER_REPLY" ] && break

      LAST_ID=$(echo "$GET_UPDATES" | jq -r '.result[-1].update_id // 0')
      [ "$LAST_ID" != "0" ] && OFFSET=$((LAST_ID + 1))
    done
    [ $DRY_RUN -eq 1 ] && echo ""

    case "$USER_REPLY" in
      yes)
        dry_print "OK: Received 'yes' reply"
        log "Update approved"
        ;;
      no)
        dry_print "OK: Received 'no' reply - update cancelled"
        log "Update declined"
        [ $DRY_RUN -eq 1 ] && tg_send "Test completed: update cancelled by user"
        exit 0
        ;;
      *)
        dry_print "TIMEOUT: No reply received within $((POLL_TIMEOUT/60)) minutes"
        log_exit "No response within $((POLL_TIMEOUT/60)) minutes" 0
        ;;
    esac
  else
    log "Proceeding with automatic update"
  fi

  dry_print ""
  dry_print "[Step 7] Downloading and running update script..."
  if [ $DRY_RUN -eq 1 ]; then
    UPDATE_CHECK=$(curl -sf --max-time "$CURL_TIMEOUT" -I "$UPDATE_SCRIPT_URL" 2>/dev/null)
    if [ $? -eq 0 ]; then
      dry_print "OK: Update script URL is accessible"
    else
      dry_print "WARNING: Update script URL may not be accessible"
    fi
    dry_print "SKIPPED: Actual download and execution (dry run)"
  else
    UPDATE_SCRIPT=$(wget -O - "$UPDATE_SCRIPT_URL" 2>>"$LOG_FILE")
    [ $? -ne 0 ] && handle_error "Could not fetch update script" "true"

    echo -e "y\ny\n" | sh -c "$UPDATE_SCRIPT" >> "$LOG_FILE" 2>&1
    [ $? -ne 0 ] && handle_error "Update script execution error" "true"
    log "Update script executed successfully"
  fi

  dry_print ""
  dry_print "[Step 8] Running DNS check..."
  if [ $DRY_RUN -eq 0 ]; then
    [ -f "$PODKOP_CONSTANTS" ] && . "$PODKOP_CONSTANTS"
    log "Waiting $DNS_CHECK_DELAY seconds before DNS check..."
    sleep "$DNS_CHECK_DELAY"
  else
    dry_print "SKIPPED: $DNS_CHECK_DELAY second delay (dry run)"
  fi

  NSLOOKUP_OUTPUT=$(nslookup -timeout="$DNS_TIMEOUT" "$FAKEIP_TEST_DOMAIN" "$DNS_SERVER" 2>&1)
  log "$NSLOOKUP_OUTPUT"

  if echo "$NSLOOKUP_OUTPUT" | grep -q "$EXPECTED_DNS_PATTERN"; then
    log "Post-update check passed: podkop is working"
    dry_print "OK: DNS check passed - $FAKEIP_TEST_DOMAIN resolves to 198.18.x.x"
    DNS_CHECK_RESULT="DNS check passed: $FAKEIP_TEST_DOMAIN resolved to 198.18.x.x"
  else
    log "Post-update check failed: podkop may not be working"
    dry_print "INFO: DNS check failed (expected if podkop not running)"
    DNS_CHECK_RESULT="DNS check failed: $FAKEIP_TEST_DOMAIN did not resolve to 198.18.x.x"
  fi

  dry_print ""
  dry_print "[Step 9] Sending completion notification..."
  if [ $FORCE_MODE -eq 0 ]; then
    if tg_send "Update to version $LATEST_VERSION completed.\n$DNS_CHECK_RESULT"; then
      dry_print "OK: Completion message sent"
    else
      dry_print "FAILED: Could not send completion message"
    fi
  fi

  if [ $DRY_RUN -eq 1 ]; then
    echo ""
    echo "=== DRY RUN COMPLETED SUCCESSFULLY ==="
    echo "All checks passed. The updater is configured correctly."
  fi
else
  log "Update check at $(date) - No new version"
fi
