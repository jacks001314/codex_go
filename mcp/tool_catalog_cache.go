package mcp

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
	"time"
)

const (
	// toolCatalogCacheTTL mirrors Rust's TOOL_CATALOG_CACHE_TTL: a catalog older
	// than this is not reused, so a chat that starts much later starts the
	// server instead of trusting a stale tool list.
	toolCatalogCacheTTL = 30 * time.Minute
	// toolCatalogCacheCapacity mirrors Rust's TOOL_CATALOG_CACHE_CAPACITY.
	toolCatalogCacheCapacity = 32
	// MCPToolCatalogCacheCapability and MCPToolCatalogCacheableProperty mirror
	// the experimental capability Rust reads after initialize
	// (codex-rs/codex-mcp/src/rmcp_client.rs): a server that answers
	// `{"codex/tool-catalog-cache": {"cacheable": false}}` opts out and its
	// catalog is never reused.
	MCPToolCatalogCacheCapability   = "codex/tool-catalog-cache"
	MCPToolCatalogCacheableProperty = "cacheable"
)

// ToolCatalogIdentity scopes one cached catalog. The same server name with a
// different transport definition (command, args, env, cwd, url, headers) or a
// different credential authority must not share a catalog: the tools a server
// exposes depend on all of it.
type ToolCatalogIdentity struct {
	Server string
	// Transport is "<kind>:<sha1>" over the canonical transport definition.
	Transport string
}

// ToolCatalogCacheStore is the replaceable backing store for cached catalogs.
// The default implementation is process-scoped and in-memory, which is enough
// for a second chat in the same process to skip a server start; a later batch
// can implement this interface with disk persistence without touching the
// service.
type ToolCatalogCacheStore interface {
	// Entry returns the entry for identity, creating it on first use.
	Entry(identity ToolCatalogIdentity) *ToolCatalogCacheEntry
	// Drop forgets one identity.
	Drop(identity ToolCatalogIdentity)
	// Len reports how many identities are cached.
	Len() int
}

// toolCatalogSnapshot is one published catalog together with the moment it was
// published, so freshness is decided at read time.
type toolCatalogSnapshot struct {
	Tools       []MCPToolInfo
	Meta        any
	PublishedAt time.Time
	Revision    uint64
}

// ToolCatalogCacheEntry is the per-identity state. It mirrors Rust's
// ToolCatalogCacheState plus the fetch-generation counter:
//
//   - lastCatalogGeneration is the newest catalog published into the cache;
//   - lastModernDiscoveryGeneration records that a modern (discovery) handshake
//     was observed, which rules out an older legacy catalog for discovery
//     clients even though legacy clients may still reuse it (Rust #60037);
//   - disabledByServer is set when the server opts out of caching.
type ToolCatalogCacheEntry struct {
	mu                            sync.Mutex
	snapshot                      *toolCatalogSnapshot
	lastCatalogGeneration         uint64
	lastModernDiscoveryGeneration uint64
	nextFetchGeneration           uint64
	disabledByServer              bool
	lastUsed                      time.Time
}

// BeginFetch allocates the generation for one discovery attempt. Publishing
// with an older generation is ignored, so a slow fetch cannot overwrite a newer
// catalog.
func (e *ToolCatalogCacheEntry) BeginFetch(now time.Time) uint64 {
	if e == nil {
		return 0
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.nextFetchGeneration++
	e.lastUsed = now
	return e.nextFetchGeneration
}

// PublishIfNewest stores tools when generation is newer than every catalog
// already published. Tool annotations are dropped: they affect approval and
// parallelism decisions, so only a live connection may supply them.
func (e *ToolCatalogCacheEntry) PublishIfNewest(generation uint64, tools []MCPToolInfo, meta any, now time.Time) bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.disabledByServer || generation <= e.lastCatalogGeneration {
		return false
	}
	e.lastCatalogGeneration = generation
	copied := cloneMCPToolInfos(tools)
	for i := range copied {
		copied[i].Annotations = nil
	}
	e.snapshot = &toolCatalogSnapshot{
		Tools:       copied,
		Meta:        meta,
		PublishedAt: now,
		Revision:    generation,
	}
	e.lastUsed = now
	return true
}

// RecordModernDiscovery notes that a modern handshake was observed at
// generation. Discovery clients then reject any older catalog, while legacy
// clients may still reuse it (Rust #60037).
func (e *ToolCatalogCacheEntry) RecordModernDiscovery(generation uint64, now time.Time) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if generation > e.lastCatalogGeneration && generation > e.lastModernDiscoveryGeneration {
		e.lastModernDiscoveryGeneration = generation
		e.lastUsed = now
	}
}

// modernDiscoveryGeneration reports the generation at which the last modern
// handshake was observed. Readers decide with currentTools; this accessor exists
// so the ordering contract can be asserted directly.
func (e *ToolCatalogCacheEntry) modernDiscoveryGeneration() uint64 {
	if e == nil {
		return 0
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lastModernDiscoveryGeneration
}

// Disable marks the server as opted out: the snapshot is dropped and no later
// publish revives it.
func (e *ToolCatalogCacheEntry) Disable() {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.disabledByServer = true
	e.snapshot = nil
}

// Disabled reports whether the server opted out of catalog caching.
func (e *ToolCatalogCacheEntry) Disabled() bool {
	if e == nil {
		return true
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.disabledByServer
}

// CurrentTools returns the cached catalog when it is still usable. Nothing is
// returned once the server opted out, the snapshot expired, or a modern
// discovery ruled it out.
func (e *ToolCatalogCacheEntry) CurrentTools(now time.Time) ([]MCPToolInfo, bool) {
	return e.currentTools(now, false)
}

// currentTools is CurrentTools with the caller's discovery posture: a
// discover-local client must not reuse a catalog published before a modern
// handshake it already observed.
func (e *ToolCatalogCacheEntry) currentTools(now time.Time, beforeStdioDiscovery bool) ([]MCPToolInfo, bool) {
	if e == nil {
		return nil, false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.lastUsed = now
	if !e.acceptsLocked(now, beforeStdioDiscovery) {
		return nil, false
	}
	return cloneMCPToolInfos(e.snapshot.Tools), true
}

// HasTools reports whether a usable catalog is cached.
func (e *ToolCatalogCacheEntry) HasTools(now time.Time) bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.lastUsed = now
	return e.acceptsLocked(now, false)
}

func (e *ToolCatalogCacheEntry) acceptsLocked(now time.Time, beforeStdioDiscovery bool) bool {
	if e.disabledByServer || e.snapshot == nil {
		return false
	}
	if len(e.snapshot.Tools) == 0 {
		return false
	}
	if beforeStdioDiscovery && e.lastModernDiscoveryGeneration > e.lastCatalogGeneration {
		return false
	}
	return now.Sub(e.snapshot.PublishedAt) <= toolCatalogCacheTTL
}

// processToolCatalogCache is the in-memory, process-scoped store. Every MCP
// service in the process shares it, which is what lets a new chat reuse a
// catalog published by an earlier one.
type processToolCatalogCache struct {
	mu      sync.Mutex
	entries map[ToolCatalogIdentity]*ToolCatalogCacheEntry
}

func newProcessToolCatalogCache() *processToolCatalogCache {
	return &processToolCatalogCache{entries: map[ToolCatalogIdentity]*ToolCatalogCacheEntry{}}
}

func (c *processToolCatalogCache) Entry(identity ToolCatalogIdentity) *ToolCatalogCacheEntry {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.entries[identity]; ok {
		return entry
	}
	if len(c.entries) >= toolCatalogCacheCapacity {
		var oldestKey ToolCatalogIdentity
		var oldest time.Time
		for key, entry := range c.entries {
			entry.mu.Lock()
			used := entry.lastUsed
			entry.mu.Unlock()
			if oldest.IsZero() || used.Before(oldest) {
				oldestKey, oldest = key, used
			}
		}
		delete(c.entries, oldestKey)
	}
	entry := &ToolCatalogCacheEntry{}
	c.entries[identity] = entry
	return entry
}

func (c *processToolCatalogCache) Drop(identity ToolCatalogIdentity) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, identity)
}

func (c *processToolCatalogCache) Len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// defaultToolCatalogCacheStore is the process-wide store services share unless a
// test installs its own.
var defaultToolCatalogCacheStore ToolCatalogCacheStore = newProcessToolCatalogCache()

// mcpToolCatalogIdentity derives the cache identity for a server. It returns
// false for servers whose tool list depends on state this identity cannot
// capture, mirroring Rust's ToolCatalogIdentity::new:
//
//   - a headers helper resolves credentials dynamically;
//   - an OAuth-backed HTTP server exposes per-account tools;
//   - a remote-sourced environment variable carries executor-local state.
func mcpToolCatalogIdentity(name string, config *ServerConfig) (ToolCatalogIdentity, bool) {
	if config == nil {
		return ToolCatalogIdentity{}, false
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return ToolCatalogIdentity{}, false
	}
	if strings.TrimSpace(config.HTTPHeadersHelper) != "" {
		return ToolCatalogIdentity{}, false
	}
	if strings.TrimSpace(config.URL) != "" &&
		(strings.TrimSpace(config.OAuthClientID) != "" ||
			strings.TrimSpace(config.OAuthClientSecret) != "" ||
			config.OAuthCallbackPort != 0) {
		return ToolCatalogIdentity{}, false
	}
	for _, envVar := range config.EnvVars {
		if strings.EqualFold(strings.TrimSpace(envVar.Source), "remote") {
			return ToolCatalogIdentity{}, false
		}
	}
	transport := map[string]any{
		"command":           config.Command,
		"args":              config.Args,
		"env":               config.Env,
		"cwd":               config.CWD,
		"url":               config.URL,
		"bearerTokenEnvVar": config.BearerTokenEnvVar,
		"httpHeaders":       config.HTTPHeaders,
		"envHttpHeaders":    config.EnvHTTPHeaders,
	}
	encoded, err := json.Marshal(transport)
	if err != nil {
		return ToolCatalogIdentity{}, false
	}
	sum := sha1.Sum(encoded)
	kind := "stdio"
	if strings.TrimSpace(config.URL) != "" {
		kind = "http"
	}
	return ToolCatalogIdentity{Server: name, Transport: kind + ":" + hex.EncodeToString(sum[:])}, true
}

// mcpServerOptsOutOfToolCatalogCache reads the server's initialize capability
// that disables catalog reuse.
func mcpServerOptsOutOfToolCatalogCache(capabilities any) bool {
	raw, err := json.Marshal(capabilities)
	if err != nil {
		return false
	}
	var decoded struct {
		Experimental map[string]json.RawMessage `json:"experimental"`
	}
	if json.Unmarshal(raw, &decoded) != nil {
		return false
	}
	entry, ok := decoded.Experimental[MCPToolCatalogCacheCapability]
	if !ok {
		return false
	}
	var properties map[string]any
	if json.Unmarshal(entry, &properties) != nil {
		return false
	}
	cacheable, ok := properties[MCPToolCatalogCacheableProperty].(bool)
	return ok && !cacheable
}
