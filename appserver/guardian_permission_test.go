package appserver

import (
	"strings"
	"testing"

	"codex_go/applypatch"
	"codex_go/sandbox"
	"codex_go/tool"
	"codex_go/turn"
)

// Mirrors Rust's `guardian::permissions::for_environment`: the reviewed turn's
// evidence comes from the selected environment's own profile and names that
// environment's selection id, instead of reporting the parent turn's profile
// anonymously.
func TestGuardianTurnEnvironmentForTurnResolvesSelectionLikeRust(t *testing.T) {
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "fake", Path: "fake-shell"}, t.TempDir())
	if _, err := manager.Add(&EnvironmentAddParams{EnvironmentID: "ready-env", ExecServerURL: "ws://127.0.0.1:1234"}); err != nil {
		t.Fatal(err)
	}
	readOnly := sandbox.ReadOnlyPermissionProfile()
	profileJSON, err := sandbox.RuntimePermissionProfileJSON(readOnly)
	if err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{Environment: manager})
	defer router.Close()

	params := &turn.TurnStartParams{Environments: []map[string]any{{
		"environmentId": "ready-env",
		"cwd":           "/remote",
		"config": map[string]any{"state": "ready", "config": map[string]any{
			"permission_profile":    profileJSON,
			"permission_profile_id": "read-only",
		}},
	}}}
	environments := router.unifiedExecEnvironmentsForTurn(params)
	if len(environments) != 1 || environments[0].PermissionProfile == nil {
		t.Fatalf("turn environments = %#v", environments)
	}

	environmentID, cwd, profile, found := router.guardianTurnEnvironmentForTurn(params, "")
	if !found {
		t.Fatal("primary environment not found")
	}
	if environmentID == nil || *environmentID != "ready-env" {
		t.Fatalf("environment id = %v, want ready-env", environmentID)
	}
	if cwd != "/remote" {
		t.Fatalf("environment cwd = %q, want /remote", cwd)
	}
	if profile == nil {
		t.Fatal("profile = nil, want the environment's own profile")
	}
	profileWire, err := sandbox.RuntimePermissionProfileJSON(*profile)
	if err != nil {
		t.Fatal(err)
	}
	environmentWire, err := sandbox.RuntimePermissionProfileJSON(*environments[0].PermissionProfile)
	if err != nil {
		t.Fatal(err)
	}
	if profileWire != environmentWire || !strings.Contains(profileWire, `"kind":"root"`) {
		t.Fatalf("profile = %s, want the environment's own %s", profileWire, environmentWire)
	}

	// A tool payload's own environment resolves that environment instead of the
	// primary one (Rust's `for_tool`), and an unavailable id drops the evidence.
	if id, _, _, found := router.guardianTurnEnvironmentForTurn(params, "ready-env"); !found || id == nil || *id != "ready-env" {
		t.Fatalf("requested environment = %v/%v, want ready-env", id, found)
	}
	if _, _, _, found := router.guardianTurnEnvironmentForTurn(params, "missing-env"); found {
		t.Fatal("an unavailable tool environment must not resolve")
	}

	// A turn that selects no environment keeps the parent turn's profile and
	// reports no selection id.
	if id, cwd, profile, found := router.guardianTurnEnvironmentForTurn(&turn.TurnStartParams{}, ""); id != nil || cwd != "" || profile != nil || found {
		t.Fatalf("unselected environment = %v/%q/%#v/%v, want none", id, cwd, profile, found)
	}
}

// The approval actions carry the tool payload's own environment, which the
// guardian action JSON renders and the permission evidence resolves (Rust's
// `apply_patch::parse_patch(...).environment_id` and ExecCommand.environment_id).
func TestGuardianApprovalActionsCarryToolEnvironmentLikeRust(t *testing.T) {
	patch := applyPatchApprovalAction(&tool.ApplyPatchApprovalRequest{
		Action: &applypatch.Action{EnvironmentID: "remote"},
		CWD:    "/repo",
	})
	if patch.EnvironmentID != "remote" {
		t.Fatalf("apply_patch environment = %q, want remote", patch.EnvironmentID)
	}
	command := commandApprovalAction(&tool.ShellRequest{UnifiedExecEnvironmentID: "remote", CWD: "/repo"})
	if command.EnvironmentID != "remote" {
		t.Fatalf("exec_command environment = %q, want remote", command.EnvironmentID)
	}
}
