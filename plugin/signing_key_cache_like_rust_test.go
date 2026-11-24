package plugin

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const signingKeyCacheTestKeyJSON = `{"key_id":"release-1","purpose":"PLUGIN_RELEASE_ARCHIVE_SIGNING","algorithm":"ES256","public_key_spki_der_base64":"AAAA"}`

func signingKeyCacheTestBody(ttl int) string {
	return fmt.Sprintf(`{"keys":[%s],"cache_ttl_seconds":%d}`, signingKeyCacheTestKeyJSON, ttl)
}

func signingKeyCacheTestHeaders(token string) http.Header {
	headers := http.Header{}
	if token != "" {
		headers.Set("Authorization", "Bearer "+token)
	}
	return headers
}

// mutableCacheClock lets a test expire the cache without sleeping.
type mutableCacheClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *mutableCacheClock) nowFunc() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *mutableCacheClock) advance(delta time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(delta)
}

// Rust #63490: a listing is reused for the same endpoint and credentials until
// the server-provided cache_ttl_seconds expires.
func TestSigningKeyCacheReusesKeysWithinTTLLikeRust(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(signingKeyCacheTestBody(600)))
	}))
	defer server.Close()

	cache := &SigningKeyCache{}
	headers := signingKeyCacheTestHeaders("token-a")
	first, err := cache.LookupPluginArchiveSigningKey(context.Background(), server.Client(), server.URL, headers, "release-1")
	if err != nil {
		t.Fatalf("first lookup: %v", err)
	}
	second, err := cache.LookupPluginArchiveSigningKey(context.Background(), server.Client(), server.URL, headers, "release-1")
	if err != nil {
		t.Fatalf("second lookup: %v", err)
	}
	if first.KeyID != second.KeyID || first.PublicKeySPKIDERBase64 != second.PublicKeySPKIDERBase64 {
		t.Fatalf("cached key = %+v, want %+v", second, first)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("key requests = %d, want 1 within the TTL", got)
	}
}

// Rust #63490: an expired listing is fetched again, and a zero TTL never
// serves a cached key.
func TestSigningKeyCacheRefetchesAfterExpiryLikeRust(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(signingKeyCacheTestBody(600)))
	}))
	defer server.Close()

	clock := &mutableCacheClock{now: time.Unix(1_700_000_000, 0)}
	cache := &SigningKeyCache{now: clock.nowFunc}
	headers := signingKeyCacheTestHeaders("token-a")
	if _, err := cache.LookupPluginArchiveSigningKey(context.Background(), server.Client(), server.URL, headers, "release-1"); err != nil {
		t.Fatalf("first lookup: %v", err)
	}
	clock.advance(601 * time.Second)
	if _, err := cache.LookupPluginArchiveSigningKey(context.Background(), server.Client(), server.URL, headers, "release-1"); err != nil {
		t.Fatalf("expired lookup: %v", err)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("key requests = %d, want a fetch after the TTL expired", got)
	}

	zeroTTL := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(signingKeyCacheTestBody(0)))
	}))
	defer zeroTTL.Close()
	requests.Store(0)
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := cache.LookupPluginArchiveSigningKey(context.Background(), zeroTTL.Client(), zeroTTL.URL, headers, "release-1"); err != nil {
			t.Fatalf("zero-TTL lookup %d: %v", attempt, err)
		}
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("zero-TTL key requests = %d, want a fetch per lookup", got)
	}
}

// Rust #63490: a request without an Authorization header always fetches, since
// an external provider can authenticate with custom headers.
func TestSigningKeyCacheFetchesWithoutAuthorizationLikeRust(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(signingKeyCacheTestBody(600)))
	}))
	defer server.Close()

	cache := &SigningKeyCache{}
	headers := http.Header{}
	headers.Set("X-Provider-Key", "custom")
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := cache.LookupPluginArchiveSigningKey(context.Background(), server.Client(), server.URL, headers, "release-1"); err != nil {
			t.Fatalf("lookup %d: %v", attempt, err)
		}
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("key requests = %d, want one per lookup without an Authorization header", got)
	}
}

// Rust #63490: the cache is scoped to the exact credentials, so another account
// or provider token never sees the previous listing.
func TestSigningKeyCacheIsolatesCredentialsLikeRust(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(signingKeyCacheTestBody(600)))
	}))
	defer server.Close()

	cache := &SigningKeyCache{}
	first := signingKeyCacheTestHeaders("token-a")
	second := signingKeyCacheTestHeaders("token-b")
	for _, headers := range []http.Header{first, second, first} {
		if _, err := cache.LookupPluginArchiveSigningKey(context.Background(), server.Client(), server.URL, headers, "release-1"); err != nil {
			t.Fatalf("lookup: %v", err)
		}
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("key requests = %d, want one per credential scope", got)
	}
}

// Rust #63490: a missing key ID fetches again, and the fresh listing still
// answers for the keys it carries.
func TestSigningKeyCacheRefreshesForUnknownKeyLikeRust(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(signingKeyCacheTestBody(600)))
	}))
	defer server.Close()

	cache := &SigningKeyCache{}
	headers := signingKeyCacheTestHeaders("token-a")
	if _, err := cache.LookupPluginArchiveSigningKey(context.Background(), server.Client(), server.URL, headers, "release-1"); err != nil {
		t.Fatalf("known lookup: %v", err)
	}
	if _, err := cache.LookupPluginArchiveSigningKey(context.Background(), server.Client(), server.URL, headers, "release-2"); PluginArchiveFailureCode(err) != "unknown_signing_key" {
		t.Fatalf("unknown key lookup error = %v, want unknown_signing_key", err)
	}
	if _, err := cache.LookupPluginArchiveSigningKey(context.Background(), server.Client(), server.URL, headers, "release-1"); err != nil {
		t.Fatalf("lookup after the refresh: %v", err)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("key requests = %d, want the refresh plus the fresh listing", got)
	}
}

// Rust #63490: a denied refresh leaves no cached keys, so the next lookup has
// to fetch instead of serving the previous listing.
func TestSigningKeyCacheDropsKeysAfterFailedRefreshLikeRust(t *testing.T) {
	var requests atomic.Int32
	var fail atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		if fail.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(signingKeyCacheTestBody(600)))
	}))
	defer server.Close()

	cache := &SigningKeyCache{}
	headers := signingKeyCacheTestHeaders("token-a")
	if _, err := cache.LookupPluginArchiveSigningKey(context.Background(), server.Client(), server.URL, headers, "release-1"); err != nil {
		t.Fatalf("first lookup: %v", err)
	}
	fail.Store(true)
	if _, err := cache.LookupPluginArchiveSigningKey(context.Background(), server.Client(), server.URL, headers, "release-2"); PluginArchiveFailureCode(err) != PluginArchiveSigningKeysUnavailableCode {
		t.Fatalf("denied refresh error = %v, want signing_keys_unavailable", err)
	}
	fail.Store(false)
	if _, err := cache.LookupPluginArchiveSigningKey(context.Background(), server.Client(), server.URL, headers, "release-1"); err != nil {
		t.Fatalf("lookup after the denied refresh: %v", err)
	}
	if got := requests.Load(); got != 3 {
		t.Fatalf("key requests = %d, want a fresh fetch after the denied refresh", got)
	}
}

// Rust #63491: overlapping lookups for one scope share a completed fetch, even
// when its TTL is zero, and each resolves its own key ID from it.
func TestSigningKeyCacheCoalescesConcurrentFetchesLikeRust(t *testing.T) {
	var requests atomic.Int32
	arrived := make(chan struct{}, 1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			arrived <- struct{}{}
			<-release
		}
		_, _ = w.Write([]byte(signingKeyCacheTestBody(0)))
	}))
	defer server.Close()

	cache := &SigningKeyCache{}
	headers := signingKeyCacheTestHeaders("token-a")
	results := make(chan error, 5)
	for attempt := 0; attempt < 5; attempt++ {
		go func() {
			key, err := cache.LookupPluginArchiveSigningKey(context.Background(), server.Client(), server.URL, headers, "release-1")
			if err == nil && key.KeyID != "release-1" {
				err = fmt.Errorf("key ID = %q", key.KeyID)
			}
			results <- err
		}()
	}
	select {
	case <-arrived:
	case <-time.After(5 * time.Second):
		t.Fatal("the first signing-key fetch never started")
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	for attempt := 0; attempt < 5; attempt++ {
		if err := <-results; err != nil {
			t.Fatalf("concurrent lookup: %v", err)
		}
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("concurrent key requests = %d, want one shared fetch", got)
	}
}
