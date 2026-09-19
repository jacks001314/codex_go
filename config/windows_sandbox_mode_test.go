package config

import "testing"

// TestResolveWindowsSandboxModeAppliesRequirementsLikeRust pins Rust's
// apply_requirement_constrained_value for "windows.sandbox": a disallowed (or
// absent) configured value falls back to the constrained initial mode, which is
// elevated when the allow-list permits it.
func TestResolveWindowsSandboxModeAppliesRequirementsLikeRust(t *testing.T) {
	unelevatedConfig := map[string]any{"windows": map[string]any{"sandbox": "unelevated"}}
	elevatedConfig := map[string]any{"windows": map[string]any{"sandbox": "elevated"}}
	elevatedOnly := &ConfigRequirements{AllowedWindowsSandboxImplementations: []WindowsSandboxImplementation{WindowsSandboxImplementationElevated}}
	bothAllowed := &ConfigRequirements{AllowedWindowsSandboxImplementations: []WindowsSandboxImplementation{WindowsSandboxImplementationElevated, WindowsSandboxImplementationUnelevated}}
	unelevatedOnly := &ConfigRequirements{AllowedWindowsSandboxImplementations: []WindowsSandboxImplementation{WindowsSandboxImplementationUnelevated}}

	cases := []struct {
		name         string
		values       map[string]any
		requirements *ConfigRequirements
		wantMode     WindowsSandboxSetupMode
		wantFallback bool
		wantOK       bool
	}{
		{"disallowed config falls back to elevated", unelevatedConfig, elevatedOnly, WindowsSandboxSetupElevated, true, true},
		{"allowed config wins", elevatedConfig, bothAllowed, WindowsSandboxSetupElevated, false, true},
		{"absent config with requirements uses the initial mode", nil, elevatedOnly, WindowsSandboxSetupElevated, true, true},
		{"absent config with unelevated-only requirements", nil, unelevatedOnly, WindowsSandboxSetupUnelevated, true, true},
		{"no requirements keeps the configured mode", unelevatedConfig, nil, WindowsSandboxSetupUnelevated, false, true},
		{"unset without requirements stays unset", nil, nil, "", false, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			mode, fellBack, ok := ResolveWindowsSandboxMode(testCase.values, testCase.requirements)
			if mode != testCase.wantMode || fellBack != testCase.wantFallback || ok != testCase.wantOK {
				t.Fatalf("ResolveWindowsSandboxMode() = %q, %v, %v; want %q, %v, %v",
					mode, fellBack, ok, testCase.wantMode, testCase.wantFallback, testCase.wantOK)
			}
		})
	}
}

// TestWindowsSandboxModeFromValuesLegacyShapes pins the configuration shapes in
// Rust's resolve_windows_sandbox_mode.
func TestWindowsSandboxModeFromValuesLegacyShapes(t *testing.T) {
	cases := []struct {
		name   string
		values map[string]any
		want   WindowsSandboxSetupMode
		ok     bool
	}{
		{"windows.sandbox", map[string]any{"windows": map[string]any{"sandbox": "elevated"}}, WindowsSandboxSetupElevated, true},
		{"legacy windows_sandbox", map[string]any{"windows_sandbox": "restricted-token"}, WindowsSandboxSetupUnelevated, true},
		{"legacy elevated feature", map[string]any{"features": map[string]any{"elevated_windows_sandbox": true}}, WindowsSandboxSetupElevated, true},
		{"legacy experimental feature", map[string]any{"features": map[string]any{"experimental_windows_sandbox": true}}, WindowsSandboxSetupUnelevated, true},
		{"nothing configured", map[string]any{}, "", false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			mode, ok := WindowsSandboxModeFromValues(testCase.values)
			if mode != testCase.want || ok != testCase.ok {
				t.Fatalf("WindowsSandboxModeFromValues() = %q, %v; want %q, %v", mode, ok, testCase.want, testCase.ok)
			}
		})
	}
}
