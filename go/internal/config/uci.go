// Package config loads runtime settings from UCI (/etc/config/podkop_updater)
// and writes back persisted state (e.g. discovered emergency IPs).
package config

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/VizzleTF/podkop_autoupdater/go/internal/uci"
)

// botTokenRE matches the Telegram bot-token shape "<bot_id>:<secret>". The
// secret is base64url-ish and ~35 chars; we allow 30+ to tolerate future
// length drift while still rejecting obvious garbage early (before getMe).
var botTokenRE = regexp.MustCompile(`^[0-9]{5,}:[A-Za-z0-9_-]{30,}$`)

const (
	uciPkg            = "podkop_updater"
	uciSec            = "settings"
	defaultCheckHours = 6
)

type Config struct {
	BotToken       string
	ChatID         int64
	CheckInterval  time.Duration
	CheckHours     int      // same as CheckInterval, in whole hours (for the settings menu)
	EmergencyIPs   []string // space-separated UCI value, optional
	MenuMID        int      // tracked Telegram menu message id, 0 if absent
	RouterLabel    string   // optional human-readable router name shown in message header; empty = fall back to hostname
	AdminIDs       []int64  // space-separated Telegram user IDs allowed to issue commands; empty = anyone in the chat
	AutoUpdate     bool     // when true, periodic check auto-installs new podkop releases instead of only notifying
	AutoUpdateSelf bool     // when true, periodic check auto-installs new updater releases too
	BackupKeep     int      // how many config backups to retain (0 = unlimited)
}

func Load() (*Config, error) {
	token, err := uci.GetIn(uciPkg, uciSec, "bot_token")
	if err != nil || token == "" {
		return nil, fmt.Errorf("bot_token not set in UCI (%s.%s.bot_token)", uciPkg, uciSec)
	}
	if !botTokenRE.MatchString(token) {
		return nil, fmt.Errorf("bot_token has invalid format (expected <bot_id>:<secret>)")
	}
	chatStr, err := uci.GetIn(uciPkg, uciSec, "chat_id")
	if err != nil || chatStr == "" {
		return nil, fmt.Errorf("chat_id not set in UCI (%s.%s.chat_id)", uciPkg, uciSec)
	}
	chatID, err := strconv.ParseInt(chatStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("chat_id is not an integer: %q", chatStr)
	}
	hours := defaultCheckHours
	if s, _ := uci.GetIn(uciPkg, uciSec, "check_interval"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			hours = n
		}
	}
	var ips []string
	if s, _ := uci.GetIn(uciPkg, uciSec, "emergency_ips"); s != "" {
		ips = strings.Fields(s)
	}
	var menuMID int
	if s, _ := uci.GetIn(uciPkg, uciSec, "menu_mid"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			menuMID = n
		}
	}
	label, _ := uci.GetIn(uciPkg, uciSec, "router_label")
	var adminIDs []int64
	if s, _ := uci.GetIn(uciPkg, uciSec, "admin_ids"); s != "" {
		for _, f := range strings.Fields(s) {
			if id, err := strconv.ParseInt(f, 10, 64); err == nil && id != 0 {
				adminIDs = append(adminIDs, id)
			}
		}
	}
	var autoUpdate bool
	if s, _ := uci.GetIn(uciPkg, uciSec, "auto_update"); s == "1" || s == "true" {
		autoUpdate = true
	}
	var autoUpdateSelf bool
	if s, _ := uci.GetIn(uciPkg, uciSec, "auto_update_self"); s == "1" || s == "true" {
		autoUpdateSelf = true
	}
	var backupKeep int
	if s, _ := uci.GetIn(uciPkg, uciSec, "backup_keep"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			backupKeep = n
		}
	}
	return &Config{
		BotToken:       token,
		ChatID:         chatID,
		CheckInterval:  time.Duration(hours) * time.Hour,
		CheckHours:     hours,
		EmergencyIPs:   ips,
		MenuMID:        menuMID,
		RouterLabel:    label,
		AdminIDs:       adminIDs,
		AutoUpdate:     autoUpdate,
		AutoUpdateSelf: autoUpdateSelf,
		BackupKeep:     backupKeep,
	}, nil
}

// Settings writes individual UCI settings edited at runtime via the bot's
// settings menu. Each setter commits immediately so a reboot keeps the change.

// SaveAutoUpdate persists the podkop auto-update flag.
func SaveAutoUpdate(v bool) error {
	return setKey("auto_update", boolStr(v))
}

// SaveAutoUpdateSelf persists the updater auto-update flag.
func SaveAutoUpdateSelf(v bool) error {
	return setKey("auto_update_self", boolStr(v))
}

func boolStr(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

// SaveCheckInterval persists the version-check interval in hours.
func SaveCheckInterval(hours int) error {
	return setKey("check_interval", strconv.Itoa(hours))
}

// SaveRouterLabel persists the router label shown in message headers.
func SaveRouterLabel(label string) error {
	return setKey("router_label", label)
}

// SaveAdminIDs persists the admin allowlist (space-separated).
func SaveAdminIDs(ids []int64) error {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, strconv.FormatInt(id, 10))
	}
	return setKey("admin_ids", strings.Join(parts, " "))
}

// SaveBackupKeep persists how many config backups to retain (0 = unlimited).
func SaveBackupKeep(n int) error {
	return setKey("backup_keep", strconv.Itoa(n))
}

func setKey(key, val string) error {
	full := fmt.Sprintf("%s.%s.%s", uciPkg, uciSec, key)
	if err := uci.Set(full, val); err != nil {
		return err
	}
	return uci.Commit(uciPkg)
}

// SaveEmergencyIPs writes the discovered IP set to UCI for the next start.
func SaveEmergencyIPs(ips []string) error {
	key := fmt.Sprintf("%s.%s.emergency_ips", uciPkg, uciSec)
	if err := uci.Set(key, strings.Join(ips, " ")); err != nil {
		return err
	}
	return uci.Commit(uciPkg)
}

// SaveMenuMID persists the tracked Telegram menu message id so the daemon
// can resume editing the same message after a restart instead of posting a
// fresh one.
func SaveMenuMID(id int) error {
	key := fmt.Sprintf("%s.%s.menu_mid", uciPkg, uciSec)
	if err := uci.Set(key, strconv.Itoa(id)); err != nil {
		return err
	}
	return uci.Commit(uciPkg)
}
