// Package config loads runtime settings from UCI (/etc/config/podkop_updater)
// and writes back persisted state (e.g. discovered emergency IPs).
package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/VizzleTF/podkop_autoupdater/go/internal/uci"
)

const (
	uciPkg            = "podkop_updater"
	uciSec            = "settings"
	defaultCheckHours = 6
)

type Config struct {
	BotToken      string
	ChatID        int64
	CheckInterval time.Duration
	EmergencyIPs  []string // space-separated UCI value, optional
}

func Load() (*Config, error) {
	token, err := uci.GetIn(uciPkg, uciSec, "bot_token")
	if err != nil || token == "" {
		return nil, fmt.Errorf("bot_token not set in UCI (%s.%s.bot_token)", uciPkg, uciSec)
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
	return &Config{
		BotToken:      token,
		ChatID:        chatID,
		CheckInterval: time.Duration(hours) * time.Hour,
		EmergencyIPs:  ips,
	}, nil
}

// SaveEmergencyIPs writes the discovered IP set to UCI for the next start.
func SaveEmergencyIPs(ips []string) error {
	key := fmt.Sprintf("%s.%s.emergency_ips", uciPkg, uciSec)
	if err := uci.Set(key, strings.Join(ips, " ")); err != nil {
		return err
	}
	return uci.Commit(uciPkg)
}
