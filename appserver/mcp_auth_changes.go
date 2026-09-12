package appserver

import "codex_go/mcp"

// routerMCPAuthChangeSource forwards this router's auth change revisions to MCP
// services so opted-in stdio servers can invalidate cached credentials without
// a reconnect (Rust codex-mcp auth_changes, #43428).
type routerMCPAuthChangeSource struct {
	router *RuntimeRouter
}

func (s routerMCPAuthChangeSource) AuthChangeState() mcp.MCPAuthChangeState {
	if s.router == nil || s.router.authChangeTracker == nil {
		return mcp.MCPAuthChangeState{}
	}
	state := s.router.authChangeTracker.Snapshot()
	return mcp.MCPAuthChangeState{
		Generation:      state.Generation,
		OwnerGeneration: state.OwnerGeneration,
	}
}

func (s routerMCPAuthChangeSource) AuthChanged() <-chan struct{} {
	if s.router == nil || s.router.authChangeTracker == nil {
		return nil
	}
	return s.router.authChangeTracker.Changed()
}
