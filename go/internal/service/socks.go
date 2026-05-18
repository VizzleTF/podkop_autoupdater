package service

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

const (
	defaultMixedPort = "2080"
	singBoxConfig    = "/etc/sing-box/config.json"
)

// DetectSocksAddr returns the SOCKS5 endpoint exposed by podkop, or an empty
// string if podkop does not advertise a SOCKS-compatible inbound on this
// system. Returning "" lets the caller skip the SOCKS5 tier entirely
// instead of waiting on a refused/black-holed connection.
func DetectSocksAddr() string {
	activeSec := uciGet("podkop.config.active_section")
	if activeSec == "" {
		activeSec = "main"
	}
	port := uciGet(fmt.Sprintf("podkop.%s.mixed_proxy_port", activeSec))
	if port == "" {
		port = defaultMixedPort
	}

	ip, found := singBoxMixedListenIP(port)
	if !found {
		// No mixed/socks inbound on this podkop config. Disable the SOCKS tier.
		return ""
	}
	if ip == "0.0.0.0" || ip == "::" {
		ip = lanIP()
	}
	if ip == "" {
		ip = "127.0.0.1"
	}
	return ip + ":" + port
}

// singBoxMixedListenIP looks for a SOCKS-capable inbound in sing-box's config
// and returns its listen IP plus true. Returns "", false if no compatible
// inbound is configured.
func singBoxMixedListenIP(port string) (string, bool) {
	data, err := os.ReadFile(singBoxConfig)
	if err != nil {
		return "", false
	}
	var cfg struct {
		Inbounds []struct {
			Type       string `json:"type"`
			ListenPort int    `json:"listen_port"`
			Listen     string `json:"listen"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return "", false
	}
	portI, err := strconv.Atoi(port)
	if err != nil {
		return "", false
	}
	for _, in := range cfg.Inbounds {
		switch in.Type {
		case "mixed", "socks", "socks5":
		default:
			continue
		}
		if in.ListenPort == portI {
			return in.Listen, true
		}
	}
	return "", false
}

func uciGet(key string) string {
	out, err := exec.Command("uci", "-q", "get", key).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func lanIP() string {
	v := uciGet("network.lan.ipaddr")
	if v == "" {
		return ""
	}
	if i := strings.Index(v, "/"); i >= 0 {
		return v[:i]
	}
	return v
}
