package appserver

import (
	"encoding/json"
	"testing"

	"codex_go/config"
	"codex_go/sandbox"
	"codex_go/turn"
)

func TestInheritedNamedPermissionResolutionBoundaries(t *testing.T) {
	for _, scenario := range []string{"unchanged", "cwd", "roots", "requirements", "selection"} {
		t.Run(scenario, func(t *testing.T) {
			cwd := t.TempDir()
			name := ":read-only"
			raw, err := sandbox.RuntimePermissionProfileJSON(sandbox.FullAccessPermissionProfile())
			if err != nil {
				t.Fatal(err)
			}
			settings := &Settings{CWD: cwd, ActivePermissionProfile: &name, normalizedPermissionProfile: json.RawMessage(raw), RuntimeWorkspaceRoots: []string{cwd}}
			params := &turn.TurnStartParams{CWD: cwd}
			applyThreadSettingsEnvironmentConfigToTurnStartParams(params, settings)
			cfg := &config.Config{Values: map[string]any{}}
			switch scenario {
			case "cwd":
				params.CWD = t.TempDir()
			case "roots":
				params.RuntimeWorkspaceRoots = []string{t.TempDir()}
			case "requirements":
				cfg.Requirements = &config.ConfigRequirements{AllowedSandboxModes: []sandbox.SandboxMode{sandbox.SandboxReadOnly}}
			case "selection":
				other := ":workspace"
				params.Permissions = &other
			}
			result, err := threadDefaultSandboxPermissionProfile(cfg, params.CWD, params)
			if err != nil {
				t.Fatal(err)
			}
			full := result.Profile.LegacySandboxPolicy().Kind == sandbox.SandboxDangerFullAccess
			if full != (scenario == "unchanged") {
				t.Fatalf("scenario %s reused stale grants: full=%v", scenario, full)
			}
		})
	}
}
