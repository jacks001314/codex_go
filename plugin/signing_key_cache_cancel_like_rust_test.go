package plugin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// Rust #63491: a refresh canceled mid-request is not a completed fetch, so a
// waiting lookup with a live context issues its own request and succeeds.
func TestSigningKeyCacheRetriesAfterCanceledRefreshLikeRust(t *testing.T) {
	var requests atomic.Int32
	arrived := make(chan struct{}, 1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			arrived <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		_, _ = w.Write([]byte(signingKeyCacheTestBody(600)))
	}))
	defer server.Close()

	cache := &SigningKeyCache{}
	headers := signingKeyCacheTestHeaders("token-a")
	canceled, cancel := context.WithCancel(context.Background())
	canceledErr := make(chan error, 1)
	go func() {
		_, err := cache.LookupPluginArchiveSigningKey(canceled, server.Client(), server.URL, headers, "release-1")
		canceledErr <- err
	}()
	select {
	case <-arrived:
	case <-time.After(5 * time.Second):
		t.Fatal("the canceled refresh never reached the key endpoint")
	}
	cancel()
	select {
	case err := <-canceledErr:
		if err == nil {
			t.Fatal("the canceled lookup reported success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the canceled lookup never returned")
	}
	close(release)

	key, err := cache.LookupPluginArchiveSigningKey(context.Background(), server.Client(), server.URL, headers, "release-1")
	if err != nil {
		t.Fatalf("lookup after the canceled refresh: %v", err)
	}
	if key.KeyID != "release-1" {
		t.Fatalf("key ID = %q, want release-1", key.KeyID)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("key requests = %d, want the retry after the cancellation", got)
	}
}
