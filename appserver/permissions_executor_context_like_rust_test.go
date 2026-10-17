package appserver

import (
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/sandbox"
	"codex_go/turn"
)

// TestPermissionsInstructionsUseRemoteExecutorPathsLikeRust pins Rust #47867:
// the permissions fragment resolves a selected remote environment's writable
// roots and deny entries with that executor's cached cwd/home/platform facts,
// rendering them in native spelling.
func TestPermissionsInstructionsUseRemoteExecutorPathsLikeRust(t *testing.T) {
	execServerURL, done := newEnvironmentInfoExecServerForTest(t, map[string]any{
		"shell": map[string]any{
			"name": "powershell",
			"path": `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`,
		},
		"cwd":         "file:///C:/workspace",
		"platformOs":  "windows",
		"userHomeDir": `C:\Users\me`,
	})
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "")
	if _, err := manager.Add(&EnvironmentAddParams{EnvironmentID: "remote", ExecServerURL: execServerURL}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if _, err := manager.Info(&EnvironmentInfoParams{EnvironmentID: "remote"}); err != nil {
		t.Fatalf("Info() error = %v", err)
	}
	waitEnvironmentInfoExecServerForTest(t, done)
	if info, ok := manager.CachedInfo("remote"); !ok || info == nil || info.PlatformOS != "windows" {
		t.Fatalf("CachedInfo() = %#v, %v", info, ok)
	}

	profile := sandbox.WorkspaceWritePermissionProfile()
	profile.DeniedReadEntries = []sandbox.FileSystemSandboxEntry{
		{Path: sandbox.FileSystemPath{Type: "path", Path: "blocked"}, Access: sandbox.FileSystemAccessDeny},
		{Path: sandbox.FileSystemPath{Type: "glob_pattern", Pattern: "~/private/**"}, Access: sandbox.FileSystemAccessDeny},
	}
	profileJSON, err := sandbox.RuntimePermissionProfileJSON(profile)
	if err != nil {
		t.Fatalf("RuntimePermissionProfileJSON() error = %v", err)
	}
	params := &turn.TurnStartParams{Environments: []map[string]any{{
		"environmentId": "remote",
		"cwd":           "file:///C:/workspace",
		"config": map[string]any{"state": "ready", "config": map[string]any{
			"permission_profile":    profileJSON,
			"permission_profile_id": "workspace",
		}},
	}}}
	router := NewRuntimeRouter(RuntimeServices{Environment: manager})
	defer router.Close()
	cfg := &config.Config{Values: map[string]any{}}
	options := router.permissionPromptOptionsForTurn("thread-executor-paths", params, nil, cfg)
	if options.ExecutorContext == nil {
		t.Fatal("permission prompt executor context is missing")
	}
	rendered, _ := router.permissionsInstructionsForTurn(params, cfg, options)
	for _, want := range []string{
		"`sandbox_mode` is `workspace-write`",
		"`C:\\workspace`",
		"path `C:\\workspace\\blocked`",
		"glob `C:\\Users\\me\\private\\**`",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("permissions fragment missing %q:\n%s", want, rendered)
		}
	}
}

// TestPermissionsInstructionsKeepLocalResolutionWithoutRemoteSelectionLikeRust
// guards the local half: no selected executor means no executor context, so
// host resolution remains in place.
func TestPermissionsInstructionsKeepLocalResolutionWithoutRemoteSelectionLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{Environment: NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "")})
	defer router.Close()
	options := router.permissionPromptOptionsForTurn("thread-local-paths", &turn.TurnStartParams{}, nil, &config.Config{Values: map[string]any{}})
	if options.ExecutorContext != nil {
		t.Fatalf("local turn resolved an executor context: %#v", options.ExecutorContext)
	}
}
