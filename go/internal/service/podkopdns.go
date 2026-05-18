package service

import (
	"encoding/json"
	"os/exec"
	"strings"

	"github.com/VizzleTF/podkop_autoupdater/go/internal/uci"
)

// PodkopDoHEndpoint returns the DoH JSON endpoint URL configured by podkop.
// Tries the podkop CLI first (`podkop check_dns_available` exposes the
// resolved dns_type/dns_server in one shot) and falls back to a direct UCI
// read if the CLI is unavailable. Returns "" if podkop is not configured
// for DoH (caller should fall back to compiled defaults).
func PodkopDoHEndpoint() string {
	if ep := dohEndpointFromCLI(); ep != "" {
		return ep
	}
	return dohEndpointFromUCI()
}

func dohEndpointFromCLI() string {
	out, err := exec.Command("podkop", "check_dns_available").Output()
	if err != nil || len(out) == 0 {
		return ""
	}
	var d struct {
		DNSType   string `json:"dns_type"`
		DNSServer string `json:"dns_server"`
	}
	if err := json.Unmarshal(out, &d); err != nil {
		return ""
	}
	if d.DNSType != "doh" || d.DNSServer == "" {
		return ""
	}
	return toDoHURL(d.DNSServer)
}

func dohEndpointFromUCI() string {
	dnsType, _ := uci.Get("podkop.settings.dns_type")
	if dnsType != "doh" {
		return ""
	}
	srv, _ := uci.Get("podkop.settings.dns_server")
	if srv == "" {
		return ""
	}
	return toDoHURL(srv)
}

func toDoHURL(srv string) string {
	if strings.HasPrefix(srv, "https://") {
		return srv
	}
	return "https://" + srv + "/dns-query"
}
