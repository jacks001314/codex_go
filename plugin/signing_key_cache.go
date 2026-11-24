package plugin

import (
	"context"
	"crypto/sha256"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Rust parity: codex-rs/plugin-archive-verification/src/archive_signing_keys.rs
// (#63490/#63491). Signing keys are reused inside one endpoint and credential
// scope until the server-provided cache TTL expires, and overlapping lookups
// share one completed fetch. A request without a usable Authorization header
// always fetches, because an external provider can authenticate with custom
// headers a shared cache could not attribute.

// maxCachedSigningKeyScopes bounds the endpoints and credentials one owner
// retains (Rust MAX_CACHED_SCOPES).
const maxCachedSigningKeyScopes = 8

// maxSigningKeyCacheTTLSeconds keeps a hostile or broken response from
// overflowing the expiry arithmetic; Rust falls back to no caching when the
// checked add fails.
const maxSigningKeyCacheTTLSeconds = uint64(1 << 31)

type signingKeyCacheScope struct {
	endpoint    string
	fingerprint [sha256.Size]byte
}

// signingKeyFetch is one completed key listing plus the failure it reported.
// A failed fetch keeps no keys, so a later lookup cannot reuse stale material.
type signingKeyFetch struct {
	keys      map[string]PluginArchiveSigningKey
	err       error
	expiresAt time.Time
}

func (f *signingKeyFetch) fresh(keyID string, now time.Time) (PluginArchiveSigningKey, bool) {
	if f == nil || f.err != nil || !now.Before(f.expiresAt) {
		return PluginArchiveSigningKey{}, false
	}
	key, ok := f.keys[keyID]
	return key, ok
}

func (f *signingKeyFetch) resolve(keyID string) (PluginArchiveSigningKey, error) {
	if f == nil {
		return PluginArchiveSigningKey{}, errPluginArchiveKeysUnavailable
	}
	if f.err != nil {
		return PluginArchiveSigningKey{}, f.err
	}
	if key, ok := f.keys[keyID]; ok {
		return key, nil
	}
	return PluginArchiveSigningKey{}, errPluginArchiveSigningKeyUnknown
}

// scopedSigningKeyCache serializes refreshes for one endpoint and credential
// scope. completedFetches lets a lookup that overlapped a refresh share its
// result instead of issuing another request.
type scopedSigningKeyCache struct {
	mu               sync.Mutex
	completedFetches uint64
	lastFetch        *signingKeyFetch
	refresh          chan struct{}
}

func newScopedSigningKeyCache() *scopedSigningKeyCache {
	cache := &scopedSigningKeyCache{refresh: make(chan struct{}, 1)}
	cache.refresh <- struct{}{}
	return cache
}

// key resolves one key ID, fetching at most once per refresh round. The
// refresh permit is released even when the fetch fails or the caller's context
// ends, so a waiter can retry instead of inheriting a canceled refresh.
func (s *scopedSigningKeyCache) key(ctx context.Context, client HTTPDoer, keyURL string, headers http.Header, keyID string, now func() time.Time) (PluginArchiveSigningKey, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	if key, ok := s.lastFetch.fresh(keyID, now()); ok {
		s.mu.Unlock()
		return key, nil
	}
	observed := s.completedFetches
	s.mu.Unlock()

	select {
	case <-s.refresh:
	case <-ctx.Done():
		return PluginArchiveSigningKey{}, errPluginArchiveKeysUnavailable
	}
	defer func() { s.refresh <- struct{}{} }()

	s.mu.Lock()
	if s.completedFetches != observed {
		completed := s.lastFetch
		s.mu.Unlock()
		return completed.resolve(keyID)
	}
	// Clear the previous listing before the request so a denied refresh leaves
	// nothing cached to reuse.
	s.lastFetch = nil
	s.mu.Unlock()

	keys, ttl, err := FetchPluginArchiveSigningKeysWithTTL(ctx, client, keyURL, headers)
	if err != nil && ctx.Err() != nil {
		// The refresh was canceled rather than answered, so it counts as no
		// completed fetch: a waiter with a live context retries (Rust #63491).
		return PluginArchiveSigningKey{}, err
	}
	fetch := &signingKeyFetch{err: err}
	if err == nil {
		fetch.keys = make(map[string]PluginArchiveSigningKey, len(keys))
		for _, key := range keys {
			fetch.keys[key.KeyID] = key
		}
		if ttl > maxSigningKeyCacheTTLSeconds {
			ttl = maxSigningKeyCacheTTLSeconds
		}
		fetch.expiresAt = now().Add(time.Duration(ttl) * time.Second)
	}
	s.mu.Lock()
	s.lastFetch = fetch
	s.completedFetches++
	s.mu.Unlock()
	return fetch.resolve(keyID)
}

type signingKeyCacheEntry struct {
	scope signingKeyCacheScope
	cache *scopedSigningKeyCache
}

// SigningKeyCache reuses Plugin Service signing keys within one plugin owner
// and credential scope. The zero value is ready; a nil cache fetches every key.
type SigningKeyCache struct {
	mu      sync.Mutex
	entries []signingKeyCacheEntry
	// now is overridden by tests; production keeps time.Now.
	now func() time.Time
}

func (c *SigningKeyCache) clock() time.Time {
	if c != nil && c.now != nil {
		return c.now()
	}
	return time.Now()
}

// signingKeyHeaderFingerprint binds a scope to the exact outgoing credentials,
// including account and provider identity headers.
func signingKeyHeaderFingerprint(headers http.Header) [sha256.Size]byte {
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	digest := sha256.New()
	for _, name := range names {
		for _, value := range headers[name] {
			digest.Write([]byte(name))
			digest.Write([]byte(":"))
			digest.Write([]byte(value))
			digest.Write([]byte("\n"))
		}
	}
	var fingerprint [sha256.Size]byte
	copy(fingerprint[:], digest.Sum(nil))
	return fingerprint
}

// scope returns the cache for one endpoint and credential pair, keeping the
// most recently used scopes and dropping the rest.
func (c *SigningKeyCache) scope(endpoint string, headers http.Header) *scopedSigningKeyCache {
	if strings.TrimSpace(headers.Get("Authorization")) == "" {
		// External providers can authenticate with custom headers, so such a
		// request always fetches.
		return newScopedSigningKeyCache()
	}
	if c == nil {
		return newScopedSigningKeyCache()
	}
	target := signingKeyCacheScope{endpoint: endpoint, fingerprint: signingKeyHeaderFingerprint(headers)}
	c.mu.Lock()
	defer c.mu.Unlock()
	for index, entry := range c.entries {
		if entry.scope != target {
			continue
		}
		c.entries = append(c.entries[:index], c.entries[index+1:]...)
		c.entries = append([]signingKeyCacheEntry{entry}, c.entries...)
		return entry.cache
	}
	entry := signingKeyCacheEntry{scope: target, cache: newScopedSigningKeyCache()}
	c.entries = append([]signingKeyCacheEntry{entry}, c.entries...)
	if len(c.entries) > maxCachedSigningKeyScopes {
		c.entries = c.entries[:maxCachedSigningKeyScopes]
	}
	return entry.cache
}

// LookupPluginArchiveSigningKey returns one published signing key, reusing a
// cached listing for the same endpoint and credentials while it is fresh.
func (c *SigningKeyCache) LookupPluginArchiveSigningKey(ctx context.Context, client HTTPDoer, serviceBaseURL string, headers http.Header, keyID string) (PluginArchiveSigningKey, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(serviceBaseURL), "/")
	if endpoint == "" || strings.TrimSpace(keyID) == "" {
		return PluginArchiveSigningKey{}, errPluginArchiveSigningKeyUnknown
	}
	cache := c.scope(endpoint, headers)
	return cache.key(ctx, client, endpoint+PluginArchiveSigningKeysPath, headers, strings.TrimSpace(keyID), c.clock)
}
