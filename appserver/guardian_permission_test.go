package appserver

import (
	"strings"
	"testing"

	"codex_go/sandbox"
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

	environmentID, cwd, profile := router.guardianTurnEnvironmentForTurn(params)
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

	// A turn that selects no environment keeps the parent turn's profile and
	// reports no selection id.
	if id, cwd, profile := router.guardianTurnEnvironmentForTurn(&turn.TurnStartParams{}); id != nil || cwd != "" || profile != nil {
		t.Fatalf("unselected environment = %v/%q/%#v, want none", id, cwd, profile)
	}
}
