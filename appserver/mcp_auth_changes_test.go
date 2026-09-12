package appserver

import (
	"testing"

	"codex_go/auth"
	"codex_go/mcp"
)

// TestRuntimeRouterWiresMCPAuthChangeSource covers Rust #43428's app-server
// wiring: every MCP service gets a source that reports the router's auth
// revisions and signals each change.
func TestRuntimeRouterWiresMCPAuthChangeSource(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	service := router.requireMCP()
	source := service.AuthChangeSource()
	if source == nil {
		t.Fatal("MCP service has no auth change source")
	}
	if state := source.AuthChangeState(); state != (mcp.MCPAuthChangeState{}) {
		t.Fatalf("initial auth change state = %+v, want zero", state)
	}

	// A same-owner token refresh advances the generation and signals a change.
	snapshot := func(user string, workspace string, token string) *auth.AuthDotJSON {
		return &auth.AuthDotJSON{
			AuthMode: "chatgpt",
			Tokens: map[string]any{
				"access_token":    token,
				"chatgpt_user_id": user,
				"account_id":      workspace,
			},
		}
	}
	router.requireAccount().ApplyAuthSnapshot(snapshot("user-a", "workspace-a", "token-1"))
	router.noteAuthChanged()
	if state := source.AuthChangeState(); state.Generation != 1 || state.OwnerGeneration != 1 {
		t.Fatalf("first login state = %+v, want {1 1}", state)
	}

	changed := source.AuthChanged()
	router.requireAccount().ApplyAuthSnapshot(snapshot("user-a", "workspace-a", "token-2"))
	router.noteAuthChanged()
	select {
	case <-changed:
	default:
		t.Fatal("auth change channel was not signalled on a token refresh")
	}
	if state := source.AuthChangeState(); state.Generation != 2 || state.OwnerGeneration != 1 {
		t.Fatalf("token refresh state = %+v, want {2 1}", state)
	}
}
