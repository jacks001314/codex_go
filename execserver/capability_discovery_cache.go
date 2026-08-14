package execserver

import (
	"encoding/json"
	"sync"
)

// CapabilityDiscoveryCache memoizes capability-root discoveries for a
// thread-scoped consumer (Rust #38420). Successful results and permanent
// failures are cached; transient (retryable recovery) failures are not cached,
// so a later request can recover after an executor reconnect. Recovery of a
// previously failed root is reported so dependents can invalidate projections.
type CapabilityDiscoveryCache struct {
	mu        sync.Mutex
	entries   map[string]cachedDiscoveryEntry
	recovered bool
}

type cachedDiscoveryEntry struct {
	response  *CapabilityRootsDiscoverResponse
	err       error
	retryable bool
}

// NewCapabilityDiscoveryCache returns an empty capability discovery cache.
func NewCapabilityDiscoveryCache() *CapabilityDiscoveryCache {
	return &CapabilityDiscoveryCache{entries: map[string]cachedDiscoveryEntry{}}
}

// Get returns the cached entry for params. Transient failures are treated as a
// miss so they are retried on the next request.
func (c *CapabilityDiscoveryCache) Get(params *CapabilityRootsDiscoverParams) (cachedDiscoveryEntry, bool) {
	if c == nil || params == nil {
		return cachedDiscoveryEntry{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[capabilityDiscoveryCacheKey(params)]
	if ok && entry.retryable {
		return cachedDiscoveryEntry{}, false
	}
	return entry, ok
}

// Put stores a discovery result. A nil response with a non-nil err records a
// failure; retryable marks transient recovery failures that must not be cached.
func (c *CapabilityDiscoveryCache) Put(params *CapabilityRootsDiscoverParams, response *CapabilityRootsDiscoverResponse, err error, retryable bool) {
	if c == nil || params == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	key := capabilityDiscoveryCacheKey(params)
	previous, existed := c.entries[key]
	if existed && previous.err != nil && err == nil {
		c.recovered = true
	}
	c.entries[key] = cachedDiscoveryEntry{response: response, err: err, retryable: retryable}
}

// TakeRecoveredDiscovery reports whether a previously failed root recovered
// since the last observation, clearing the flag (Rust #38420).
func (c *CapabilityDiscoveryCache) TakeRecoveredDiscovery() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	recovered := c.recovered
	c.recovered = false
	return recovered
}

func capabilityDiscoveryCacheKey(params *CapabilityRootsDiscoverParams) string {
	data, err := json.Marshal(params)
	if err != nil {
		return ""
	}
	return string(data)
}

func (e cachedDiscoveryEntry) Error() error {
	if e.err == nil {
		return nil
	}
	return e.err
}
