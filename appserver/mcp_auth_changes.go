package appserver

import (
	"fmt"

	"codex_go/mcp"
)

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

// MCPConnectionAuthScope keeps a pooled stdio connection private to this
// router's credential source (Rust #57211 ConnectionKey::auth_state): the
// tracker identity stands in for Arc::ptr_eq, and owner plus login generations
// survive credential refreshes but change with the credential source.
func (s routerMCPAuthChangeSource) MCPConnectionAuthScope() string {
	if s.router == nil || s.router.authChangeTracker == nil {
		return ""
	}
	state := s.router.authChangeTracker.Snapshot()
	return fmt.Sprintf("auth=%p|owner=%d|login=%d", s.router.authChangeTracker, state.OwnerGeneration, state.LoginGeneration)
}

func (s routerMCPAuthChangeSource) AuthChanged() <-chan struct{} {
	if s.router == nil || s.router.authChangeTracker == nil {
		return nil
	}
	return s.router.authChangeTracker.Changed()
}
