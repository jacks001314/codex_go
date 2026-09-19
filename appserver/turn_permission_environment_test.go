package appserver

import (
	"path/filepath"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/sandbox"
	"codex_go/turn"
)

// Mirrors Rust #46568: the fallback permission profile is materialized with the
// captured primary selection's workspace roots, including while the attachment
// is still starting, and a ready primary's own profile wins over the thread
// defaults.
func TestTurnSandboxPermissionProfileUsesCapturedEnvironmentStateLikeRust(t *testing.T) {
	threadCWD := t.TempDir()
	environmentRoot := t.TempDir()
	cfg := &config.Config{Values: map[string]any{
		"default_permissions": "restricted",
		"permissions": map[string]any{
			"restricted": map[string]any{
				"filesystem": map[string]any{":workspace_roots": "write"},
			},
		},
	}}

	starting := &turn.TurnStartParams{
		ThreadID: "thread-1",
		CWD:      threadCWD,
		Environments: []map[string]any{{
			"environmentId":  "remote",
			"cwd":            environmentRoot,
			"workspaceRoots": []string{environmentRoot},
			"config":         map[string]any{"state": "pending"},
		}},
	}
	resolution, err := turnSandboxPermissionProfile(cfg, threadCWD, starting)
	if err != nil {
		t.Fatalf("turnSandboxPermissionProfile(starting) error = %v", err)
	}
	if resolution == nil || len(resolution.WorkspaceRoots) == 0 {
		t.Fatalf("starting environment resolution = %#v", resolution)
	}
	if !sameAppPath(resolution.WorkspaceRoots[0], environmentRoot) {
		t.Fatalf("workspace roots = %#v, want the captured environment root %q", resolution.WorkspaceRoots, environmentRoot)
	}
	if sameAppPath(resolution.WorkspaceRoots[0], threadCWD) {
		t.Fatalf("the fallback profile kept the thread cwd anchor: %#v", resolution.WorkspaceRoots)
	}

	// The attachment's own resolved profile wins for a ready primary.
	environmentJSON, err := sandbox.RuntimePermissionProfileJSON(sandbox.ReadOnlyPermissionProfile())
	if err != nil {
		t.Fatal(err)
	}
	ready := &turn.TurnStartParams{
		ThreadID: "thread-1",
		CWD:      threadCWD,
		Environments: []map[string]any{{
			"environmentId": "remote",
			"cwd":           environmentRoot,
			"config": map[string]any{"state": "ready", "config": map[string]any{
				"permission_profile":    environmentJSON,
				"permission_profile_id": "read-only",
			}},
		}},
	}
	resolution, err = turnSandboxPermissionProfile(cfg, threadCWD, ready)
	if err != nil {
		t.Fatalf("turnSandboxPermissionProfile(ready) error = %v", err)
	}
	if resolution == nil || resolution.ID != "read-only" || strings.TrimSpace(resolution.ProfileJSON) != strings.TrimSpace(environmentJSON) {
		t.Fatalf("ready environment resolution = %#v", resolution)
	}
}

// A ready attachment's own profile governs its sandbox context
// (Rust TurnEnvironment::permission_profile_with_workspace_roots), while the
// local context keeps the thread profile.
func TestExecutorSkillSandboxContextUsesEnvironmentProfileLikeRust(t *testing.T) {
	cwd := t.TempDir()
	remoteDenied := filepath.Join(cwd, "remote-only", "**")
	cfg := &config.Config{Values: map[string]any{"sandbox_mode": "workspace-write"}}
	attachment := &config.Config{Values: map[string]any{
		"default_permissions": "restricted",
		"permissions": map[string]any{
			"restricted": map[string]any{
				"filesystem": map[string]any{
					":minimal":   "read",
					remoteDenied: "deny",
				},
			},
		},
	}}
	attachmentResolution, err := attachment.ResolveSandboxPermissionProfile("", cwd)
	if err != nil {
		t.Fatalf("attachment profile resolution error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{DefaultCWD: cwd})
	contexts, err := router.executorSkillSandboxContextsForTurn(cfg, cwd, &turn.TurnStartParams{
		ThreadID: "thread-1",
		CWD:      cwd,
		Environments: []map[string]any{{
			"environmentId": "remote",
			"cwd":           cwd,
			"config": map[string]any{"state": "ready", "config": map[string]any{
				"permission_profile":    attachmentResolution.ProfileJSON,
				"permission_profile_id": "restricted",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("executorSkillSandboxContextsForTurn() error = %v", err)
	}
	remote := contexts["remote"]
	if remote == nil {
		t.Fatalf("remote sandbox context is missing: %#v", contexts)
	}
	if got := string(remote.Permissions); !strings.Contains(got, "remote-only") || !strings.Contains(got, `"access":"deny"`) {
		t.Fatalf("remote context permissions = %s, want the attachment profile", got)
	}
	local := contexts["local"]
	if local == nil {
		t.Fatalf("local sandbox context is missing: %#v", contexts)
	}
	if got := string(local.Permissions); strings.Contains(got, "remote-only") {
		t.Fatalf("the attachment profile leaked into the local context: %s", got)
	}
}
