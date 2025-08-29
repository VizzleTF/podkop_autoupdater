[Русская версия (Russian version)](./README_ru.md)

# Podkop Updater for OpenWrt

This script (`podkop_updater.sh`) automates checking for updates to the `podkop` package on an OpenWrt or ImmortalWrt router. It supports three modes: manual updates via console, automatic updates without confirmation, and automatic updates with Telegram bot confirmation (default).

## Features
- Checks the latest `podkop` version via GitHub API.
- Compares with the installed version using `opkg`.
- Supports three modes:
  - **Manual**: Run via console without cron (`/usr/bin/podkop_updater.sh`).
  - **Automatic**: Run via cron without Telegram confirmation (`--force`).
  - **Telegram**: Run via cron with Telegram bot confirmation (default).
- Sends Telegram messages for confirmation, post-update success, or failure in Telegram mode.
- Automates update script prompts: upgrade `podkop` and install Russian translation.
- Performs a post-update DNS check using the `TEST_DOMAIN` from `/usr/bin/podkop` to verify `podkop` functionality.
- Logs actions to `/tmp/podkop_update.log`.
- Supports case-insensitive Telegram responses ("yes", "Yes", "YES", etc.).

## Requirements
- **OpenWrt or ImmortalWrt router** with internet access.
- **Installed packages**:
  - `curl`: For API requests (usually pre-installed).
  - `jq`: For JSON parsing (dependency of `podkop`).
  - `wget`: For downloading the update script.
  - `nslookup`: For post-update DNS check (provided by `busybox` or `bind-tools`).
- **Telegram bot** (for Telegram mode):
  - Create a bot via [@BotFather](https://t.me/BotFather) and obtain a token.
  - Get your chat ID via [@get_id_bot](https://t.me/getmyid_bot) or similar.
- **Network access** to:
  - GitHub API (`api.github.com`).
  - Telegram API (`api.telegram.org`) for Telegram mode.
  - OpenWrt/ImmortalWrt package repositories and `podkop` update script URL.

## Installation
Run the installer script with a single command:
```sh
sh <(wget -O - https://raw.githubusercontent.com/VizzleTF/podkop_autoupdater/refs/heads/main/install.sh)
```
The installer will:
- Install required packages (`curl`, `jq`, `wget`).
- Download `podkop_updater.sh` to `/usr/bin/`.
- Prompt for update mode:
  - Manual: Installs script without cron or Telegram setup.
  - Automatic: Prompts for cron frequency (in hours).
  - Telegram (default): Prompts for cron frequency, bot token, and chat ID.
- Configure the script and cron job as needed.
- Verify network access to GitHub and Telegram APIs.

## Manual Installation
1. **Save the Script**:
   ```sh
   wget -O /usr/bin/podkop_updater.sh https://raw.githubusercontent.com/VizzleTF/podkop_autoupdater/refs/heads/main/podkop_updater.sh
   chmod +x /usr/bin/podkop_updater.sh
   ```
2. **Configure Telegram** (for Telegram mode):
   - Edit the script (`vi /usr/bin/podkop_updater.sh`).
   - Replace `your_bot_token` with your Telegram bot token.
   - Replace `your_chat_id` with your Telegram chat ID.
3. **Verify Dependencies**:
   ```sh
   opkg update && opkg install curl jq wget
   ```

## Usage
1. **Manual Mode**:
   ```sh
   /usr/bin/podkop_updater.sh
   ```
   - Checks for updates and sends a Telegram message if a new version is available:
     ```
     New version available: 0.3.43. Current: 0.3.41-1. Reply to this message with 'yes' to update or 'no' to cancel.
     ```
   - Reply **directly** to the message with "yes" to update or "no" to cancel.
   - After an update attempt, receives a Telegram message with the result:
     - Success:
       ```
       Update to version 0.3.43 succeeded.
       DNS check passed: fakeip.podkop.fyi resolved to 198.18.x.x
       ```
     - Failure:
       ```
       Update to version 0.3.43 failed: Update script execution error. No DNS check performed.
       ```
     - DNS check failure due to missing TEST_DOMAIN:
       ```
       Update to version 0.3.43 succeeded.
       DNS check failed: Could not retrieve TEST_DOMAIN from /usr/bin/podkop.
       ```
   - Logs actions to `/tmp/podkop_update.log`.
2. **Automatic Mode (via cron)**:
   - Configured by the installer with `--force`:
     ```sh
     /usr/bin/podkop_updater.sh --force
     ```
   - Updates automatically without Telegram confirmation or notifications.
3. **Telegram Mode (via cron)**:
   - Configured by the installer (default).
   - Runs periodically, sends Telegram messages for confirmation, and waits 5 minutes for a response.
   - Sends a post-update Telegram message with success or failure status.

## How It Works
1. **Version Check**:
   - Fetches the latest `podkop` release from [GitHub](https://api.github.com/repos/itdoginfo/podkop/releases/latest).
   - Gets the installed version via `opkg info podkop`.
2. **Mode Handling**:
   - **Manual/Telegram**: Sends a Telegram message and waits for "yes" or "no".
   - **Automatic**: Updates immediately if `--force` is used.
3. **Update**:
   - Runs the update script (`https://raw.githubusercontent.com/itdoginfo/podkop/refs/heads/main/install.sh`).
   - Answers two prompts:
     - "Just upgrade podkop?" → `y`
     - "Need a Russian translation?" → `y`
4. **Post-Update Check** (if update succeeds):
   - Retrieves `TEST_DOMAIN` from `/usr/bin/podkop`.
   - If `TEST_DOMAIN` is not found, logs an error and, in Telegram mode, sends a notification.
   - Waits 1 minute after a successful update.
   - Runs `nslookup -timeout=2 $TEST_DOMAIN 127.0.0.42`.
   - Checks if the resolved IP is in `198.18.0.0/16` (e.g., `198.18.0.181`).
   - Logs success if the IP matches, or failure if it doesn’t.
5. **Telegram Notification** (Telegram mode only):
   - Sent after update attempt:
     - Success: Reports update success and DNS check result (or failure to retrieve `TEST_DOMAIN`).
     - Failure: Reports update failure (e.g., wget missing, script fetch failed, execution error) and notes no DNS check was performed.
   - Example messages:
     ```
     Update to version 0.3.43 succeeded.
     DNS check passed: fakeip.podkop.fyi resolved to 198.18.x.x
     ```
     ```
     Update to version 0.3.43 failed: Update script execution error. No DNS check performed.
     ```
     ```
     Update to version 0.3.43 succeeded.
     DNS check failed: Could not retrieve TEST_DOMAIN from /usr/bin/podkop.
     ```
6. **Logging**:
   - Logs actions, including post-update check and Telegram notifications, to `/tmp/podkop_update.log`.

## Troubleshooting
- **Telegram message not sent**:
  - Check `/tmp/podkop_update.log` for errors (e.g., "Cannot connect to Telegram API" or "Failed to send Telegram notification").
  - Verify `BOT_TOKEN` and `CHAT_ID`.
  - Ensure the router can reach `api.telegram.org`:
    ```sh
    ping api.telegram.org
    curl -s https://api.telegram.org/bot<your_bot_token>/getMe
    ```
- **"Yes" reply not detected**:
  - Ensure you reply **directly** to the bot’s message (click "Reply" in Telegram).
  - Check bot privacy settings in [@BotFather](https://t.me/BotFather):
    ```sh
    /setprivacy -> Select your bot -> Disable
    ```
  - Manually query Telegram API:
    ```sh
    curl -s "https://api.telegram.org/bot<your_bot_token>/getUpdates"
    ```
    Look for your "yes" reply with `reply_to_message.message_id` matching the bot’s message ID.
- **Update script fails**:
  - Test the update script manually:
    ```sh
    echo -e "y\ny\n" | sh <(wget -O - https://raw.githubusercontent.com/itdoginfo/podkop/refs/heads/main/install.sh)
    ```
  - Check `/tmp/podkop_update.log` for errors (e.g., "Failed to fetch update script", "Update script execution error").
  - Ensure `wget` is installed and the router has enough memory/storage:
    ```sh
    df -h
    free
    ```
  - In Telegram mode, expect a failure notification, e.g., "Update to version X failed: Update script execution error."
- **Post-update check fails**:
  - Verify `nslookup` is available:
    ```sh
    nslookup -timeout=2 $(grep 'TEST_DOMAIN=' /usr/bin/podkop | cut -d'"' -f2) 127.0.0.42
    ```
  - Check `/tmp/podkop_update.log` for the `nslookup` output or "Failed to retrieve TEST_DOMAIN".
  - Ensure `/usr/bin/podkop` exists and contains a valid `TEST_DOMAIN`:
    ```sh
    grep 'TEST_DOMAIN=' /usr/bin/podkop
    ```
  - Ensure `podkop` services are running and DNS is configured correctly.
  - If the IP is not in `198.18.0.0/16`, `podkop` may not be functioning properly.
- **No new version detected**:
  - Verify GitHub API access:
    ```sh
    curl -s https://api.github.com/repos/itdoginfo/podkop/releases/latest
    ```
  - Check the installed version:
    ```sh
    opkg info podkop
    ```

## Example Log
A successful Telegram mode run:
```
Starting podkop update check at Fri May 2 14:00:00 UTC 2025
Telegram API connection successful
Latest version: 0.3.43
Installed version: 0.3.41-1
New version available: 0.3.43 (current: 0.3.41-1)
Sent Telegram message, ID: 1501
Initial offset: 2
Polling updates, offset: 2
Updates response: {"ok":true,"result":[{"update_id":2,"message":{"message_id":1502,"chat":{"id":<chat_id>},"text":"yes","reply_to_message":{"message_id":1501}}}]}
Update requested (yes response detected)
[output from install.sh, including package downloads and installation]
Update script executed successfully
Retrieving TEST_DOMAIN from /usr/bin/podkop...
Waiting 1 minute before performing post-update DNS check...
Running nslookup check for fakeip.podkop.fyi...
Server:         127.0.0.42
Address:        127.0.0.42:53
Non-authoritative answer:
Name:   fakeip.podkop.fyi
Address: 198.18.0.181
Post-update check passed: fakeip.podkop.fyi resolved to 198.18.x.x (podkop is working)
Sent Telegram notification: Update to version 0.3.43 succeeded. DNS check passed: fakeip.podkop.fyi resolved to 198.18.x.x
```

A failed Telegram mode run (update script execution error):
```
Starting podkop update check at Fri May 2 14:00:00 UTC 2025
Telegram API connection successful
Latest version: 0.3.43
Installed version: 0.3.41-1
New version available: 0.3.43 (current: 0.3.41-1)
Sent Telegram message, ID: 1501
Initial offset: 2
Polling updates, offset: 2
Updates response: {"ok":true,"result":[{"update_id":2,"message":{"message_id":1502,"chat":{"id":<chat_id>},"text":"yes","reply_to_message":{"message_id":1501}}}]}
Update requested (yes response detected)
[output from install.sh, with error]
Error: Update script failed
Sent Telegram notification: Update to version 0.3.43 failed: Update script execution error. No DNS check performed.
```

A successful Telegram mode run with missing TEST_DOMAIN:
```
Starting podkop update check at Fri May 2 14:00:00 UTC 2025
Telegram API connection successful
Latest version: 0.3.43
Installed version: 0.3.41-1
New version available: 0.3.43 (current: 0.3.41-1)
Sent Telegram message, ID: 1501
Initial offset: 2
Polling updates, offset: 2
Updates response: {"ok":true,"result":[{"update_id":2,"message":{"message_id":1502,"chat":{"id":<chat_id>},"text":"yes","reply_to_message":{"message_id":1501}}}]}
Update requested (yes response detected)
[output from install.sh, including package downloads and installation]
Update script executed successfully
Retrieving TEST_DOMAIN from /usr/bin/podkop...
Error: Failed to retrieve TEST_DOMAIN from /usr/bin/podkop
Sent Telegram notification: Update to version 0.3.43 succeeded. DNS check failed: Could not retrieve TEST_DOMAIN from /usr/bin/podkop.
```

A successful automatic mode run:
```
Starting podkop update check at Fri May 2 14:00:00 UTC 2025
Running in force mode (automatic update without Telegram)
Latest version: 0.3.43
Installed version: 0.3.41-1
New version available: 0.3.43 (current: 0.3.41-1)
Proceeding with automatic update
[output from install.sh]
Update script executed successfully
Retrieving TEST_DOMAIN from /usr/bin/podkop...
Waiting 1 minute before performing post-update DNS check...
Running nslookup check for fakeip.podkop.fyi...
Server:         127.0.0.42
Address:        127.0.0.42:53
Non-authoritative answer:
Name:   fakeip.podkop.fyi
Address: 198.18.0.181
Post-update check passed: fakeip.podkop.fyi resolved to 198.18.x.x (podkop is working)
```

## License
This script is released under the [MIT License](https://opensource.org/licenses/MIT).

## Contributing
Submit issues or pull requests to improve the script. Suggestions for better error handling, additional features, or language support are welcome.

## Credits
Developed for automating `podkop` updates on OpenWrt and ImmortalWrt routers, leveraging the [podkop](https://github.com/itdoginfo/podkop) project’s installation script.