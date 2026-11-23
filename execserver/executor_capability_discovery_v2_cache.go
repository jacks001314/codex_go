package execserver

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// MIN_CACHED_DISCOVERY_CONTEXTS bounds the V2 inventory cache without forcing
// eviction for ordinary environment selections (Rust
// `MIN_CACHED_DISCOVERY_CONTEXTS`).
const minCachedDiscoveryContexts = 16

// Failed discovery loads are cached per context and retried on the first
// lookup after their cooldown. Consecutive failures double the delay from one
// second up to thirty seconds (Rust #62679).
const (
	initialDiscoveryRetryDelay = time.Second
	maxDiscoveryRetryDelay     = 30 * time.Second
)

// ExecutorCapabilityDiscoveryV2Snapshot is one environment's caller-bound V2
// inventory for a model step (Rust `ExecutorCapabilityDiscoveryV2Snapshot`).
// Exactly one of Response and Err is populated.
type ExecutorCapabilityDiscoveryV2Snapshot struct {
	EnvironmentID string
	Response      *DiscoverV2CapabilitiesResponse
	Err           string
}

// CloneExecutorCapabilityDiscoveryV2Snapshots deep-copies snapshots so one
// consumer cannot mutate another step's captured inventory.
func CloneExecutorCapabilityDiscoveryV2Snapshots(snapshots []ExecutorCapabilityDiscoveryV2Snapshot) []ExecutorCapabilityDiscoveryV2Snapshot {
	if snapshots == nil {
		return nil
	}
	out := make([]ExecutorCapabilityDiscoveryV2Snapshot, len(snapshots))
	for i := range snapshots {
		out[i] = cloneExecutorCapabilityDiscoveryV2Snapshot(snapshots[i])
	}
	return out
}

func cloneExecutorCapabilityDiscoveryV2Snapshot(snapshot ExecutorCapabilityDiscoveryV2Snapshot) ExecutorCapabilityDiscoveryV2Snapshot {
	clone := snapshot
	if snapshot.Response != nil {
		response := *snapshot.Response
		response.Plugins = cloneExecutorPlugins(snapshot.Response.Plugins)
		response.Skills = cloneExecutorSkills(snapshot.Response.Skills)
		response.Warnings = append([]string(nil), snapshot.Response.Warnings...)
		clone.Response = &response
	}
	return clone
}

// ExecutorCapabilityDiscoveryV2SnapshotSameDiscovery compares discovery
// identity rather than inventory contents (Rust
// `ExecutorCapabilityDiscoveryV2Snapshot::same_discovery`). Successful loads
// compare by the cached response identity so a cache hit is unchanged and a
// reload or recovery is a change; repeated errors compare by message.
func ExecutorCapabilityDiscoveryV2SnapshotSameDiscovery(previous, current ExecutorCapabilityDiscoveryV2Snapshot) bool {
	if previous.EnvironmentID != current.EnvironmentID {
		return false
	}
	switch {
	case previous.Response != nil && current.Response != nil:
		return previous.Response == current.Response
	case previous.Response == nil && current.Response == nil:
		return previous.Err == current.Err
	default:
		return false
	}
}

func cloneExecutorPlugins(plugins []ExecutorPlugin) []ExecutorPlugin {
	if plugins == nil {
		return nil
	}
	out := make([]ExecutorPlugin, len(plugins))
	for i := range plugins {
		out[i] = plugins[i]
		out[i].Files = cloneDiscoveredPluginFiles(plugins[i].Files)
		out[i].Skills = cloneExecutorSkills(plugins[i].Skills)
		if plugins[i].RemotePluginID != nil {
			value := *plugins[i].RemotePluginID
			out[i].RemotePluginID = &value
		}
	}
	return out
}

func cloneDiscoveredPluginFiles(files DiscoveredPluginFiles) DiscoveredPluginFiles {
	out := files
	if files.MCPConfig != nil {
		copy := *files.MCPConfig
		out.MCPConfig = &copy
	}
	if files.AppsConfig != nil {
		copy := *files.AppsConfig
		out.AppsConfig = &copy
	}
	return out
}

func cloneExecutorSkills(skills []ExecutorSkill) []ExecutorSkill {
	if skills == nil {
		return nil
	}
	out := make([]ExecutorSkill, len(skills))
	for i := range skills {
		out[i] = skills[i]
		if skills[i].ShortDescription != nil {
			value := *skills[i].ShortDescription
			out[i].ShortDescription = &value
		}
		if skills[i].DiscoveryRoot != nil {
			root := *skills[i].DiscoveryRoot
			out[i].DiscoveryRoot = &root
		}
	}
	return out
}

// ExecutorCapabilityDiscoveryV2Target identifies one live environment instance
// and performs its discovery request. SessionID changes only when the executor
// session is replaced; transport recovery preserves it.
type ExecutorCapabilityDiscoveryV2Target interface {
	CapabilityDiscoveryInstanceID() string
	CapabilityDiscoverySessionID(context.Context) (string, error)
	DiscoverCapabilitiesV2(context.Context, *DiscoverV2CapabilitiesParams) (*DiscoverV2CapabilitiesResponse, error)
}

// executorCapabilityDiscoveryV2Failure records one context's cached discovery
// error and the earliest time a later lookup may retry it (Rust
// `DiscoveryV2Failure`).
type executorCapabilityDiscoveryV2Failure struct {
	err         error
	attempts    uint32
	nextRetryAt time.Time
}

type executorCapabilityDiscoveryV2CacheEntry struct {
	environmentID string
	instanceID    string
	sessionID     string
	requestKey    string
	// forceReload is sent with this entry's discovery request so the executor
	// ignores its own watcher-backed snapshot. An invalidated entry keeps it
	// through retries.
	forceReload bool

	mu       sync.Mutex
	loading  bool
	wait     chan struct{}
	response *DiscoverV2CapabilitiesResponse
	failure  *executorCapabilityDiscoveryV2Failure
}

// forceReloadEntry discards a cached snapshot and cooldown while retaining the
// force-reload request (Rust `DiscoveryV2CacheEntry::force_reload`). Readers
// holding the replaced entry keep their result.
func (e *executorCapabilityDiscoveryV2CacheEntry) forceReloadEntry() *executorCapabilityDiscoveryV2CacheEntry {
	if e == nil {
		return nil
	}
	return &executorCapabilityDiscoveryV2CacheEntry{
		environmentID: e.environmentID,
		instanceID:    e.instanceID,
		sessionID:     e.sessionID,
		requestKey:    e.requestKey,
		forceReload:   true,
	}
}

// ExecutorCapabilityDiscoveryV2Cache caches V2 inventories by environment
// instance, executor session, cwd, and sandbox. It coalesces concurrent loads
// and caches failed loads per context: lookups during the failure's cooldown
// return the cached error without issuing a request, and the first lookup after
// it retries with a doubling delay. The cache lives with its thread and is
// reused across turns; Invalidate replaces every entry with a forced reload
// while in-flight readers keep the result they already captured (Rust #62679).
type ExecutorCapabilityDiscoveryV2Cache struct {
	mu      sync.Mutex
	entries []*executorCapabilityDiscoveryV2CacheEntry
	// capacity never shrinks until the cache is replaced.
	capacity int
	// now reports the wall clock for cooldown deadlines; tests replace it.
	now func() time.Time
}

// EnsureCapacity reserves room for a complete environment selection.
func (c *ExecutorCapabilityDiscoveryV2Cache) EnsureCapacity(minimum int) {
	if c == nil {
		return
	}
	c.mu.Lock()
	if minimum > c.capacity {
		c.capacity = minimum
	}
	c.mu.Unlock()
}

// Invalidate makes future lookups reload every context without interrupting
// loads that are already running (Rust #62679
// ExecutorCapabilityDiscoveryV2Cache::invalidate). The next lookup starts a
// forced load immediately, without a failure cooldown.
func (c *ExecutorCapabilityDiscoveryV2Cache) Invalidate() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for index, entry := range c.entries {
		c.entries[index] = entry.forceReloadEntry()
	}
}

// Discover loads one inventory through the cache. The loader is called at most
// once for concurrent callers with the same key; failed loads are shared until
// their cooldown expires.
func (c *ExecutorCapabilityDiscoveryV2Cache) Discover(ctx context.Context, environmentID string, target ExecutorCapabilityDiscoveryV2Target, request *DiscoverV2CapabilitiesParams) (*DiscoverV2CapabilitiesResponse, error) {
	if target == nil {
		return nil, errors.New("executor discovery V2 target is nil")
	}
	sessionID, err := target.CapabilityDiscoverySessionID(ctx)
	if err != nil {
		return nil, errors.New("executor discovery V2 failed")
	}
	key, err := executorCapabilityDiscoveryV2RequestKey(request)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return target.DiscoverCapabilitiesV2(ctx, request)
	}
	entry := c.entryFor(environmentID, target.CapabilityDiscoveryInstanceID(), sessionID, key, request != nil && request.ForceReload)
	return c.load(ctx, entry, target, request)
}

func (c *ExecutorCapabilityDiscoveryV2Cache) entryFor(environmentID string, instanceID string, sessionID string, requestKey string, forceReload bool) *executorCapabilityDiscoveryV2CacheEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !forceReload {
		for _, entry := range c.entries {
			if entry.environmentID == environmentID && entry.instanceID == instanceID &&
				entry.sessionID == sessionID && entry.requestKey == requestKey {
				return entry
			}
		}
	} else {
		for index, entry := range c.entries {
			if entry.environmentID != environmentID || entry.instanceID != instanceID ||
				entry.sessionID != sessionID || entry.requestKey != requestKey {
				continue
			}
			// Rust #62679: a forced context replaces its entry with a fresh load;
			// readers holding the old entry keep its result.
			c.entries = append(c.entries[:index], c.entries[index+1:]...)
			break
		}
	}
	entry := &executorCapabilityDiscoveryV2CacheEntry{
		environmentID: environmentID,
		instanceID:    instanceID,
		sessionID:     sessionID,
		requestKey:    requestKey,
		forceReload:   forceReload,
	}
	c.appendEntryLocked(entry)
	return entry
}

// appendEntryLocked stores a new entry, evicting the oldest context once the
// cache reaches capacity. Callers hold c.mu.
func (c *ExecutorCapabilityDiscoveryV2Cache) appendEntryLocked(entry *executorCapabilityDiscoveryV2CacheEntry) {
	capacity := c.capacity
	if capacity < minCachedDiscoveryContexts {
		capacity = minCachedDiscoveryContexts
	}
	if len(c.entries) >= capacity {
		c.entries = c.entries[1:]
	}
	c.entries = append(c.entries, entry)
}

func (c *ExecutorCapabilityDiscoveryV2Cache) clock() time.Time {
	if c != nil && c.now != nil {
		return c.now()
	}
	return time.Now()
}

// discoveryRetryDelay doubles the initial delay per consecutive failure up to
// the maximum (Rust INITIAL_DISCOVERY_RETRY_DELAY / MAX_DISCOVERY_RETRY_DELAY).
func discoveryRetryDelay(attempts uint32) time.Duration {
	delay := initialDiscoveryRetryDelay
	for attempt := uint32(1); attempt < attempts; attempt++ {
		if delay >= maxDiscoveryRetryDelay {
			break
		}
		delay *= 2
	}
	if delay > maxDiscoveryRetryDelay {
		return maxDiscoveryRetryDelay
	}
	return delay
}

func (c *ExecutorCapabilityDiscoveryV2Cache) load(ctx context.Context, entry *executorCapabilityDiscoveryV2CacheEntry, target ExecutorCapabilityDiscoveryV2Target, request *DiscoverV2CapabilitiesParams) (*DiscoverV2CapabilitiesResponse, error) {
	if entry == nil {
		return nil, errors.New("executor discovery V2 cache entry is nil")
	}
	for {
		entry.mu.Lock()
		if entry.loading {
			wait := entry.wait
			entry.mu.Unlock()
			<-wait
			continue
		}
		if entry.response != nil {
			response := entry.response
			entry.mu.Unlock()
			return response, nil
		}
		// Waiting callers must not immediately retry a failed request; the
		// cooldown is checked again after every wake-up.
		if failure := entry.failure; failure != nil && c.clock().Before(failure.nextRetryAt) {
			err := failure.err
			entry.mu.Unlock()
			return nil, err
		}
		entry.loading = true
		entry.wait = make(chan struct{})
		entry.mu.Unlock()

		loadRequest := *request
		loadRequest.ForceReload = entry.forceReload
		response, err := target.DiscoverCapabilitiesV2(ctx, &loadRequest)
		// Cancellation drops the load without starting a cooldown (Rust #62679).
		canceled := ctx != nil && ctx.Err() != nil

		entry.mu.Lock()
		switch {
		case err == nil:
			entry.response = response
			entry.failure = nil
		case canceled:
			entry.response = nil
		default:
			attempts := uint32(1)
			if entry.failure != nil {
				attempts = entry.failure.attempts + 1
			}
			entry.failure = &executorCapabilityDiscoveryV2Failure{
				err:         err,
				attempts:    attempts,
				nextRetryAt: c.clock().Add(discoveryRetryDelay(attempts)),
			}
			entry.response = nil
		}
		wait := entry.wait
		entry.wait = nil
		entry.loading = false
		close(wait)
		entry.mu.Unlock()
		return response, err
	}
}

func executorCapabilityDiscoveryV2RequestKey(request *DiscoverV2CapabilitiesParams) (string, error) {
	if request == nil {
		return "", nil
	}
	// Reload controls how the entry is populated, not which inventory it
	// identifies (Rust #62675 DiscoveryCacheKey).
	keyRequest := *request
	keyRequest.ForceReload = false
	encoded, err := json.Marshal(&keyRequest)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}
