package app

import (
	"testing"

	"codex_go/cli"
)

// TestInteractiveLoadSettingsCarriesServiceTierRequirementsLikeRust pins the
// host half of the Rust #51253 wiring: the TUI bootstrap reads
// configRequirements/read next to model/list and constrains the model catalog's
// service tiers with it (codex-rs/tui/src/app_server_session.rs ->
// service_tier_resolution::constrain_server_service_tiers). Go's settings load is
// where the launcher already resolves preferences, so it must also carry the
// requirements the tea Options hand to the catalog path.
//
// Rust counterpart: `config_requirements_read_exposes_independent_speed_policy`
// (codex-rs/app-server/tests/suite/v2/config_rpc.rs) — the embedded app server
// answers `supportsIndependentSpeedModes: Some(true)` (#51253), so Ultra Fast is
// not held behind the shared Fast gate for a local TUI session.
func TestInteractiveLoadSettingsCarriesServiceTierRequirementsLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())

	result, err := interactiveLoadSettings(&cli.RootOptions{})
	if err != nil {
		t.Fatalf("interactiveLoadSettings error = %v", err)
	}
	requirements := result.ServiceTierRequirements
	if requirements == nil {
		t.Fatal("settings load did not carry the server configRequirements/read response")
	}
	if requirements.SupportsIndependentSpeedModes == nil || !*requirements.SupportsIndependentSpeedModes {
		t.Fatalf("supportsIndependentSpeedModes = %#v, want true", requirements.SupportsIndependentSpeedModes)
	}
	// The TUI's startup settings come from this same record, so the new-thread
	// entry carries the requirements to the tea Options.
	t.Setenv("CODEX_HOME", t.TempDir())
	local := interactiveNewThreadLocalSettings(&cli.RootOptions{})
	if local.Tui.ServiceTierRequirements == nil {
		t.Fatal("new-thread local settings dropped the service tier requirements")
	}
}
