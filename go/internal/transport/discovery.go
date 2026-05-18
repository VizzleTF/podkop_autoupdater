package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sort"
)

// telegramCIDRs covers AS62041 (Telegram Messenger Inc) IPv4 prefixes.
// Used to filter DoH responses against unrelated CDN/proxy IPs.
var telegramCIDRs = []string{
	"91.105.192.0/23",
	"91.108.4.0/22",
	"91.108.8.0/22",
	"91.108.12.0/22",
	"91.108.16.0/22",
	"91.108.20.0/22",
	"91.108.56.0/22",
	"95.161.64.0/20",
	"149.154.160.0/20",
	"185.76.151.0/24",
}

var parsedCIDRs []*net.IPNet

func init() {
	for _, s := range telegramCIDRs {
		if _, n, err := net.ParseCIDR(s); err == nil && n != nil {
			parsedCIDRs = append(parsedCIDRs, n)
		}
	}
}

// InTelegramRange reports whether ip is inside a known Telegram prefix.
func InTelegramRange(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	for _, n := range parsedCIDRs {
		if n.Contains(parsed) {
			return true
		}
	}
	return false
}

// DefaultDoHEndpoints is the compiled-in fallback used when the caller
// doesn't supply DoH endpoints (e.g. podkop config isn't readable).
var DefaultDoHEndpoints = []string{
	"https://1.1.1.1/dns-query",       // Cloudflare
	"https://8.8.8.8/resolve",         // Google
	"https://dns.quad9.net/dns-query", // Quad9
}

type dohAnswer struct {
	Status int `json:"Status"`
	Answer []struct {
		Name string `json:"name"`
		Type int    `json:"type"`
		Data string `json:"data"`
	} `json:"Answer"`
}

// Discover queries the given DoH endpoints for A records of host and returns
// the deduplicated, sorted list of IPv4 addresses that fall inside known
// Telegram CIDR blocks. If endpoints is empty, DefaultDoHEndpoints is used.
//
// hc must be configured to reach the DoH endpoints directly (do not route
// through TieredTransport — DoH discovery is the thing we use when DNS
// itself is blocked).
func Discover(ctx context.Context, hc *http.Client, host string, endpoints []string) ([]string, error) {
	if hc == nil {
		hc = http.DefaultClient
	}
	if len(endpoints) == 0 {
		endpoints = DefaultDoHEndpoints
	}
	found := make(map[string]struct{})
	var lastErr error
	for _, ep := range endpoints {
		ips, err := queryDoH(ctx, hc, ep, host)
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", ep, err)
			continue
		}
		for _, ip := range ips {
			if InTelegramRange(ip) {
				found[ip] = struct{}{}
			}
		}
	}
	if len(found) == 0 {
		if lastErr != nil {
			return nil, fmt.Errorf("DoH discovery failed: %w", lastErr)
		}
		return nil, fmt.Errorf("DoH discovery: no IPs in Telegram range")
	}
	out := make([]string, 0, len(found))
	for ip := range found {
		out = append(out, ip)
	}
	sort.Strings(out)
	return out, nil
}

func queryDoH(ctx context.Context, hc *http.Client, endpoint, host string) ([]string, error) {
	url := fmt.Sprintf("%s?name=%s&type=A", endpoint, host)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/dns-json")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	var d dohAnswer
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return nil, err
	}
	var ips []string
	for _, a := range d.Answer {
		if a.Type == 1 { // A record
			ips = append(ips, a.Data)
		}
	}
	return ips, nil
}
