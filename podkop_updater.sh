#!/bin/ash

# Configuration: Specify the Telegram bot token and chat ID (used only in Telegram mode)
BOT_TOKEN="your_bot_token"  # Telegram bot token from @BotFather
CHAT_ID="your_chat_id"  # Telegram chat ID for notifications

# Log file for debugging
LOG_FILE="/tmp/podkop_update.log"  # Path to log file for update operations

# Constants
LOG_MAX_LINES=200  # Maximum lines in log file before rotation
LOG_KEEP_LINES=100  # Lines to keep after log rotation
POLL_TIMEOUT=300  # Telegram response timeout in seconds (5 minutes)
POLL_INTERVAL=5  # Polling interval for Telegram updates in seconds
DNS_CHECK_DELAY=60  # Delay before DNS check after update in seconds
DNS_TIMEOUT=2  # DNS lookup timeout in seconds
GITHUB_API_URL="https://api.github.com/repos/itdoginfo/podkop/releases/latest"  # GitHub API URL for latest podkop release
UPDATE_SCRIPT_URL="https://raw.githubusercontent.com/itdoginfo/podkop/refs/heads/main/install.sh"  # URL for podkop update script
TELEGRAM_API_BASE="https://api.telegram.org/bot"  # Base URL for Telegram Bot API
TEMP_TELEGRAM_FILE="/tmp/telegram_test.json"  # Temporary file for Telegram API tests
PODKOP_BINARY="/usr/bin/podkop"  # Path to podkop binary
PODKOP_CONSTANTS="/usr/lib/podkop/constants.sh"
FAKEIP_TEST_DOMAIN="fakeip.podkop.fyi"  # Default test domain, can be overridden from constants
DNS_SERVER="127.0.0.42"  # Local DNS server for testing
EXPECTED_DNS_PATTERN="Address:.*198\.18\."  # Expected DNS response pattern for podkop functionality

# Log rotation: keep only last lines if file gets too large
if [ -f "$LOG_FILE" ] && [ $(wc -l < "$LOG_FILE" 2>/dev/null || echo 0) -gt $LOG_MAX_LINES ]; then
  tail -n $LOG_KEEP_LINES "$LOG_FILE" > "${LOG_FILE}.tmp" && mv "${LOG_FILE}.tmp" "$LOG_FILE"
fi

# Functions
send_telegram_message() {
  local message="$1"
  SEND_RESPONSE=$(curl -X POST "${TELEGRAM_API_BASE}${BOT_TOKEN}/sendMessage" -d chat_id=$CHAT_ID -d text="$message")
  if echo "$SEND_RESPONSE" | grep -q '"ok":true'; then
    echo "Sent Telegram notification: $message" >> $LOG_FILE
    return 0
  else
    echo "Error: Failed to send Telegram notification" >> $LOG_FILE
    return 1
  fi
}

log_and_exit() {
  local message="$1"
  local exit_code="${2:-1}"
  echo "$message" >> $LOG_FILE
  exit $exit_code
}

# Starting update check - will log result at the end

# Step 1: Check for --force parameter (automatic mode without Telegram)
FORCE_MODE=0  # Flag for force mode (0=Telegram mode, 1=automatic mode)
if [ "$1" = "--force" ]; then
  FORCE_MODE=1
  echo "Running in force mode (automatic update without Telegram)" >> $LOG_FILE
fi

# Step 2: Fetch the latest version from GitHub
LATEST_RELEASE=$(curl -s $GITHUB_API_URL)  # JSON response from GitHub API
if [ -z "$LATEST_RELEASE" ]; then
  log_and_exit "Error: Failed to fetch GitHub release"
fi
LATEST_VERSION=$(echo $LATEST_RELEASE | jq -r '.tag_name')  # Extract version tag from JSON
LATEST_VERSION=${LATEST_VERSION#v}  # Remove "v" prefix if present

# Step 3: Get the installed version on the router
INSTALLED_INFO=$(opkg info podkop)  # Package information from opkg
INSTALLED_VERSION=$(echo "$INSTALLED_INFO" | grep '^Version:' | cut -d' ' -f2)  # Extract version string
INSTALLED_MAIN_VERSION=${INSTALLED_VERSION%-*}  # Remove package revision (e.g., "-1")
INSTALLED_MAIN_VERSION=${INSTALLED_MAIN_VERSION#v}  # Remove "v" prefix if present

# Step 4: Compare versions
if [ "$(printf '%s\n' "$INSTALLED_MAIN_VERSION" "$LATEST_VERSION" | sort -V | tail -n1)" = "$LATEST_VERSION" ] && [ "$INSTALLED_MAIN_VERSION" != "$LATEST_VERSION" ]; then
  echo "Update check at $(date) - New version available: $LATEST_VERSION (current: $INSTALLED_VERSION)" >> $LOG_FILE

  # Step 5: Handle update based on mode
  if [ $FORCE_MODE -eq 1 ]; then
    # Automatic mode: Run update without Telegram
    echo "Proceeding with automatic update" >> $LOG_FILE
  else
    # Telegram mode: Check Telegram API connectivity
    curl -s "${TELEGRAM_API_BASE}${BOT_TOKEN}/getMe" > $TEMP_TELEGRAM_FILE
    if ! grep -q '"ok":true' $TEMP_TELEGRAM_FILE; then
      log_and_exit "Error: Cannot connect to Telegram API. Check token or network."
    fi
    echo "Telegram API connection successful" >> $LOG_FILE

    # Send message to Telegram
    SEND_RESPONSE=$(curl -X POST "${TELEGRAM_API_BASE}${BOT_TOKEN}/sendMessage" -d chat_id=$CHAT_ID -d text="New version available: $LATEST_VERSION. Current: $INSTALLED_VERSION. Reply to this message with 'yes' to update or 'no' to cancel.")  # Telegram API response
    MESSAGE_ID=$(echo $SEND_RESPONSE | jq -r '.result.message_id')  # Extract message ID from response
    if [ -z "$MESSAGE_ID" ] || [ "$MESSAGE_ID" = "null" ]; then
      log_and_exit "Error: Failed to send Telegram message"
    fi
    echo "Sent Telegram message, ID: $MESSAGE_ID" >> $LOG_FILE

    # Clear old updates and get initial offset
    GET_UPDATES=$(curl -s "${TELEGRAM_API_BASE}${BOT_TOKEN}/getUpdates?offset=-1")  # Get latest updates to clear queue
    LAST_UPDATE_ID=$(echo $GET_UPDATES | jq -r '.result[-1].update_id // 0')  # Get last update ID
    OFFSET=$((LAST_UPDATE_ID + 1))  # Set offset for polling new updates
    echo "Initial offset: $OFFSET" >> $LOG_FILE

    # Poll for response (yes/no)
    START_TIME=$(date +%s)  # Record polling start time
    while [ $(( $(date +%s) - START_TIME )) -lt $POLL_TIMEOUT ]; do
      sleep $POLL_INTERVAL
      GET_UPDATES=$(curl -s "${TELEGRAM_API_BASE}${BOT_TOKEN}/getUpdates?offset=$OFFSET")  # Poll for new updates
      echo "Polling updates, offset: $OFFSET" >> $LOG_FILE
      echo "Updates response: $GET_UPDATES" >> $LOG_FILE
      
      # Check for "yes" response (case-insensitive)
      YES_ID=$(echo $GET_UPDATES | jq -r --arg msgid "$MESSAGE_ID" '.result[] | select(.message.reply_to_message != null) | select(.message.reply_to_message.message_id == ($msgid | tonumber)) | select(.message.text | ascii_downcase == "yes") | .update_id')  # Extract update ID if yes reply found
      if [ -n "$YES_ID" ]; then
        echo "Update requested (yes response detected)" >> $LOG_FILE
        break
      fi

      # Check for "no" response (case-insensitive)
      NO_ID=$(echo $GET_UPDATES | jq -r --arg msgid "$MESSAGE_ID" '.result[] | select(.message.reply_to_message != null) | select(.message.reply_to_message.message_id == ($msgid | tonumber)) | select(.message.text | ascii_downcase == "no") | .update_id')  # Extract update ID if no reply found
      if [ -n "$NO_ID" ]; then
        echo "Update declined (no response detected)" >> $LOG_FILE
        exit 0
      fi

      # Update offset for the next poll
      LAST_UPDATE_ID=$(echo $GET_UPDATES | jq -r '.result[-1].update_id // 0')  # Get ID of most recent update
      if [ "$LAST_UPDATE_ID" != "0" ]; then
        OFFSET=$((LAST_UPDATE_ID + 1))  # Set next polling offset
      fi
      echo "Updated offset: $OFFSET" >> $LOG_FILE
    done

    # Check if update was approved
    if [ -z "$YES_ID" ]; then
      log_and_exit "No response received within $((POLL_TIMEOUT/60)) minutes" 0
    fi
  fi

  # Step 6: Run the update script
  if ! command -v wget >/dev/null 2>&1; then
    echo "Error: wget not installed" >> $LOG_FILE
    if [ $FORCE_MODE -eq 0 ]; then
      send_telegram_message "Update to version $LATEST_VERSION failed: wget not installed. No DNS check performed."
    fi
    exit 1
  fi
  UPDATE_SCRIPT=$(wget -O - $UPDATE_SCRIPT_URL 2>>$LOG_FILE)  # Download update script content
  if [ $? -ne 0 ]; then
    echo "Error: Failed to fetch update script" >> $LOG_FILE
    if [ $FORCE_MODE -eq 0 ]; then
      send_telegram_message "Update to version $LATEST_VERSION failed: Could not fetch update script. No DNS check performed."
    fi
    exit 1
  fi
  # Pipe 'y\ny\n' to answer both prompts: upgrade podkop and install Russian translation
  echo -e "y\ny\n" | sh -c "$UPDATE_SCRIPT" >> $LOG_FILE 2>&1  # Execute update script with automatic responses
  UPDATE_EXIT_CODE=$?  # Capture exit code of update script execution
  if [ $UPDATE_EXIT_CODE -eq 0 ]; then
    echo "Update script executed successfully" >> $LOG_FILE
  else
    echo "Error: Update script failed" >> $LOG_FILE
    if [ $FORCE_MODE -eq 0 ]; then
      send_telegram_message "Update to version $LATEST_VERSION failed: Update script execution error. No DNS check performed."
    fi
    exit 1
  fi

  # Step 7: Post-update check after delay
  echo "Loading constants from $PODKOP_CONSTANTS..." >> $LOG_FILE
  if [ -f "$PODKOP_CONSTANTS" ]; then
    eval $(grep -v '^#' "$PODKOP_CONSTANTS" | grep '=')
  fi
  echo "Waiting $DNS_CHECK_DELAY seconds before performing post-update DNS check..." >> $LOG_FILE
  sleep $DNS_CHECK_DELAY  # Wait for podkop service to restart after update
  echo "Running nslookup check for $FAKEIP_TEST_DOMAIN..." >> $LOG_FILE
  NSLOOKUP_OUTPUT=$(nslookup -timeout=$DNS_TIMEOUT "$FAKEIP_TEST_DOMAIN" $DNS_SERVER 2>&1)  # DNS lookup output with timeout
  echo "$NSLOOKUP_OUTPUT" >> $LOG_FILE
  DNS_CHECK_RESULT=""  # Result message for DNS check
  if echo "$NSLOOKUP_OUTPUT" | grep -q "$EXPECTED_DNS_PATTERN"; then
    echo "Post-update check passed: $FAKEIP_TEST_DOMAIN resolved to 198.18.x.x (podkop is working)" >> $LOG_FILE
    DNS_CHECK_RESULT="DNS check passed: $FAKEIP_TEST_DOMAIN resolved to 198.18.x.x"  # Success message for DNS check
  else
    echo "Post-update check failed: $FAKEIP_TEST_DOMAIN did not resolve to 198.18.x.x (podkop may not be working)" >> $LOG_FILE
    DNS_CHECK_RESULT="DNS check failed: $FAKEIP_TEST_DOMAIN did not resolve to 198.18.x.x or error occurred"  # Failure message for DNS check
  fi

  # Step 8: Send Telegram notification (only in Telegram mode)
  if [ $FORCE_MODE -eq 0 ]; then
    if [ -n "$DNS_CHECK_RESULT" ]; then
      send_telegram_message "Update to version $LATEST_VERSION completed successfully.\n$DNS_CHECK_RESULT"
    else
      send_telegram_message "Update to version $LATEST_VERSION completed successfully.\nDNS check was skipped due to FAKEIP_TEST_DOMAIN retrieval failure."
    fi
  fi
else
  echo "Update check at $(date) - No new version" >> $LOG_FILE
fi