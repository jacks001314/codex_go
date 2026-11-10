package appserver

import (
	"path/filepath"
	"testing"

	"codex_go/sandbox"
)

// Rust #60513 marks the MCP runtime dirty with
// `McpRefreshReason::SessionPermissionsGranted` after a session-scoped grant so
// the next runtime rebuild sees it. Go keeps no cached MCP runtime: every turn
// re-lists MCP server tools and every call reads the grants through
// grantedPermissionsForEnvironment, so the grant applies to later turns without
// a refresh step or a reason label.
func TestSessionPermissionGrantAppliesToLaterTurnsWithoutMcpRuntimeRefreshLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	sessionPath := filepath.Join(t.TempDir(), "granted")
	router.recordPermissionGrant("thread-1", "turn-1", "local", string(PermissionGrantScopeSession),
		&sandbox.AdditionalPermissionProfile{FileSystem: []string{sessionPath}})

	for _, turnID := range []string{"turn-1", "turn-2", "turn-3"} {
		granted := router.grantedPermissionsForEnvironment("thread-1", turnID, "local")
		if granted == nil || len(granted.FileSystem) != 1 || granted.FileSystem[0] != sessionPath {
			t.Fatalf("granted permissions for %s = %#v, want the session grant", turnID, granted)
		}
	}

	// Turn-scoped grants keep expiring while the session grant stays visible.
	turnPath := filepath.Join(t.TempDir(), "turn-only")
	router.recordPermissionGrant("thread-1", "turn-2", "local", string(PermissionGrantScopeTurn),
		&sandbox.AdditionalPermissionProfile{ReadFileSystem: []string{turnPath}})
	router.clearTurnPermissionGrants("thread-1", "turn-2")
	granted := router.grantedPermissionsForEnvironment("thread-1", "turn-3", "local")
	if granted == nil || len(granted.FileSystem) != 1 || len(granted.ReadFileSystem) != 0 {
		t.Fatalf("granted permissions after turn clear = %#v, want only the session grant", granted)
	}
}
