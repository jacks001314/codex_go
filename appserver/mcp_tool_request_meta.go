package appserver

import (
	"strings"

	"codex_go/mcp"
)

// SetThreadMCPToolRequestMetaHook installs the host-scoped MCP request metadata
// hook for one thread (the Go seam for Rust's thread extension data). Passing
// nil removes the thread override so the router default applies.
func (r *RuntimeRouter) SetThreadMCPToolRequestMetaHook(threadID string, hook *mcp.MCPToolRequestMetaHook) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	r.mcpToolRequestMetaMu.Lock()
	defer r.mcpToolRequestMetaMu.Unlock()
	if r.mcpToolRequestMetaHooks == nil {
		r.mcpToolRequestMetaHooks = map[string]*mcp.MCPToolRequestMetaHook{}
	}
	if hook == nil {
		delete(r.mcpToolRequestMetaHooks, threadID)
		return
	}
	r.mcpToolRequestMetaHooks[threadID] = hook
}

// threadMCPToolRequestMetaHook resolves the hook installed for a thread,
// falling back to the router-wide host selection.
func (r *RuntimeRouter) threadMCPToolRequestMetaHook(threadID string) *mcp.MCPToolRequestMetaHook {
	if r == nil {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	r.mcpToolRequestMetaMu.Lock()
	defer r.mcpToolRequestMetaMu.Unlock()
	if hook := r.mcpToolRequestMetaHooks[threadID]; hook != nil {
		return hook
	}
	return r.mcpToolRequestMetaDefault
}
