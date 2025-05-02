#!/bin/ash

# Configuration: Specify the Telegram bot token and chat ID
# Replace with your actual values
BOT_TOKEN="your_bot_token"
CHAT_ID="your_chat_id"

# Log file for debugging
LOG_FILE="/tmp/podkop_update.log"
echo "Starting podkop update check at $(date)" > $LOG_FILE

# Step 1: Check Telegram API connectivity
curl -s "https://api.telegram.org/bot$BOT_TOKEN/getMe" > /tmp/telegram_test.json
if ! grep -q '"ok":true' /tmp/telegram_test.json; then
  echo "Error: Cannot connect to Telegram API. Check token or network." >> $LOG_FILE
  exit 1
fi
echo "Telegram API connection successful" >> $LOG_FILE

# Step 2: Fetch the latest version from GitHub
LATEST_RELEASE=$(curl -s https://api.github.com/repos/itdoginfo/podkop/releases/latest)
if [ -z "$LATEST_RELEASE" ]; then
  echo "Error: Failed to fetch GitHub release" >> $LOG_FILE
  exit 1
fi
LATEST_VERSION=$(echo $LATEST_RELEASE | jq -r '.tag_name')
LATEST_VERSION=${LATEST_VERSION#v}  # Remove "v" prefix if present
echo "Latest version: $LATEST_VERSION" >> $LOG_FILE

# Step 3: Get the installed version on the router
INSTALLED_INFO=$(opkg info podkop)
INSTALLED_VERSION=$(echo "$INSTALLED_INFO" | grep '^Version:' | cut -d' ' -f2)
INSTALLED_MAIN_VERSION=${INSTALLED_VERSION%-*}  # Remove package revision (e.g., "-1")
echo "Installed version: $INSTALLED_VERSION" >> $LOG_FILE

# Step 4: Compare versions
if [ "$(printf '%s\n' "$INSTALLED_MAIN_VERSION" "$LATEST_VERSION" | sort -V | tail -n1)" = "$LATEST_VERSION" ] && [ "$INSTALLED_MAIN_VERSION" != "$LATEST_VERSION" ]; then
  echo "New version available: $LATEST_VERSION (current: $INSTALLED_VERSION)" >> $LOG_FILE

  # Step 5: Send message to Telegram
  SEND_RESPONSE=$(curl -X POST "https://api.telegram.org/bot$BOT_TOKEN/sendMessage" -d chat_id=$CHAT_ID -d text="New version available: $LATEST_VERSION. Current: $INSTALLED_VERSION. Reply to this message with 'yes' to update or 'no' to cancel.")
  MESSAGE_ID=$(echo $SEND_RESPONSE | jq -r '.result.message_id')
  if [ -z "$MESSAGE_ID" ] || [ "$MESSAGE_ID" = "null" ]; then
    echo "Error: Failed to send Telegram message" >> $LOG_FILE
    exit 1
  fi
  echo "Sent Telegram message, ID: $MESSAGE_ID" >> $LOG_FILE

  # Step 6: Clear old updates and get initial offset
  GET_UPDATES=$(curl -s "https://api.telegram.org/bot$BOT_TOKEN/getUpdates?offset=-1")
  LAST_UPDATE_ID=$(echo $GET_UPDATES | jq -r '.result[-1].update_id // 0')
  OFFSET=$((LAST_UPDATE_ID + 1))
  echo "Initial offset: $OFFSET" >> $LOG_FILE

  # Step 7: Poll for response (yes/no)
  TIMEOUT=300  # 5 minutes in seconds
  INTERVAL=5   # Poll every 5 seconds
  START_TIME=$(date +%s)
  while [ $(( $(date +%s) - START_TIME )) -lt $TIMEOUT ]; do
    sleep $INTERVAL
    GET_UPDATES=$(curl -s "https://api.telegram.org/bot$BOT_TOKEN/getUpdates?offset=$OFFSET")
    echo "Polling updates, offset: $OFFSET" >> $LOG_FILE
    echo "Updates response: $GET_UPDATES" >> $LOG_FILE
    
    # Check for "yes" response (case-insensitive)
    YES_ID=$(echo $GET_UPDATES | jq -r --arg msgid "$MESSAGE_ID" '.result[] | select(.message.reply_to_message != null) | select(.message.reply_to_message.message_id == ($msgid | tonumber)) | select(.message.text | ascii_downcase == "yes") | .update_id')
    if [ -n "$YES_ID" ]; then
      echo "Update requested (yes response detected)" >> $LOG_FILE
      # Step 8: Run the update script with automated 'y' inputs for both prompts
      if ! command -v wget >/dev/null 2>&1; then
        echo "Error: wget not installed" >> $LOG_FILE
        exit 1
      fi
      UPDATE_SCRIPT=$(wget -O - https://raw.githubusercontent.com/itdoginfo/podkop/refs/heads/main/install.sh 2>>$LOG_FILE)
      if [ $? -ne 0 ]; then
        echo "Error: Failed to fetch update script" >> $LOG_FILE
        exit 1
      fi
      # Pipe 'y\ny\n' to answer both prompts: upgrade podkop and install Russian translation
      echo -e "y\ny\n" | sh -c "$UPDATE_SCRIPT" >> $LOG_FILE 2>&1
      if [ $? -eq 0 ]; then
        echo "Update script executed successfully" >> $LOG_FILE
      else
        echo "Error: Update script failed" >> $LOG_FILE
        exit 1
      fi
      exit 0
    fi

    # Check for "no" response (case-insensitive)
    NO_ID=$(echo $GET_UPDATES | jq -r --arg msgid "$MESSAGE_ID" '.result[] | select(.message.reply_to_message != null) | select(.message.reply_to_message.message_id == ($msgid | tonumber)) | select(.message.text | ascii_downcase == "no") | .update_id')
    if [ -n "$NO_ID" ]; then
      echo "Update declined (no response detected)" >> $LOG_FILE
      exit 0
    fi

    # Update offset for the next poll
    LAST_UPDATE_ID=$(echo $GET_UPDATES | jq -r '.result[-1].update_id // 0')
    if [ "$LAST_UPDATE_ID" != "0" ]; then
      OFFSET=$((LAST_UPDATE_ID + 1))
    fi
    echo "Updated offset: $OFFSET" >> $LOG_FILE
  done

  # If no response is received within the timeout
  echo "No response received within 5 minutes" >> $LOG_FILE
else
  echo "No new version available" >> $LOG_FILE
fi