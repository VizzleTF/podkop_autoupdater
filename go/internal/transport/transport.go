// Package transport implements an http.RoundTripper with three-tier
// fallback: Podkop SOCKS5 → direct → emergency Telegram IPs.
//
// The active tier is sticky: once a tier succeeds it becomes the first try
// for subsequent requests, with a short connect timeout before falling back
// through the cascade again.
package transport

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/proxy"

	"github.com/VizzleTF/podkop_autoupdater/go/internal/logger"
)

const (
	defaultConnectTimeout  = 3 * time.Second
	defaultTLSTimeout      = 5 * time.Second
	defaultKeepAlive       = 30 * time.Second
	defaultIdleConnTimeout = 90 * time.Second
)

type tier struct {
	name string
	rt   http.RoundTripper
}

// TieredTransport is a fallback-aware RoundTripper. It is safe for
// concurrent use.
type TieredTransport struct {
	pinnedHost string

	mu    sync.Mutex
	tiers []*tier
	idx   int // sticky index (last known good tier)
}

// New builds the standard three-tier transport for podkop_updater:
//   - tier 0: Podkop SOCKS5 (if socksAddr is non-empty)
//   - tier 1: Direct
//   - tier 2..N: Emergency IPs that override DNS for telegramHost
func New(socksAddr, telegramHost string, emergencyIPs []string) *TieredTransport {
	var tiers []*tier
	if socksAddr != "" {
		if rt := socksRoundTripper(socksAddr); rt != nil {
			tiers = append(tiers, &tier{name: "socks5:" + socksAddr, rt: rt})
		}
	}
	tiers = append(tiers, &tier{name: "direct", rt: directRoundTripper()})
	for _, ip := range emergencyIPs {
		tiers = append(tiers, &tier{
			name: "emergency:" + ip,
			rt:   pinnedHostRoundTripper(telegramHost, ip),
		})
	}
	return &TieredTransport{
		pinnedHost: telegramHost,
		tiers:      tiers,
		idx:        0,
	}
}

// RebuildEmergency replaces the emergency-tier set with new IPs while
// preserving the SOCKS5 and direct tiers. Resets the sticky index since
// the slice order may have shifted.
func (t *TieredTransport) RebuildEmergency(ips []string) {
	newEmergency := make([]*tier, 0, len(ips))
	for _, ip := range ips {
		newEmergency = append(newEmergency, &tier{
			name: "emergency:" + ip,
			rt:   pinnedHostRoundTripper(t.pinnedHost, ip),
		})
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	kept := make([]*tier, 0, len(t.tiers))
	for _, x := range t.tiers {
		if strings.HasPrefix(x.name, "emergency:") {
			continue
		}
		kept = append(kept, x)
	}
	t.tiers = append(kept, newEmergency...)
	t.idx = 0
	logger.Logf("Transport: rebuilt emergency tiers: %v", ips)
}

// Tiers returns the configured tier names, in cascade order.
func (t *TieredTransport) Tiers() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	names := make([]string, len(t.tiers))
	for i, x := range t.tiers {
		names[i] = x.name
	}
	return names
}

// CurrentTier returns the name of the sticky tier.
func (t *TieredTransport) CurrentTier() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.tiers) == 0 {
		return ""
	}
	return t.tiers[t.idx].name
}

// RoundTrip satisfies http.RoundTripper. Tries the sticky tier first, then
// walks the cascade in order. Returns the first successful response, or an
// error wrapping the last attempt's error.
func (t *TieredTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.mu.Lock()
	snapshot := append([]*tier(nil), t.tiers...)
	start := t.idx
	t.mu.Unlock()

	n := len(snapshot)
	if n == 0 {
		return nil, errors.New("transport: no tiers configured")
	}
	if start >= n {
		start = 0
	}

	var lastErr error
	for offset := 0; offset < n; offset++ {
		i := (start + offset) % n
		resp, err := snapshot[i].rt.RoundTrip(cloneReq(req))
		if err == nil {
			t.mu.Lock()
			// Only update sticky if the underlying slice hasn't been swapped.
			if i < len(t.tiers) && t.tiers[i] == snapshot[i] && t.idx != i {
				logger.Logf("Transport: switched to %s", snapshot[i].name)
				t.idx = i
			}
			t.mu.Unlock()
			return resp, nil
		}
		logger.Logf("Transport: tier %s failed: %v", snapshot[i].name, err)
		lastErr = err
	}
	return nil, fmt.Errorf("all tiers failed: %w", lastErr)
}

func cloneReq(req *http.Request) *http.Request {
	r2 := req.Clone(req.Context())
	if req.Body != nil && req.GetBody != nil {
		if body, err := req.GetBody(); err == nil {
			r2.Body = body
		}
	}
	return r2
}

func directRoundTripper() http.RoundTripper {
	return &http.Transport{
		DialContext:           dialer().DialContext,
		TLSHandshakeTimeout:   defaultTLSTimeout,
		IdleConnTimeout:       defaultIdleConnTimeout,
		ResponseHeaderTimeout: 0, // none; caller controls via context
	}
}

func socksRoundTripper(socksAddr string) http.RoundTripper {
	pd, err := proxy.SOCKS5("tcp", socksAddr, nil, dialer())
	if err != nil {
		logger.Errf("transport: socks5 init for %s: %v", socksAddr, err)
		return nil
	}
	var dialCtx func(ctx context.Context, network, addr string) (net.Conn, error)
	if cd, ok := pd.(proxy.ContextDialer); ok {
		dialCtx = cd.DialContext
	} else {
		dialCtx = func(_ context.Context, network, addr string) (net.Conn, error) {
			return pd.Dial(network, addr)
		}
	}
	return &http.Transport{
		DialContext:         dialCtx,
		TLSHandshakeTimeout: defaultTLSTimeout,
		IdleConnTimeout:     defaultIdleConnTimeout,
	}
}

// pinnedHostRoundTripper returns a transport that, when dialing pinnedHost,
// substitutes the destination IP. SNI and TLS host verification still use
// the original hostname, so the certificate validates correctly.
func pinnedHostRoundTripper(pinnedHost, ip string) http.RoundTripper {
	return &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			h, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			if h == pinnedHost {
				addr = net.JoinHostPort(ip, port)
			}
			return dialer().DialContext(ctx, network, addr)
		},
		TLSHandshakeTimeout: defaultTLSTimeout,
		IdleConnTimeout:     defaultIdleConnTimeout,
	}
}

func dialer() *net.Dialer {
	return &net.Dialer{
		Timeout:   defaultConnectTimeout,
		KeepAlive: defaultKeepAlive,
	}
}
