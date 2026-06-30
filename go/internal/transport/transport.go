// Package transport implements an http.RoundTripper that reaches Telegram
// without ever depending on the system DNS resolver for api.telegram.org.
//
// The data path has at most two tiers:
//
//   - tier 0: Podkop SOCKS5 — only when the operator explicitly configures a
//     SOCKS endpoint. The proxy resolves api.telegram.org itself, so the local
//     resolver is not consulted. Useful when the ISP blocks Telegram IPs.
//   - tier 1: pinned IPs — a single transport whose dialer substitutes a known
//     Telegram IP for api.telegram.org (SNI preserved, so TLS still validates).
//     Any other host (e.g. github for the update path) is dialed normally via
//     the system resolver, since those IPs are not pinned.
//
// The pinned IP set is refreshed in the background via DoH (see discovery.go),
// which queries fixed resolver IPs over HTTPS and therefore also avoids the
// system resolver. If DoH fails, the previously known IP set is kept.
//
// The active tier is sticky: once a tier succeeds it becomes the first try for
// subsequent requests, with a short connect timeout before falling back
// through the cascade again.
package transport

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
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

	mu     sync.Mutex
	tiers  []*tier
	idx    int              // sticky index (last known good tier)
	ipTier *pinnedTransport // the pinned-IP tier, for in-place IP updates
}

// New builds the standard transport for podkop_updater:
//   - tier 0: Podkop SOCKS5 (only if socksAddr is non-empty)
//   - tier 1: pinned Telegram IPs (DNS-free for telegramHost)
func New(socksAddr, telegramHost string, emergencyIPs []string) *TieredTransport {
	var tiers []*tier
	if socksAddr != "" {
		if rt := socksRoundTripper(socksAddr); rt != nil {
			tiers = append(tiers, &tier{name: "socks5:" + socksAddr, rt: rt})
		}
	}
	pt := newPinnedTransport(telegramHost, emergencyIPs)
	tiers = append(tiers, &tier{name: ipTierName(emergencyIPs), rt: pt})
	return &TieredTransport{
		pinnedHost: telegramHost,
		tiers:      tiers,
		idx:        0,
		ipTier:     pt,
	}
}

// ipTierName labels the pinned-IP tier for /status display.
func ipTierName(ips []string) string {
	return fmt.Sprintf("telegram-ip(%d)", len(ips))
}

// SetEmergencyIPs swaps the pinned-IP set in place. The pinned tier itself is
// unchanged (same transport, same connection pool), so the sticky index stays
// valid and there is no tier reordering. No-op if there is no pinned tier.
func (t *TieredTransport) SetEmergencyIPs(ips []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ipTier == nil {
		return
	}
	t.ipTier.setIPs(ips)
	for _, x := range t.tiers {
		if x.rt == t.ipTier {
			x.name = ipTierName(ips)
		}
	}
	logger.Logf("Transport: updated telegram IPs: %v", ips)
}

// ResetSticky moves the sticky index back to tier 0 so the next request
// prefers the cheapest/most-preferred tier (SOCKS5 if configured) again.
// Called periodically so a transient outage that pushed us onto the pinned-IP
// tier does not pin us there after the primary path recovers. No-op if already
// at 0.
func (t *TieredTransport) ResetSticky() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.idx != 0 {
		logger.Logf("Transport: resetting sticky tier %s → %s", t.tiers[t.idx].name, t.tiers[0].name)
		t.idx = 0
	}
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

// pinnedTransport is an http.RoundTripper whose dialer, when connecting to the
// pinned host, substitutes a known IP from a swappable set and falls through
// the set until one connects. Every other host is dialed normally (system
// resolver), so non-Telegram traffic (e.g. github fetches) keeps working.
//
// SNI and TLS host verification still use the original hostname, so the
// certificate validates correctly even though we dial a raw IP.
type pinnedTransport struct {
	host string
	rt   *http.Transport

	mu  sync.RWMutex
	ips []string
	rr  atomic.Uint32 // round-robin start offset, spreads fresh dials across IPs
}

func newPinnedTransport(host string, ips []string) *pinnedTransport {
	pt := &pinnedTransport{host: host}
	pt.setIPs(ips)
	pt.rt = &http.Transport{
		DialContext:         pt.dial,
		TLSHandshakeTimeout: defaultTLSTimeout,
		IdleConnTimeout:     defaultIdleConnTimeout,
	}
	return pt
}

func (pt *pinnedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return pt.rt.RoundTrip(req)
}

func (pt *pinnedTransport) setIPs(ips []string) {
	cp := append([]string(nil), ips...)
	pt.mu.Lock()
	pt.ips = cp
	pt.mu.Unlock()
}

// snapshot returns the current IP set. The slice is never mutated in place
// (setIPs replaces the header), so the returned value is safe to read after
// the lock is dropped.
func (pt *pinnedTransport) snapshot() []string {
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	return pt.ips
}

func (pt *pinnedTransport) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if host != pt.host {
		// Non-pinned host (e.g. github): dial via the system resolver.
		return dialer().DialContext(ctx, network, addr)
	}
	ips := pt.snapshot()
	if len(ips) == 0 {
		return nil, fmt.Errorf("transport: no pinned IPs for %s", host)
	}
	start := int(pt.rr.Add(1)-1) % len(ips)
	d := dialer()
	var lastErr error
	for i := 0; i < len(ips); i++ {
		ip := ips[(start+i)%len(ips)]
		conn, err := d.DialContext(ctx, network, net.JoinHostPort(ip, port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
		logger.Logf("Transport: telegram IP %s dial failed: %v", ip, err)
	}
	return nil, fmt.Errorf("all telegram IPs failed: %w", lastErr)
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

func dialer() *net.Dialer {
	return &net.Dialer{
		Timeout:   defaultConnectTimeout,
		KeepAlive: defaultKeepAlive,
	}
}
