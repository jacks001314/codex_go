package appserver

import (
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/sandbox"
	"codex_go/turn"
)

// Rust #60461: approved filesystem access makes executor skills discoverable.
// A turn's discovery sees session plus turn grants, while a turn-less (session
// inventory) discovery sees only session-scope grants.
func TestExecutorSkillSandboxContextsCarryGrantScopesLikeRust(t *testing.T) {
	cwd := t.TempDir()
	sessionDir := t.TempDir()
	turnDir := t.TempDir()
	cfg := &config.Config{Values: map[string]any{
		"default_permissions": "restricted",
		"permissions": map[string]any{
			"restricted": map[string]any{"filesystem": map[string]any{":minimal": "read"}},
		},
	}}
	router := NewRuntimeRouter(RuntimeServices{DefaultCWD: cwd})
	router.recordPermissionGrant("thread-grant", "turn-1", "local", string(PermissionGrantScopeSession),
		&sandbox.AdditionalPermissionProfile{FileSystem: []string{sessionDir}})
	router.recordPermissionGrant("thread-grant", "turn-2", "local", string(PermissionGrantScopeTurn),
		&sandbox.AdditionalPermissionProfile{FileSystem: []string{turnDir}})
	params := &turn.TurnStartParams{RuntimeWorkspaceRoots: []string{cwd}}
	// Sandbox permission entries encode paths as URI-style strings, so compare
	// on the slash-normalized native path.
	sessionPath := strings.ReplaceAll(executorEnvironmentNativePath(sessionDir), "\\", "/")
	turnPath := strings.ReplaceAll(executorEnvironmentNativePath(turnDir), "\\", "/")

	withTurn, err := router.executorSkillSandboxContextsForTurn(cfg, cwd, "thread-grant", "turn-2", params)
	if err != nil {
		t.Fatalf("turn discovery error = %v", err)
	}
	withTurnPermissions := string(withTurn["local"].Permissions)
	if !strings.Contains(withTurnPermissions, sessionPath) || !strings.Contains(withTurnPermissions, turnPath) {
		t.Fatalf("turn discovery permissions missing grants: %s", withTurnPermissions)
	}

	withoutTurn, err := router.executorSkillSandboxContextsForTurn(cfg, cwd, "thread-grant", "", params)
	if err != nil {
		t.Fatalf("session discovery error = %v", err)
	}
	sessionPermissions := string(withoutTurn["local"].Permissions)
	if !strings.Contains(sessionPermissions, sessionPath) {
		t.Fatalf("session discovery permissions missing the session grant: %s", sessionPermissions)
	}
	if strings.Contains(sessionPermissions, turnPath) {
		t.Fatalf("turn-scoped grant leaked into session discovery: %s", sessionPermissions)
	}
}

func TestSessionGrantedPermissionsExcludeTurnScopesLikeRust(t *testing.T) {
	sessionDir := t.TempDir()
	turnDir := t.TempDir()
	router := NewRuntimeRouter(RuntimeServices{})
	router.recordPermissionGrant("thread-1", "turn-1", "remote", string(PermissionGrantScopeSession),
		&sandbox.AdditionalPermissionProfile{FileSystem: []string{sessionDir}})
	router.recordPermissionGrant("thread-1", "turn-1", "remote", string(PermissionGrantScopeTurn),
		&sandbox.AdditionalPermissionProfile{ReadFileSystem: []string{turnDir}})

	session := router.sessionGrantedPermissionsForEnvironment("thread-1", "remote")
	if session == nil || len(session.FileSystem) != 1 || session.FileSystem[0] != sessionDir {
		t.Fatalf("session grants = %#v", session)
	}
	if len(session.ReadFileSystem) != 0 {
		t.Fatalf("session grants leaked a turn read grant: %#v", session)
	}

	turnScoped := router.skillDiscoveryGrantedPermissions("thread-1", "turn-1", "remote")
	if turnScoped == nil || len(turnScoped.FileSystem) != 1 || len(turnScoped.ReadFileSystem) != 1 {
		t.Fatalf("turn discovery grants = %#v", turnScoped)
	}
	noTurn := router.skillDiscoveryGrantedPermissions("thread-1", "", "remote")
	if noTurn == nil || len(noTurn.ReadFileSystem) != 0 {
		t.Fatalf("turn-less discovery grants = %#v", noTurn)
	}
}
