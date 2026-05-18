// Package config loads runtime settings from UCI (/etc/config/podkop_updater)
// and writes back persisted state (e.g. discovered emergency IPs).
package config

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
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
	token, err := uciGet("bot_token")
	if err != nil || token == "" {
		return nil, fmt.Errorf("bot_token not set in UCI (%s.%s.bot_token)", uciPkg, uciSec)
	}
	chatStr, err := uciGet("chat_id")
	if err != nil || chatStr == "" {
		return nil, fmt.Errorf("chat_id not set in UCI (%s.%s.chat_id)", uciPkg, uciSec)
	}
	chatID, err := strconv.ParseInt(chatStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("chat_id is not an integer: %q", chatStr)
	}
	hours := defaultCheckHours
	if s, _ := uciGet("check_interval"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			hours = n
		}
	}
	var ips []string
	if s, _ := uciGet("emergency_ips"); s != "" {
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
	val := strings.Join(ips, " ")
	if err := exec.Command("uci", "set",
		fmt.Sprintf("%s.%s.emergency_ips=%s", uciPkg, uciSec, val)).Run(); err != nil {
		return fmt.Errorf("uci set: %w", err)
	}
	if err := exec.Command("uci", "commit", uciPkg).Run(); err != nil {
		return fmt.Errorf("uci commit: %w", err)
	}
	return nil
}

func uciGet(key string) (string, error) {
	out, err := exec.Command("uci", "-q", "get", fmt.Sprintf("%s.%s.%s", uciPkg, uciSec, key)).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return "", nil // uci -q returns 1 on missing key; treat as empty
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
