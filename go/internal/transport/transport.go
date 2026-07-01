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
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
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

	mu            sync.Mutex
	tiers         []*tier
	idx           int              // sticky index (last known good tier)
	degraded      bool             // last successful request used a non-preferred tier (idx != 0)
	ipTier        *pinnedTransport // the pinned-IP tier, for in-place IP updates
	lastSuccessAt time.Time        // wall time of the last successful round-trip (passive liveness signal)
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
		pinnedHost:    telegramHost,
		tiers:         tiers,
		idx:           0,
		ipTier:        pt,
		lastSuccessAt: time.Now(), // assume healthy at boot; first request confirms
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

// Degraded reports whether the last successful request fell through to a
// non-preferred tier (e.g. SOCKS5 down, traffic on pinned IPs). Unaffected by
// ResetSticky's optimistic nudge — it reflects the last actual success, so a
// watchdog polling it won't see spurious recover/degrade flaps every reset
// cycle. Always false when there is only one tier.
func (t *TieredTransport) Degraded() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.degraded
}

// LastSuccess returns the wall-clock time of the last successful round-trip
// through any tier. The watchdog uses time-since-LastSuccess as a passive
// liveness signal: a healthy long-poll completes one round-trip per poll
// window, so a stale value means the actual data path (not a synthetic probe)
// has stopped completing requests.
func (t *TieredTransport) LastSuccess() time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lastSuccessAt
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
	// We retry the request across tiers, but http.RoundTripper closes req.Body
	// even on failure. If the body isn't replayable (GetBody == nil — e.g. the
	// go-telegram/bot multipart pipe), the first failing tier closes it and
	// every later tier reads from a closed body. Buffer it once and install a
	// GetBody so each cloneReq below gets a fresh independent reader.
	if err := ensureReplayableBody(req); err != nil {
		return nil, err
	}

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
			// Any successful round-trip is proof the data path is alive — this is
			// the passive liveness signal the watchdog reads (long-poll completes
			// one round-trip per poll window, so this advances even when idle).
			t.lastSuccessAt = time.Now()
			// Only update sticky if the underlying slice hasn't been swapped.
			if i < len(t.tiers) && t.tiers[i] == snapshot[i] {
				if t.idx != i {
					logger.Logf("Transport: switched to %s", snapshot[i].name)
					t.idx = i
				}
				// Health truth tracks the *actual* successful tier, not the
				// optimistic ResetSticky nudge — so the watchdog doesn't flap.
				t.degraded = i != 0
			}
			t.mu.Unlock()
			return resp, nil
		}
		logger.Logf("Transport: tier %s failed: %v", snapshot[i].name, err)
		lastErr = err
	}
	return nil, fmt.Errorf("all tiers failed: %w", lastErr)
}

// ensureReplayableBody buffers req.Body into memory and installs a GetBody so
// the request can be retried across tiers. No-op when there is no body or the
// body is already replayable (GetBody set, e.g. bodies from http.NewRequest
// with a bytes/strings reader). Telegram API request bodies are small.
func ensureReplayableBody(req *http.Request) error {
	if req.Body == nil || req.GetBody != nil {
		return nil
	}
	data, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		return err
	}
	req.Body = io.NopCloser(bytes.NewReader(data))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	}
	return nil
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

// pinnedTransport is an http.RoundTripper whose TLS dialer, when connecting to
// the pinned host, substitutes a known IP from a swappable set and walks the
// set until one completes a full TLS handshake. Every other host is dialed
// normally (system resolver), so non-Telegram traffic (e.g. github fetches)
// keeps working.
//
// The TLS handshake runs *inside* the dialer (DialTLSContext), not at the
// http.Transport layer. This is deliberate: a Telegram IP that accepts TCP but
// serves a broken/foreign TLS endpoint (common when a hardcoded DC IP is not
// reachable from a given geo — it half-answers instead of refusing) would, if
// we only checked TCP connect, be handed up as a "good" connection and then
// fail the handshake with no retry onto another IP. Handshaking in the dialer
// lets us skip such a poisoned IP within the same request and land on a live
// one, so the path stays up as long as *any* pinned IP works.
//
// SNI and certificate verification use the original hostname, so the cert
// validates correctly even though we dial a raw IP.
//
// Per-IP health (ipHealth) ranks the dial order: IPs that last handshook come
// first, never-tried IPs next, and IPs whose last attempt failed last (stalest
// failure first, so a temporarily-unreachable Telegram IP is eventually
// re-probed rather than pinned-dead forever). This mirrors TDLib's
// DcOptionsSet::Stat ok/error/check model and subsumes a single sticky
// last-good: steady state dials one healthy IP, and dead/poisoned IPs sink out
// of the way instead of costing a connect timeout on every reconnect.
type pinnedTransport struct {
	host      string
	rt        *http.Transport
	tlsConfig *tls.Config // base cloned per dial (ServerName/NextProtos set per call); nil → zero value. Test seam for custom roots.

	mu     sync.RWMutex
	ips    []string
	health map[string]*ipHealth // per-IP last ok/error times; pruned to current ips
}

// ipHealth records the last successful and last failed dial times for one IP.
type ipHealth struct {
	okAt  time.Time
	errAt time.Time
}

func newPinnedTransport(host string, ips []string) *pinnedTransport {
	pt := &pinnedTransport{host: host}
	pt.setIPs(ips)
	pt.rt = &http.Transport{
		DialTLSContext:  pt.dialTLS,
		IdleConnTimeout: defaultIdleConnTimeout,
		// We handshake ourselves and force HTTP/1.1, so leave the Transport's
		// own HTTP/2 wiring off (it would need to inspect the negotiated proto).
		ForceAttemptHTTP2: false,
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
	// Keep health only for IPs still in the set; new IPs start unknown.
	keep := make(map[string]*ipHealth, len(cp))
	for _, ip := range cp {
		if h, ok := pt.health[ip]; ok {
			keep[ip] = h
		}
	}
	pt.health = keep
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

// orderedIPs returns the current IP set sorted by health: healthy IPs first
// (most-recent success first), then never-tried IPs, then failed IPs (stalest
// failure first, so a long-dead IP is the last resort but still eventually
// re-probed). A reconnect thus tries a known-good IP before ever touching a
// dead/poisoned one.
func (pt *pinnedTransport) orderedIPs() []string {
	pt.mu.RLock()
	ips := append([]string(nil), pt.ips...)
	rank := make(map[string]int, len(ips))
	okAt := make(map[string]time.Time, len(ips))
	errAt := make(map[string]time.Time, len(ips))
	for _, ip := range ips {
		h := pt.health[ip]
		switch {
		case h == nil || (h.okAt.IsZero() && h.errAt.IsZero()):
			rank[ip] = 1 // unknown
		case h.okAt.After(h.errAt):
			rank[ip] = 0 // healthy
			okAt[ip] = h.okAt
		default:
			rank[ip] = 2 // failed
			errAt[ip] = h.errAt
		}
	}
	pt.mu.RUnlock()

	sort.SliceStable(ips, func(i, j int) bool {
		a, b := ips[i], ips[j]
		if rank[a] != rank[b] {
			return rank[a] < rank[b]
		}
		switch rank[a] {
		case 0: // healthy: most-recent success first
			return okAt[a].After(okAt[b])
		case 2: // failed: stalest failure first (most due for a re-probe)
			return errAt[a].Before(errAt[b])
		}
		return false // unknown: preserve input order
	})
	return ips
}

// mark records the outcome of a dial attempt against ip.
func (pt *pinnedTransport) mark(ip string, ok bool) {
	pt.mu.Lock()
	h := pt.health[ip]
	if h == nil {
		h = &ipHealth{}
		pt.health[ip] = h
	}
	now := time.Now()
	if ok {
		h.okAt = now
	} else {
		h.errAt = now
	}
	pt.mu.Unlock()
}

// dialTLS establishes a TLS connection. For the pinned host it walks the IP set
// doing a full TLS handshake per IP, skipping any that fail either TCP connect
// or the handshake. For any other host it resolves and dials normally.
func (pt *pinnedTransport) dialTLS(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if host != pt.host {
		// Non-pinned host (e.g. github): resolve + dial + TLS normally.
		return pt.tlsDial(ctx, network, addr, host)
	}
	ips := pt.orderedIPs()
	if len(ips) == 0 {
		return nil, fmt.Errorf("transport: no pinned IPs for %s", host)
	}
	var lastErr error
	for _, ip := range ips {
		conn, err := pt.tlsDial(ctx, network, net.JoinHostPort(ip, port), pt.host)
		if err == nil {
			pt.mark(ip, true)
			return conn, nil
		}
		pt.mark(ip, false)
		lastErr = err
		logger.Logf("Transport: telegram IP %s failed: %v", ip, err)
	}
	return nil, fmt.Errorf("all telegram IPs failed: %w", lastErr)
}

// tlsDial opens a TCP connection to addr and performs a TLS handshake using
// serverName for SNI and certificate verification (so a raw-IP addr still
// validates against the real hostname's cert).
func (pt *pinnedTransport) tlsDial(ctx context.Context, network, addr, serverName string) (net.Conn, error) {
	raw, err := dialer().DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	var cfg *tls.Config
	if pt.tlsConfig != nil {
		cfg = pt.tlsConfig.Clone()
	} else {
		cfg = &tls.Config{}
	}
	cfg.ServerName = serverName
	if len(cfg.NextProtos) == 0 {
		cfg.NextProtos = []string{"http/1.1"}
	}
	hctx := ctx
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		hctx, cancel = context.WithTimeout(ctx, defaultTLSTimeout)
		defer cancel()
	}
	tconn := tls.Client(raw, cfg)
	if err := tconn.HandshakeContext(hctx); err != nil {
		_ = raw.Close()
		return nil, err
	}
	return tconn, nil
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
