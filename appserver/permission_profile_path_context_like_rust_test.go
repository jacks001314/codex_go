package appserver

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/config"
	"codex_go/sandbox"
)

// Mirrors Rust #44676: profile availability resolves configured workspace
// roots against the cwd supplied by the permissionProfile/list request.
func TestPermissionProfileListResolvesRootsAgainstRequestedCWDLikeRust(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	unsafeCWD := filepath.Join(workspace, "[workspace]")
	if err := os.MkdirAll(unsafeCWD, 0o755); err != nil {
		t.Fatalf("MkdirAll(unsafe cwd) error = %v", err)
	}
	document := `default_permissions = ":workspace"

[permissions.scoped.workspace_roots]
"private/*.env" = true

[permissions.scoped.filesystem]
":workspace_roots" = "write"
`
	if err := os.WriteFile(config.ConfigPath(home), []byte(document), 0o600); err != nil {
		t.Fatalf("WriteFile(config.toml) error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home)})

	for _, testCase := range []struct {
		cwd     string
		allowed bool
	}{
		{cwd: unsafeCWD, allowed: false},
		{cwd: workspace, allowed: true},
	} {
		response := router.Handle(requestWithParams(t, IntID(1), MethodPermissionProfileList, sandbox.PermissionProfileListParams{
			CWD: &testCase.cwd,
		}))
		if response.Error != nil {
			t.Fatalf("permissionProfile/list(cwd=%q) error = %+v", testCase.cwd, response.Error)
		}
		list := response.Result.(*sandbox.PermissionProfileListResponse)
		var found bool
		for _, profile := range list.Data {
			if profile.ID != "scoped" {
				continue
			}
			found = true
			if profile.Allowed != testCase.allowed {
				t.Fatalf("profile scoped allowed for cwd %q = %v, want %v", testCase.cwd, profile.Allowed, testCase.allowed)
			}
		}
		if !found {
			t.Fatalf("profile scoped missing from catalog: %#v", list.Data)
		}
	}
}
