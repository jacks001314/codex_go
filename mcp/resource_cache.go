package mcp

import (
	"sort"
	"strings"
	"sync"
	"time"

	"codex_go/utils"
)

const (
	defaultMCPResourceCacheTTL        = 30 * time.Second
	defaultMCPResourceCacheMaxEntries = 128
)

type MCPResourceCacheOptions struct {
	TTL        time.Duration
	MaxEntries int
	Now        func() time.Time
}

type MCPResourceCacheKey struct {
	Server   string
	URI      string
	ThreadID string
	RootsKey string
}

type MCPResourceCache struct {
	mu    sync.Mutex
	now   func() time.Time
	ttl   time.Duration
	cache *utils.Cache[MCPResourceCacheKey, *mcpResourceCacheEntry]
}

type mcpResourceCacheEntry struct {
	expiresAt time.Time
	response  *MCPResourceReadResponse
}

func NewMCPResourceCache(options *MCPResourceCacheOptions) *MCPResourceCache {
	ttl := defaultMCPResourceCacheTTL
	maxEntries := defaultMCPResourceCacheMaxEntries
	now := time.Now
	if options != nil {
		if options.TTL > 0 {
			ttl = options.TTL
		}
		if options.MaxEntries > 0 {
			maxEntries = options.MaxEntries
		}
		if options.Now != nil {
			now = options.Now
		}
	}
	return &MCPResourceCache{
		now:   now,
		ttl:   ttl,
		cache: utils.New[MCPResourceCacheKey, *mcpResourceCacheEntry](maxEntries),
	}
}

func (c *MCPResourceCache) SetClock(clock func() time.Time) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if clock == nil {
		c.now = time.Now
		return
	}
	c.now = clock
}

func (c *MCPResourceCache) Read(key *MCPResourceCacheKey) (*MCPResourceReadResponse, bool) {
	if c == nil || key == nil {
		return nil, false
	}
	normalized := normalizeMCPResourceCacheKey(key)
	if normalized.Server == "" || normalized.URI == "" {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.cache.Get(normalized)
	if !ok || entry == nil {
		return nil, false
	}
	if !c.now().Before(entry.expiresAt) {
		c.cache.Remove(normalized)
		return nil, false
	}
	return cloneMCPResourceReadResponse(entry.response), true
}

func (c *MCPResourceCache) Write(key *MCPResourceCacheKey, response *MCPResourceReadResponse) {
	if c == nil || key == nil || response == nil {
		return
	}
	normalized := normalizeMCPResourceCacheKey(key)
	if normalized.Server == "" || normalized.URI == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache.Insert(normalized, &mcpResourceCacheEntry{
		expiresAt: c.now().Add(c.ttl),
		response:  cloneMCPResourceReadResponse(response),
	})
}

func (c *MCPResourceCache) Remove(key *MCPResourceCacheKey) {
	if c == nil || key == nil {
		return
	}
	normalized := normalizeMCPResourceCacheKey(key)
	if normalized.Server == "" || normalized.URI == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache.Remove(normalized)
}

func (c *MCPResourceCache) Clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache.Clear()
}

func (c *MCPResourceCache) Len() int {
	if c == nil {
		return 0
	}
	return c.cache.Len()
}

func normalizeMCPResourceCacheKey(key *MCPResourceCacheKey) MCPResourceCacheKey {
	if key == nil {
		return MCPResourceCacheKey{}
	}
	return MCPResourceCacheKey{
		Server:   strings.TrimSpace(key.Server),
		URI:      strings.TrimSpace(key.URI),
		ThreadID: strings.TrimSpace(key.ThreadID),
		RootsKey: strings.TrimSpace(key.RootsKey),
	}
}

func mcpRootsCacheKey(roots []MCPRoot) string {
	roots = cloneMCPRoots(roots)
	if len(roots) == 0 {
		return ""
	}
	parts := make([]string, 0, len(roots))
	for _, root := range roots {
		uri := strings.TrimSpace(root.URI)
		if uri == "" {
			continue
		}
		parts = append(parts, uri+"\x1f"+strings.TrimSpace(root.Name))
	}
	if len(parts) == 0 {
		return ""
	}
	sort.Strings(parts)
	return strings.Join(parts, "\x00")
}
