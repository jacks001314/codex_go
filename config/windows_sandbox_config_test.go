package config

import (
	"testing"

	"codex_go/sandbox"
)

// Mirrors Rust #45312: explicit mode wins without persisting a feature-only
// fallback, and requirements constrain the effective level.
func TestPrepareWindowsSandboxConfigSeparatesConfiguredModeLikeRust(t *testing.T) {
	featureElevated := map[string]any{
		"features": map[string]any{"elevated_windows_sandbox": true},
	}
	prepared := prepareWindowsSandboxConfig(featureElevated, nil)
	if prepared.Mode != "" || prepared.EffectiveMode != WindowsSandboxModeElevated ||
		prepared.Level != sandbox.WindowsSandboxElevated || !prepared.Effective {
		t.Fatalf("feature-only prepared config = %#v", prepared)
	}

	explicitUnelevated := map[string]any{
		"windows":  map[string]any{"sandbox": "unelevated"},
		"features": map[string]any{"elevated_windows_sandbox": true},
	}
	prepared = prepareWindowsSandboxConfig(explicitUnelevated, nil)
	if prepared.Mode != WindowsSandboxModeUnelevated ||
		prepared.EffectiveMode != WindowsSandboxModeUnelevated ||
		prepared.Level != sandbox.WindowsSandboxUnelevated {
		t.Fatalf("explicit prepared config = %#v", prepared)
	}

	requirements := &ConfigRequirements{
		AllowedWindowsSandboxImplementations: []WindowsSandboxImplementation{WindowsSandboxImplementationElevated},
	}
	prepared = prepareWindowsSandboxConfig(explicitUnelevated, requirements)
	if prepared.Mode != WindowsSandboxModeElevated ||
		prepared.EffectiveMode != WindowsSandboxModeElevated ||
		prepared.Level != sandbox.WindowsSandboxElevated || !prepared.FellBack {
		t.Fatalf("constrained prepared config = %#v", prepared)
	}

	prepared = prepareWindowsSandboxConfig(map[string]any{
		"windows": map[string]any{"sandbox": "mxc"},
	}, requirements)
	if prepared.Mode != WindowsSandboxModeMxc || prepared.EffectiveMode != WindowsSandboxModeMxc ||
		prepared.Level != sandbox.WindowsSandboxMxc || prepared.FellBack {
		t.Fatalf("mxc prepared config = %#v", prepared)
	}
}
