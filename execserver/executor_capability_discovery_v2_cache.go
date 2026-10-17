package execserver

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
)

// MIN_CACHED_DISCOVERY_CONTEXTS bounds the V2 inventory cache without forcing
// eviction for ordinary environment selections (Rust
// `MIN_CACHED_DISCOVERY_CONTEXTS`).
const minCachedDiscoveryContexts = 16

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

type executorCapabilityDiscoveryV2CacheEntry struct {
	environmentID string
	instanceID    string
	sessionID     string
	requestKey    string

	mu           sync.Mutex
	loading      bool
	waiting      int
	loaded       bool
	wait         chan struct{}
	response     *DiscoverV2CapabilitiesResponse
	err          error
	retryable    bool
	sawRetryable bool
}

// ExecutorCapabilityDiscoveryV2Cache caches V2 inventories by environment
// instance, executor session, cwd, and sandbox. It coalesces concurrent loads,
// retains non-retryable failures, and retries retryable failures on a later
// call. Each turn owns a fresh cache; in-flight loads retain their entry.
type ExecutorCapabilityDiscoveryV2Cache struct {
	mu      sync.Mutex
	entries []*executorCapabilityDiscoveryV2CacheEntry
	// capacity never shrinks until the cache is replaced.
	capacity  int
	recovered atomic.Bool
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

// TakeRecoveredDiscovery reports whether a failed discovery recovered since the
// last observation.
func (c *ExecutorCapabilityDiscoveryV2Cache) TakeRecoveredDiscovery() bool {
	if c == nil {
		return false
	}
	return c.recovered.Swap(false)
}

// Discover loads one inventory through the cache. The loader is called at most
// once for concurrent callers with the same key.
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
	entry := c.entryFor(environmentID, target.CapabilityDiscoveryInstanceID(), sessionID, key)
	return c.load(ctx, entry, target, request)
}

func (c *ExecutorCapabilityDiscoveryV2Cache) entryFor(environmentID string, instanceID string, sessionID string, requestKey string) *executorCapabilityDiscoveryV2CacheEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, entry := range c.entries {
		if entry.environmentID == environmentID && entry.instanceID == instanceID &&
			entry.sessionID == sessionID && entry.requestKey == requestKey {
			return entry
		}
	}
	var retryable bool
	for _, entry := range c.entries {
		if entry.environmentID == environmentID && entry.instanceID == instanceID &&
			entry.requestKey == requestKey {
			entry.mu.Lock()
			retryable = entry.retryable || entry.sawRetryable
			entry.mu.Unlock()
			break
		}
	}
	entry := &executorCapabilityDiscoveryV2CacheEntry{
		environmentID: environmentID,
		instanceID:    instanceID,
		sessionID:     sessionID,
		requestKey:    requestKey,
		retryable:     retryable,
		sawRetryable:  retryable,
	}
	capacity := c.capacity
	if capacity < minCachedDiscoveryContexts {
		capacity = minCachedDiscoveryContexts
	}
	if len(c.entries) == capacity {
		c.entries = c.entries[1:]
	}
	c.entries = append(c.entries, entry)
	return entry
}

func (c *ExecutorCapabilityDiscoveryV2Cache) load(ctx context.Context, entry *executorCapabilityDiscoveryV2CacheEntry, target ExecutorCapabilityDiscoveryV2Target, request *DiscoverV2CapabilitiesParams) (*DiscoverV2CapabilitiesResponse, error) {
	for {
		entry.mu.Lock()
		if entry.loading || entry.waiting > 0 {
			wait := entry.wait
			if wait == nil {
				entry.mu.Unlock()
				continue
			}
			entry.waiting++
			entry.mu.Unlock()
			<-wait
			entry.mu.Lock()
			entry.waiting--
			response, err := entry.response, entry.err
			entry.mu.Unlock()
			return response, err
		}
		if entry.loaded && !entry.retryable {
			response, err := entry.response, entry.err
			entry.mu.Unlock()
			return response, err
		}
		entry.loading = true
		entry.loaded = false
		entry.wait = make(chan struct{})
		entry.response = nil
		entry.err = nil
		entry.mu.Unlock()

		response, err := target.DiscoverCapabilitiesV2(ctx, request)
		canceled := ctx != nil && ctx.Err() != nil
		retryable := canceled || IsRetryableRecoveryError(err)

		entry.mu.Lock()
		entry.response = response
		entry.err = err
		entry.retryable = retryable
		entry.loaded = !retryable
		if err == nil {
			if entry.sawRetryable {
				c.recovered.Store(true)
			}
			entry.sawRetryable = false
		} else if retryable {
			entry.sawRetryable = true
		}
		entry.loading = false
		close(entry.wait)
		entry.mu.Unlock()
		return response, err
	}
}

func executorCapabilityDiscoveryV2RequestKey(request *DiscoverV2CapabilitiesParams) (string, error) {
	encoded, err := json.Marshal(request)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}
