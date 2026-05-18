package service

import (
	"bufio"
	"os"
	"regexp"
	"strings"
)

const podkopConstantsPath = "/usr/lib/podkop/constants.sh"

// readPodkopConstants parses /usr/lib/podkop/constants.sh and returns the
// set of NAME=VALUE pairs found. Quoted values are unquoted. Comments and
// blank lines are skipped. Returns nil if the file is not present.
func readPodkopConstants() map[string]string {
	f, err := os.Open(podkopConstantsPath)
	if err != nil {
		return nil
	}
	defer f.Close()

	out := make(map[string]string)
	re := regexp.MustCompile(`^([A-Z_][A-Z0-9_]*)=(.*)$`)
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m := re.FindStringSubmatch(line)
		if len(m) != 3 {
			continue
		}
		v := strings.TrimSpace(m[2])
		// Strip optional surrounding quotes and any trailing inline comment.
		if i := strings.Index(v, " #"); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
		out[m[1]] = v
	}
	return out
}

// LoadDNSConfig returns DefaultDNSConfig overridden with values from
// /usr/lib/podkop/constants.sh where present. Pure function — no
// side-effects on package state.
func LoadDNSConfig() DNSConfig {
	cfg := DefaultDNSConfig()
	c := readPodkopConstants()
	if c == nil {
		return cfg
	}
	if v := c["FAKEIP_TEST_DOMAIN"]; v != "" {
		cfg.TestDomain = v
	}
	if addr := c["SB_DNS_INBOUND_ADDRESS"]; addr != "" {
		port := c["SB_DNS_INBOUND_PORT"]
		if port == "" {
			port = "53"
		}
		cfg.Server = addr + ":" + port
	}
	if r := c["SB_FAKEIP_INET4_RANGE"]; r != "" {
		// "198.18.0.0/15" → "198.18."
		base := r
		if i := strings.Index(base, "/"); i > 0 {
			base = base[:i]
		}
		parts := strings.SplitN(base, ".", 4)
		if len(parts) >= 2 {
			cfg.ExpectedPfx = parts[0] + "." + parts[1] + "."
		}
	}
	return cfg
}
