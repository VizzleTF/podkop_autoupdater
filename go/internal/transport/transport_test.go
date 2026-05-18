package transport

import (
	"errors"
	"fmt"
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
