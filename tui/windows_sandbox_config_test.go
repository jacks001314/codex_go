package tui

import "testing"

// Mirrors Rust #45813 windows_sandbox_tests.rs:
// windows_sandbox_server_policy_controls_setup_choices.
func TestWindowsSandboxServerPolicyControlsSetupChoices(t *testing.T) {
	unelevated := string(WindowsSandboxModeConfigUnelevated)
	for _, testCase := range []struct {
		name            string
		configured      *string
		implementations []string
		wantMode        *WindowsSandboxModeConfig
		wantAllows      [2]bool
		wantRequires    bool
	}{
		{
			name:         "null policy allows both",
			configured:   &unelevated,
			wantMode:     ptrMode(WindowsSandboxModeConfigUnelevated),
			wantAllows:   [2]bool{true, true},
			wantRequires: false,
		},
		{
			name:            "disallowed mode falls back to elevated",
			configured:      &unelevated,
			implementations: []string{"elevated"},
			wantMode:        ptrMode(WindowsSandboxModeConfigElevated),
			wantAllows:      [2]bool{true, false},
			wantRequires:    true,
		},
		{
			name:            "unconfigured prefers elevated",
			implementations: []string{"unelevated", "elevated"},
			wantMode:        ptrMode(WindowsSandboxModeConfigElevated),
			wantAllows:      [2]bool{true, true},
			wantRequires:    true,
		},
		{
			name:            "unconfigured unelevated only",
			implementations: []string{"unelevated"},
			wantMode:        ptrMode(WindowsSandboxModeConfigUnelevated),
			wantAllows:      [2]bool{false, true},
			wantRequires:    false,
		},
		{
			name:            "empty policy allows nothing",
			configured:      &unelevated,
			implementations: []string{},
			wantMode:        nil,
			wantAllows:      [2]bool{false, false},
			wantRequires:    false,
		},
		{
			name:            "mxc is not a setup mode",
			configured:      &unelevated,
			implementations: []string{"mxc"},
			wantMode:        nil,
			wantAllows:      [2]bool{false, false},
			wantRequires:    false,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			values := map[string]any{}
			if testCase.configured != nil {
				values["windows"] = map[string]any{"sandbox": *testCase.configured}
			}
			state := WindowsSandboxConfigFromResponses(
				values,
				WindowsSandboxPolicyFromImplementations(testCase.implementations),
			)
			if !sameWindowsSandboxMode(state.Mode, testCase.wantMode) {
				t.Fatalf("mode = %v, want %v", derefMode(state.Mode), derefMode(testCase.wantMode))
			}
			gotAllows := [2]bool{
				state.Allows(WindowsSandboxModeConfigElevated),
				state.Allows(WindowsSandboxModeConfigUnelevated),
			}
			if gotAllows != testCase.wantAllows {
				t.Fatalf("allows = %v, want %v", gotAllows, testCase.wantAllows)
			}
			if got := state.RequiresElevated(); got != testCase.wantRequires {
				t.Fatalf("requires_elevated = %v, want %v", got, testCase.wantRequires)
			}
		})
	}
}

// Mirrors Rust #45813's legacy feature-flag precedence loop.
func TestWindowsSandboxLegacyFeatureFallbackMatchesRust(t *testing.T) {
	for _, feature := range []struct {
		key  string
		mode WindowsSandboxModeConfig
	}{
		{key: "elevated_windows_sandbox", mode: WindowsSandboxModeConfigElevated},
		{key: "experimental_windows_sandbox", mode: WindowsSandboxModeConfigUnelevated},
		{key: "enable_experimental_windows_sandbox", mode: WindowsSandboxModeConfigUnelevated},
	} {
		explicitElevated := string(WindowsSandboxModeConfigElevated)
		for _, explicit := range []*string{nil, &explicitElevated} {
			values := map[string]any{"features": map[string]any{feature.key: true}}
			want := feature.mode
			if explicit != nil {
				values["windows"] = map[string]any{"sandbox": string(*explicit)}
				want = WindowsSandboxModeConfig(*explicit)
			}
			state := WindowsSandboxConfigFromResponses(
				values,
				WindowsSandboxPolicy{Loaded: true},
			)
			if state.Mode == nil || *state.Mode != want {
				t.Fatalf("feature %q explicit %v mode = %v, want %v", feature.key, explicit != nil, derefMode(state.Mode), want)
			}
		}
	}

	unknown := WindowsSandboxConfig{}
	if unknown.Allows(WindowsSandboxModeConfigElevated) || unknown.Allows(WindowsSandboxModeConfigUnelevated) {
		t.Fatal("an unloaded policy must deny every setup mode")
	}
	if unknown.RequiresElevated() {
		t.Fatal("an unloaded policy must not require elevated setup")
	}
	if unknown.Level() != WindowsSandboxLevelDisabled {
		t.Fatalf("default level = %q, want disabled", unknown.Level())
	}
}

func TestWindowsSandboxConfigLevelMatchesRust(t *testing.T) {
	for _, testCase := range []struct {
		mode  *WindowsSandboxModeConfig
		level WindowsSandboxLevel
	}{
		{mode: ptrMode(WindowsSandboxModeConfigElevated), level: WindowsSandboxLevelElevated},
		{mode: ptrMode(WindowsSandboxModeConfigUnelevated), level: WindowsSandboxLevelRestrictedToken},
		{mode: nil, level: WindowsSandboxLevelDisabled},
	} {
		if got := (WindowsSandboxConfig{Mode: testCase.mode}).Level(); got != testCase.level {
			t.Fatalf("mode %v level = %q, want %q", derefMode(testCase.mode), got, testCase.level)
		}
	}
}

func ptrMode(mode WindowsSandboxModeConfig) *WindowsSandboxModeConfig {
	return &mode
}

func sameWindowsSandboxMode(a, b *WindowsSandboxModeConfig) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func derefMode(mode *WindowsSandboxModeConfig) any {
	if mode == nil {
		return nil
	}
	return *mode
}
