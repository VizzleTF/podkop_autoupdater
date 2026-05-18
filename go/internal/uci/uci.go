// Package uci wraps the OpenWrt `uci` CLI for reading and writing
// configuration values. All functions shell out to /sbin/uci.
package uci

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Get returns the value of an arbitrary UCI key (e.g. "podkop.settings.dns_type").
// Missing keys return ("", nil); only unexpected failures return a non-nil error.
func Get(key string) (string, error) {
	out, err := exec.Command("uci", "-q", "get", key).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return "", nil // uci -q returns 1 on missing key; treat as empty
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// GetIn is a typed shortcut for Get(pkg.section.key).
func GetIn(pkg, section, key string) (string, error) {
	return Get(fmt.Sprintf("%s.%s.%s", pkg, section, key))
}

// Set runs `uci set key=value`.
func Set(key, value string) error {
	if err := exec.Command("uci", "set", fmt.Sprintf("%s=%s", key, value)).Run(); err != nil {
		return fmt.Errorf("uci set: %w", err)
	}
	return nil
}

// Commit runs `uci commit pkg`, persisting staged changes.
func Commit(pkg string) error {
	if err := exec.Command("uci", "commit", pkg).Run(); err != nil {
		return fmt.Errorf("uci commit: %w", err)
	}
	return nil
}
