package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

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

// TestPinnedDial_SubstitutesIP verifies the pinned host is dialed at one of
// the configured IPs (not via the system resolver) and that an unreachable IP
// falls through to the next one.
func TestPinnedDial_SubstitutesIP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()
	host, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))

	pt := newPinnedTransport("pinned.example", []string{"192.0.2.1", host})
	var dialed []string
	base := pt.rt.DialContext
	pt.rt.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialed = append(dialed, addr)
		return base(ctx, network, addr)
	}

	req, _ := http.NewRequest("GET", "http://pinned.example:"+port+"/", nil)
	resp, err := pt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	resp.Body.Close()
	// The request addr handed to DialContext must be the pinned host, never a
	// resolved IP — substitution happens inside dial.
	if len(dialed) == 0 || dialed[0] != "pinned.example:"+port {
		t.Fatalf("expected dial to pinned host, got %v", dialed)
	}
}

// TestPinnedDial_AllIPsDead returns an error mentioning the pinned failure.
func TestPinnedDial_AllIPsDead(t *testing.T) {
	pt := newPinnedTransport("pinned.example", []string{"192.0.2.1", "192.0.2.2"})
	req, _ := http.NewRequest("GET", "http://pinned.example:9/", nil)
	_, err := pt.RoundTrip(req)
	if err == nil {
		t.Fatal("want error when all pinned IPs dead")
	}
}

// TestPinnedDial_OtherHostUsesResolver confirms a non-pinned host is dialed
// normally (so github fetches still work).
func TestPinnedDial_OtherHostUsesResolver(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()
	pt := newPinnedTransport("pinned.example", []string{"192.0.2.1"})
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
