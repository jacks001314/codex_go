package appserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/sandbox"
	"codex_go/turn"
)

func TestCompactFilesystemContextFeatureLikeRust(t *testing.T) {
	workspace := t.TempDir()
	extra := t.TempDir()
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "")
	if err := manager.SetInfo("env-a", EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, workspace); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{Environment: manager})
	defer router.Close()

	profile := sandbox.WorkspaceWritePermissionProfile()
	profile.SandboxPolicy.WritableRoots = []string{workspace, extra}
	params := &turn.TurnStartParams{ThreadID: "thread-compact-filesystem", CWD: workspace, Environments: []map[string]any{
		perEnvironmentTestSelection(t, "env-a", workspace, profile, []string{workspace}),
	}}
	render := func(enabled bool) string {
		t.Helper()
		cfg := &config.Config{Values: map[string]any{"features": map[string]any{"compact_filesystem_context": enabled}}}
		items, _, err := router.turnEnvironmentContextInputItemForTurn(context.Background(), params.ThreadID, "turn-1", params, cfg)
		if err != nil {
			t.Fatalf("turnEnvironmentContextInputItemForTurn(enabled=%v) error = %v", enabled, err)
		}
		encoded, err := json.Marshal(items)
		if err != nil {
			t.Fatalf("Marshal(items) error = %v", err)
		}
		return string(encoded)
	}

	full := render(false)
	compact := render(true)
	if strings.Contains(full, "Managed, restricted. Read access to :root.") {
		t.Fatalf("feature-disabled filesystem context was compact:\n%s", full)
	}
	for _, want := range []string{
		"Managed, restricted. Read access to :root.",
		"Writable workspace roots:",
		"Other writable paths:",
		"Directly under each listed path, .git, .agents, .codex, .gcode and .aws",
	} {
		if !strings.Contains(compact, want) {
			t.Fatalf("feature-enabled filesystem context missing %q:\n%s", want, compact)
		}
	}
}
