package appserver

import (
	"testing"

	"codex_go/config"
	"codex_go/sandbox"
)

// TestWindowsSandboxMxcSelectionLikeRust mirrors Rust #46271: `windows.sandbox =
// "mxc"` selects the native backend, which Go reports through the MXC level (so
// sandbox metadata says windows_mxc), while the legacy setup mode stays unset
// because MXC does not use the legacy setup APIs.
func TestWindowsSandboxMxcSelectionLikeRust(t *testing.T) {
	values := map[string]any{"windows": map[string]any{"sandbox": "mxc"}}
	if got := windowsSandboxLevelFromConfigValues(values); got != sandbox.WindowsSandboxMxc {
		t.Fatalf("windowsSandboxLevelFromConfigValues(mxc) = %q, want mxc", got)
	}
	if mode, ok := windowsSandboxModeFromConfigValues(values); ok {
		t.Fatalf("windowsSandboxModeFromConfigValues(mxc) = %q, %v; want no legacy setup mode", mode, ok)
	}
	// The legacy levels still map to their setup modes.
	elevated := map[string]any{"windows": map[string]any{"sandbox": "elevated"}}
	if got := windowsSandboxLevelFromConfigValues(elevated); got != sandbox.WindowsSandboxElevated {
		t.Fatalf("windowsSandboxLevelFromConfigValues(elevated) = %q, want elevated", got)
	}
	if mode, ok := windowsSandboxModeFromConfigValues(elevated); !ok || mode != sandbox.WindowsSetupElevated {
		t.Fatalf("windowsSandboxModeFromConfigValues(elevated) = %q, %v", mode, ok)
	}
	// A managed allow-list never replaces a configured mxc.
	requirements := &config.ConfigRequirements{
		AllowedWindowsSandboxImplementations: []config.WindowsSandboxImplementation{config.WindowsSandboxImplementationElevated},
	}
	cfg := &config.Config{Values: values, Requirements: requirements}
	if got := windowsSandboxLevelForConfig(cfg); got != sandbox.WindowsSandboxMxc {
		t.Fatalf("windowsSandboxLevelForConfig(mxc, elevated-only requirements) = %q, want mxc", got)
	}
}
