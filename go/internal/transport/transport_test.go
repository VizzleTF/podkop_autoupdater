package transport

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// trustServer returns a tls.Config that trusts the given httptest TLS server's
// self-signed certificate, so the pinned dialer's handshake validates.
func trustServer(srv *httptest.Server) *tls.Config {
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	return &tls.Config{RootCAs: pool}
}

// fakeRT is a controllable RoundTripper for cascade tests.
type fakeRT struct {
	name  string
	fail  bool
	calls atomic.Int32
}

func (f *fakeRT) RoundTrip(r *http.Request) (*http.Response, error) {
	f.calls.Add(1)
	if f.fail {
		return nil, errors.New("fake fail: " + f.name)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, f.name)
	}))
	defer srv.Close()
	// Synthesize a response directly to avoid wiring a real server.
	resp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{"X-Tier": []string{f.name}},
		Body:       http.NoBody,
		Request:    r,
	}
	return resp, nil
}

func newTiered(t *testing.T, rts ...*fakeRT) *TieredTransport {
	t.Helper()
	tiers := make([]*tier, len(rts))
	for i, rt := range rts {
		tiers[i] = &tier{name: rt.name, rt: rt}
	}
	return &TieredTransport{tiers: tiers}
}

func TestRoundTrip_FirstTierWorks(t *testing.T) {
	a := &fakeRT{name: "a"}
	b := &fakeRT{name: "b"}
	tt := newTiered(t, a, b)
	req, _ := http.NewRequest("GET", "http://example.com", nil)
	resp, err := tt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Header.Get("X-Tier"); got != "a" {
		t.Fatalf("want a, got %s", got)
	}
	if b.calls.Load() != 0 {
		t.Fatalf("b should not be called")
	}
}

func TestRoundTrip_CascadeOnFailure(t *testing.T) {
	a := &fakeRT{name: "a", fail: true}
	b := &fakeRT{name: "b"}
	c := &fakeRT{name: "c"}
	tt := newTiered(t, a, b, c)
	req, _ := http.NewRequest("GET", "http://example.com", nil)
	resp, err := tt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Header.Get("X-Tier"); got != "b" {
		t.Fatalf("want b, got %s", got)
	}
	if c.calls.Load() != 0 {
		t.Fatalf("c should not be called")
	}
	if tt.CurrentTier() != "b" {
		t.Fatalf("sticky should be b, got %s", tt.CurrentTier())
	}
}

func TestRoundTrip_Sticky(t *testing.T) {
	a := &fakeRT{name: "a", fail: true}
	b := &fakeRT{name: "b"}
	tt := newTiered(t, a, b)
	req, _ := http.NewRequest("GET", "http://example.com", nil)
	for i := 0; i < 3; i++ {
		if _, err := tt.RoundTrip(req); err != nil {
			t.Fatal(err)
		}
	}
	// After first request flips sticky to b, subsequent requests must skip a.
	if a.calls.Load() != 1 {
		t.Fatalf("a should be tried once (initial), got %d", a.calls.Load())
	}
	if b.calls.Load() != 3 {
		t.Fatalf("b should be tried 3 times, got %d", b.calls.Load())
	}
}

// TestPinnedDial_SubstitutesIP verifies the pinned host is dialed at one of the
// configured IPs and that a dead IP falls through to a live one, which then
// completes a full TLS handshake and is remembered as last-good.
func TestPinnedDial_SubstitutesIP(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()
	host, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "https://"))

	// First IP is dead (RFC5737 TEST-NET); second is the live TLS server.
	pt := newPinnedTransport(host, []string{"192.0.2.1", host})
	pt.tlsConfig = trustServer(srv)

	req, _ := http.NewRequest("GET", "https://"+host+":"+port+"/", nil)
	resp, err := pt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	// The live IP must be marked healthy and the dead one marked failed, so a
	// reconnect (via orderedIPs) tries the live IP first.
	if h := pt.health[host]; h == nil || h.okAt.IsZero() {
		t.Fatalf("live IP %q not marked healthy: %+v", host, h)
	}
	if h := pt.health["192.0.2.1"]; h == nil || h.errAt.IsZero() {
		t.Fatalf("dead IP not marked failed: %+v", h)
	}
	if got := pt.orderedIPs(); got[0] != host {
		t.Fatalf("healthy IP should sort first, got order %v", got)
	}
}

// TestPinnedDial_HealthOrdering verifies orderedIPs ranks healthy → unknown →
// failed, with stalest failure last-but-first-among-failed for re-probe.
func TestPinnedDial_HealthOrdering(t *testing.T) {
	pt := newPinnedTransport("api.test", []string{"good", "unknown", "freshFail", "staleFail"})
	// Seed health directly.
	now := time.Now()
	pt.health["good"] = &ipHealth{okAt: now}
	pt.health["staleFail"] = &ipHealth{errAt: now.Add(-time.Hour)}
	pt.health["freshFail"] = &ipHealth{errAt: now}
	// "unknown" left absent.

	got := pt.orderedIPs()
	want := []string{"good", "unknown", "staleFail", "freshFail"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

// TestPinnedDial_SkipsPoisonedIP is the regression for the real bug: an IP that
// accepts TCP but serves broken TLS (writes garbage instead of a handshake)
// must fail *inside* the dialer, not be handed up as a good connection. The
// walk onto a live IP after such a failure is covered by SubstitutesIP.
func TestPinnedDial_SkipsPoisonedIP(t *testing.T) {
	// Poison server: accepts TCP, then writes garbage instead of a TLS record.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_, _ = c.Write([]byte("this is not a TLS handshake\n"))
			_ = c.Close()
		}
	}()

	pt := newPinnedTransport("api.test", nil)
	conn, err := pt.tlsDial(t.Context(), "tcp", ln.Addr().String(), "api.test")
	if err == nil {
		conn.Close()
		t.Fatal("TCP-ok but TLS-broken IP must fail in the dialer, got success")
	}
}

// TestPinnedDial_AllIPsDead returns an error mentioning the pinned failure.
func TestPinnedDial_AllIPsDead(t *testing.T) {
	pt := newPinnedTransport("pinned.example", []string{"192.0.2.1", "192.0.2.2"})
	req, _ := http.NewRequest("GET", "https://pinned.example:9/", nil)
	_, err := pt.RoundTrip(req)
	if err == nil {
		t.Fatal("want error when all pinned IPs dead")
	}
}

// TestPinnedDial_OtherHostUsesResolver confirms a non-pinned host is dialed
// normally (so github fetches still work).
func TestPinnedDial_OtherHostUsesResolver(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()
	pt := newPinnedTransport("pinned.example", []string{"192.0.2.1"})
	pt.tlsConfig = trustServer(srv)
	req, _ := http.NewRequest("GET", srv.URL+"/", nil)
	resp, err := pt.RoundTrip(req)
	if err != nil {
		t.Fatalf("non-pinned host should dial normally: %v", err)
	}
	resp.Body.Close()
}

// TestSetEmergencyIPs_InPlace confirms IP swap updates the tier without
// changing tier identity (sticky-safe) and refreshes the display name.
func TestSetEmergencyIPs_InPlace(t *testing.T) {
	tt := New("", "pinned.example", []string{"192.0.2.1"})
	if got := tt.Tiers(); len(got) != 1 || got[0] != "telegram-ip(1)" {
		t.Fatalf("unexpected tiers: %v", got)
	}
	ipTier := tt.ipTier
	tt.SetEmergencyIPs([]string{"192.0.2.1", "192.0.2.2", "192.0.2.3"})
	if tt.ipTier != ipTier {
		t.Fatal("pinned tier identity must not change on IP swap")
	}
	if got := tt.Tiers(); got[len(got)-1] != "telegram-ip(3)" {
		t.Fatalf("tier name not refreshed: %v", got)
	}
	if got := ipTier.snapshot(); len(got) != 3 {
		t.Fatalf("IP set not updated: %v", got)
	}
}

// TestDegraded_TracksActualTier verifies Degraded reflects the last
// *successful* tier and does not flap when ResetSticky optimistically nudges
// the index back to 0 (the watchdog must not see spurious recover/degrade).
func TestDegraded_TracksActualTier(t *testing.T) {
	a := &fakeRT{name: "a", fail: true}
	b := &fakeRT{name: "b"}
	tt := newTiered(t, a, b)
	req, _ := http.NewRequest("GET", "http://example.com", nil)

	if _, err := tt.RoundTrip(req); err != nil {
		t.Fatal(err)
	}
	if !tt.Degraded() {
		t.Fatal("want degraded after falling through to tier b")
	}

	// ResetSticky alone must NOT clear degraded — only an actual tier-0 success.
	tt.ResetSticky()
	if !tt.Degraded() {
		t.Fatal("ResetSticky must not flap degraded to false")
	}

	// tier-0 recovers; next request succeeds on it → no longer degraded.
	a.fail = false
	if _, err := tt.RoundTrip(req); err != nil {
		t.Fatal(err)
	}
	if tt.Degraded() {
		t.Fatal("want not degraded after a tier-0 success")
	}
}

// TestEnsureReplayableBody covers the cross-tier retry fix: a non-replayable
// body (GetBody == nil, like the go-telegram multipart pipe) must survive the
// first tier consuming and closing it, so a later tier still reads it in full.
func TestEnsureReplayableBody(t *testing.T) {
	pr, pw := io.Pipe()
	go func() {
		_, _ = pw.Write([]byte("payload123"))
		_ = pw.Close()
	}()
	req, _ := http.NewRequest("POST", "http://example.com", pr)
	req.GetBody = nil // a pipe body is not replayable

	if err := ensureReplayableBody(req); err != nil {
		t.Fatal(err)
	}
	if req.GetBody == nil {
		t.Fatal("GetBody should be installed")
	}

	// First tier reads then closes its clone (as http.Transport does on error).
	c1 := cloneReq(req)
	b1, _ := io.ReadAll(c1.Body)
	c1.Body.Close()
	if string(b1) != "payload123" {
		t.Fatalf("first read = %q", b1)
	}
	// Second tier must still see the full body, not a closed pipe.
	c2 := cloneReq(req)
	b2, _ := io.ReadAll(c2.Body)
	c2.Body.Close()
	if string(b2) != "payload123" {
		t.Fatalf("second read = %q (cross-tier replay broken)", b2)
	}
}

func TestRoundTrip_AllFail(t *testing.T) {
	a := &fakeRT{name: "a", fail: true}
	b := &fakeRT{name: "b", fail: true}
	tt := newTiered(t, a, b)
	req, _ := http.NewRequest("GET", "http://example.com", nil)
	_, err := tt.RoundTrip(req)
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !strings.Contains(err.Error(), "all tiers failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}
