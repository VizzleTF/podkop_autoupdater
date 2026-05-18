package service

import "strings"

// PodkopDoHEndpoint reads podkop's UCI DNS settings and returns the DoH
// JSON endpoint URL configured by the user. Returns "" if podkop is not
// using DoH (caller should fall back to compiled defaults).
//
// Possible UCI values:
//   podkop.settings.dns_type   = doh | udp | tcp | dot | ...
//   podkop.settings.dns_server = IP | hostname | full https URL
func PodkopDoHEndpoint() string {
	if uciGet("podkop.settings.dns_type") != "doh" {
		return ""
	}
	srv := uciGet("podkop.settings.dns_server")
	if srv == "" {
		return ""
	}
	if strings.HasPrefix(srv, "https://") {
		return srv
	}
	return "https://" + srv + "/dns-query"
}
