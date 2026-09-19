package config

import "testing"

// TestResolveWindowsSandboxModeAppliesRequirementsLikeRust pins Rust's
// apply_requirement_constrained_value for "windows.sandbox": a disallowed (or
// absent) configured value falls back to the constrained initial mode, which is
// elevated when the allow-list permits it.
func TestResolveWindowsSandboxModeAppliesRequirementsLikeRust(t *testing.T) {
	unelevatedConfig := map[string]any{"windows": map[string]any{"sandbox": "unelevated"}}
	elevatedConfig := map[string]any{"windows": map[string]any{"sandbox": "elevated"}}
	mxcConfig := map[string]any{"windows": map[string]any{"sandbox": "mxc"}}
	elevatedOnly := &ConfigRequirements{AllowedWindowsSandboxImplementations: []WindowsSandboxImplementation{WindowsSandboxImplementationElevated}}
	bothAllowed := &ConfigRequirements{AllowedWindowsSandboxImplementations: []WindowsSandboxImplementation{WindowsSandboxImplementationElevated, WindowsSandboxImplementationUnelevated}}
	unelevatedOnly := &ConfigRequirements{AllowedWindowsSandboxImplementations: []WindowsSandboxImplementation{WindowsSandboxImplementationUnelevated}}

	cases := []struct {
		name         string
		values       map[string]any
		requirements *ConfigRequirements
		wantMode     WindowsSandboxMode
		wantFallback bool
		wantOK       bool
	}{
		{"disallowed config falls back to elevated", unelevatedConfig, elevatedOnly, WindowsSandboxModeElevated, true, true},
		{"allowed config wins", elevatedConfig, bothAllowed, WindowsSandboxModeElevated, false, true},
		{"absent config with requirements uses the initial mode", nil, elevatedOnly, WindowsSandboxModeElevated, true, true},
		{"absent config with unelevated-only requirements", nil, unelevatedOnly, WindowsSandboxModeUnelevated, true, true},
		{"no requirements keeps the configured mode", unelevatedConfig, nil, WindowsSandboxModeUnelevated, false, true},
		{"unset without requirements stays unset", nil, nil, "", false, false},
		// Rust #46271: the allowed-implementation list only governs the legacy
		// elevated/unelevated backends, so a configured mxc is never replaced.
		{"mxc ignores an elevated-only allowlist", mxcConfig, elevatedOnly, WindowsSandboxModeMxc, false, true},
		{"mxc ignores an unelevated-only allowlist", mxcConfig, unelevatedOnly, WindowsSandboxModeMxc, false, true},
		{"mxc without requirements", mxcConfig, nil, WindowsSandboxModeMxc, false, true},
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
		want   WindowsSandboxMode
		ok     bool
	}{
		{"windows.sandbox", map[string]any{"windows": map[string]any{"sandbox": "elevated"}}, WindowsSandboxModeElevated, true},
		{"windows.sandbox mxc", map[string]any{"windows": map[string]any{"sandbox": "mxc"}}, WindowsSandboxModeMxc, true},
		{"legacy windows_sandbox", map[string]any{"windows_sandbox": "restricted-token"}, WindowsSandboxModeUnelevated, true},
		{"legacy elevated feature", map[string]any{"features": map[string]any{"elevated_windows_sandbox": true}}, WindowsSandboxModeElevated, true},
		{"legacy experimental feature", map[string]any{"features": map[string]any{"experimental_windows_sandbox": true}}, WindowsSandboxModeUnelevated, true},
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
