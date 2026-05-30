package selfupdate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVerifyChecksum(t *testing.T) {
	const sum = "abc123def456"

	newServer := func(status int, body string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			if body != "" {
				_, _ = w.Write([]byte(body))
			}
		}))
	}

	t.Run("match", func(t *testing.T) {
		// sha256sum-style: "<hash>  <filename>"
		srv := newServer(200, sum+"  podkop_updater-amd64\n")
		defer srv.Close()
		if err := verifyChecksum(context.Background(), srv.Client(), srv.URL+"/bin", sum); err != nil {
			t.Fatalf("want nil, got %v", err)
		}
	})

	t.Run("mismatch", func(t *testing.T) {
		srv := newServer(200, "deadbeef  podkop_updater-amd64\n")
		defer srv.Close()
		if err := verifyChecksum(context.Background(), srv.Client(), srv.URL+"/bin", sum); err == nil {
			t.Fatal("want mismatch error, got nil")
		}
	})

	t.Run("missing tolerated", func(t *testing.T) {
		srv := newServer(404, "")
		defer srv.Close()
		if err := verifyChecksum(context.Background(), srv.Client(), srv.URL+"/bin", sum); err != nil {
			t.Fatalf("404 should be tolerated, got %v", err)
		}
	})
}
