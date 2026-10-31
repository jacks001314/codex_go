package mcp

import "sync"

// clientToolCatalog stores the tool catalog state owned by one live MCP
// connection, mirroring Rust #43031's per-client ClientToolCatalog. Reusing a
// connection preserves the published catalog; a replacement connection starts a
// new catalog at revision zero. The read lock lets an in-flight call hold
// catalog authority until execution finishes, so a refresh publishes only after
// calls that were prepared against the current revision complete.
type clientToolCatalog struct {
	mu       sync.RWMutex
	revision uint64
	tools    []MCPToolInfo
	// refresh serializes fetches without blocking calls against the current
	// catalog.
	refreshMu sync.Mutex
}

func newClientToolCatalog(tools []MCPToolInfo) *clientToolCatalog {
	return &clientToolCatalog{tools: cloneMCPToolInfos(tools)}
}

// read runs read against a consistent snapshot of the catalog.
func (c *clientToolCatalog) read(read func(revision uint64, tools []MCPToolInfo)) {
	if c == nil || read == nil {
		return
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	read(c.revision, c.tools)
}

func (c *clientToolCatalog) currentRevision() uint64 {
	if c == nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.revision
}

// listedTools returns the catalog's current exact inventory.
func (c *clientToolCatalog) listedTools() []MCPToolInfo {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return cloneMCPToolInfos(c.tools)
}

// refresh serializes fetching and publication. The current catalog stays
// readable during the fetch, and a successful fetch advances the revision.
func (c *clientToolCatalog) refresh(fetch func() ([]MCPToolInfo, error)) error {
	if c == nil {
		return nil
	}
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	tools, err := fetch()
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tools = cloneMCPToolInfos(tools)
	c.revision++
	return nil
}

// runWithRevision rejects a call prepared against another revision before it
// runs, and holds catalog authority until run returns.
func (c *clientToolCatalog) runWithRevision(expected uint64, run func() error) (bool, error) {
	if c == nil {
		return true, run()
	}
	c.mu.RLock()
	if c.revision != expected {
		c.mu.RUnlock()
		return false, nil
	}
	defer c.mu.RUnlock()
	return true, run()
}

func cloneMCPToolInfos(tools []MCPToolInfo) []MCPToolInfo {
	if len(tools) == 0 {
		return nil
	}
	return append([]MCPToolInfo(nil), tools...)
}
