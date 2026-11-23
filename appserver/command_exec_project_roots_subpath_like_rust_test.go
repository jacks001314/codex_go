package appserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/sandbox"
	"codex_go/tool"
)

// Mirrors Rust #62183: a `:workspace_roots` write grant scoped to a subpath
// resolves against the command's working directory, so the granted
// subdirectory is writable while a sibling file and the matching subdirectory
// under the parent stay outside the grant.
func TestRuntimeRouterCommandExecProjectRootsSubpathGrantUsesCommandCWDLikeRust(t *testing.T) {
	home := t.TempDir()
	defaultCWD := t.TempDir()
	commandDir := filepath.Join(defaultCWD, "command-cwd")
	grantedDir := filepath.Join(commandDir, "granted")
	parentGranted := filepath.Join(defaultCWD, "granted")
	if err := os.MkdirAll(grantedDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(granted) error = %v", err)
	}
	if err := os.MkdirAll(parentGranted, 0o700); err != nil {
		t.Fatalf("MkdirAll(parent granted) error = %v", err)
	}
	if err := os.WriteFile(config.ConfigPath(home), []byte(`
[permissions.command-cwd.filesystem]
":root" = "read"

[permissions.command-cwd.filesystem.":workspace_roots"]
"granted" = "write"
`), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
	profileID := "command-cwd"
	relativeCWD := "command-cwd"
	oldRunner := runCommandExecSandboxed
	called := false
	runCommandExecSandboxed = func(ctx context.Context, req *tool.ShellRequest) (*tool.ShellResult, error) {
		called = true
		if req.CWD != commandDir {
			t.Fatalf("CWD = %q, want %q", req.CWD, commandDir)
		}
		if req.PermissionProfileID != profileID {
			t.Fatalf("PermissionProfileID = %q, want %q", req.PermissionProfileID, profileID)
		}
		if strings.Contains(req.PermissionProfileJSON, "project_roots") {
			t.Fatalf("launch profile still carries symbolic workspace rules: %s", req.PermissionProfileJSON)
		}
		if access := commandExecPermissionProfileJSONPathAccess(t, req.PermissionProfileJSON, grantedDir); access != string(sandbox.FileSystemAccessWrite) {
			t.Fatalf("granted subpath access = %q, want write in %s", access, req.PermissionProfileJSON)
		}
		if access := commandExecPermissionProfileJSONPathAccess(t, req.PermissionProfileJSON, commandDir); access == string(sandbox.FileSystemAccessWrite) {
			t.Fatalf("command cwd unexpectedly had write access in %s", req.PermissionProfileJSON)
		}
		if access := commandExecPermissionProfileJSONPathAccess(t, req.PermissionProfileJSON, parentGranted); access == string(sandbox.FileSystemAccessWrite) {
			t.Fatalf("parent subdirectory unexpectedly had write access in %s", req.PermissionProfileJSON)
		}
		return &tool.ShellResult{ExitCode: 0, Stdout: "granted"}, nil
	}
	defer func() { runCommandExecSandboxed = oldRunner }()

	router := NewRuntimeRouter(RuntimeServices{
		DefaultCWD:  defaultCWD,
		Config:      config.NewConfigService(home),
		CommandExec: NewCommandExecService(),
	})
	response := router.Handle(requestWithParams(t, IntID(1), MethodCommandExec, CommandExecParams{
		Command:           commandExecTestOutputCommand("ignored", ""),
		CWD:               &relativeCWD,
		PermissionProfile: &profileID,
	}))
	if response.Error != nil {
		t.Fatalf("command exec = %+v", response)
	}
	if !called {
		t.Fatalf("sandbox runner was not called")
	}
	result := response.Result.(*CommandExecResponse)
	if result.ExitCode != 0 || result.Stdout != "granted" {
		t.Fatalf("result = %+v", result)
	}
}
