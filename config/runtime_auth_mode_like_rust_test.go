package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Mirrors Rust #60014 (`resolve_runtime_auth_mode` +
// FeatureRequirementsInput): the configured mode comes from the typed
// `features.runtime_agent_instances` table, the boolean gate form keeps the
// default, and a managed requirement wins while reporting the override.
func TestRuntimeAuthModeResolvesConfiguredAndManagedValuesLikeRust(t *testing.T) {
	configured := func(value any) *Config {
		return &Config{Values: map[string]any{
			"features": map[string]any{"runtime_agent_instances": value},
		}}
	}

	mode, overridden := configured(map[string]any{"mode": "assertion_only"}).RuntimeAuthMode()
	if mode != RuntimeAuthModeAssertionOnly || overridden {
		t.Fatalf("configured mode = %q overridden=%t, want assertion_only", mode, overridden)
	}
	if mode, _ := configured(true).RuntimeAuthMode(); mode != RuntimeAuthModeDualHeader {
		t.Fatalf("boolean gate mode = %q, want the dual_header default", mode)
	}
	if mode, _ := (&Config{}).RuntimeAuthMode(); mode != RuntimeAuthModeDualHeader {
		t.Fatalf("absent config mode = %q, want the dual_header default", mode)
	}
	var missing *Config
	if mode, _ := missing.RuntimeAuthMode(); mode != RuntimeAuthModeDualHeader {
		t.Fatalf("nil config mode = %q, want the dual_header default", mode)
	}

	required := RuntimeAuthModeAssertionOnly
	cfg := configured(map[string]any{"mode": "dual_header"})
	cfg.Requirements = &ConfigRequirements{RuntimeAuthMode: &required}
	mode, overridden = cfg.RuntimeAuthMode()
	if mode != RuntimeAuthModeAssertionOnly || !overridden {
		t.Fatalf("required mode = %q overridden=%t, want assertion_only overriding", mode, overridden)
	}
	warning := RuntimeAuthModeConflictWarning(RuntimeAuthModeDualHeader, required)
	for _, fragment := range []string{
		"features.runtime_agent_instances.mode",
		"`DualHeader` is not in the allowed set [AssertionOnly]",
		"falling back to required value AssertionOnly",
	} {
		if !strings.Contains(warning, fragment) {
			t.Fatalf("conflict warning %q does not contain %q", warning, fragment)
		}
	}
	if warnings := StartupWarnings(cfg.Values, cfg.Requirements); len(warnings) == 0 ||
		!strings.Contains(strings.Join(warnings, "\n"), "features.runtime_agent_instances.mode") {
		t.Fatalf("startup warnings = %#v, want the runtime auth mode conflict", warnings)
	}

	// The requirement alone is enough, and it does not report an override.
	cfg = &Config{Requirements: &ConfigRequirements{RuntimeAuthMode: &required}}
	if mode, overridden := cfg.RuntimeAuthMode(); mode != RuntimeAuthModeAssertionOnly || overridden {
		t.Fatalf("requirement-only mode = %q overridden=%t, want assertion_only", mode, overridden)
	}
	// A matching configured mode is not an override either.
	cfg = configured(map[string]any{"mode": "assertion_only"})
	cfg.Requirements = &ConfigRequirements{RuntimeAuthMode: &required}
	if mode, overridden := cfg.RuntimeAuthMode(); mode != RuntimeAuthModeAssertionOnly || overridden {
		t.Fatalf("matching mode = %q overridden=%t", mode, overridden)
	}
}

// TestRuntimeAuthModeRequirementParsesTypedFeaturesTableLikeRust covers the
// requirements-file half of Rust's FeatureRequirementsInput: only the table's
// `mode` is typed, and an unknown value is rejected.
func TestRuntimeAuthModeRequirementParsesTypedFeaturesTableLikeRust(t *testing.T) {
	requirements, err := configRequirementsFromValidatedMap(map[string]any{
		"features": map[string]any{
			"runtime_agent_instances": map[string]any{"enabled": true, "mode": "assertion_only"},
		},
	})
	if err != nil {
		t.Fatalf("parse requirements: %v", err)
	}
	if requirements.RuntimeAuthMode == nil || *requirements.RuntimeAuthMode != RuntimeAuthModeAssertionOnly {
		t.Fatalf("RuntimeAuthMode = %v, want assertion_only", requirements.RuntimeAuthMode)
	}
	if !requirements.FeatureRequirements["runtime_agent_instances"] {
		t.Fatalf("FeatureRequirements = %#v, want the boolean gate lifted", requirements.FeatureRequirements)
	}

	requirements, err = configRequirementsFromValidatedMap(map[string]any{
		"features": map[string]any{"runtime_agent_instances": true},
	})
	if err != nil {
		t.Fatalf("parse boolean form: %v", err)
	}
	if requirements.RuntimeAuthMode != nil {
		t.Fatalf("boolean form produced mode %v, want none", *requirements.RuntimeAuthMode)
	}

	if _, err := configRequirementsFromValidatedMap(map[string]any{
		"features": map[string]any{
			"runtime_agent_instances": map[string]any{"mode": "assertion"},
		},
	}); err == nil || !strings.Contains(err.Error(), "features.runtime_agent_instances.mode") {
		t.Fatalf("invalid mode error = %v", err)
	}
}

// TestRuntimeAuthModeConflictIsALoadErrorLikeRust mirrors Rust #60014
// (`core/src/config/managed_features.rs:365` `resolve_runtime_auth_mode`,
// called from `validate_explicit_feature_settings_in_config_toml`): a
// configured mode that contradicts the managed requirement fails the load
// instead of only producing a startup warning. The message is byte-identical
// to the warning so the two channels can never disagree.
func TestRuntimeAuthModeConflictIsALoadErrorLikeRust(t *testing.T) {
	required := RuntimeAuthModeAssertionOnly
	requirements := &ConfigRequirements{RuntimeAuthMode: &required}
	conflicting := func() map[string]any {
		return map[string]any{
			"features": map[string]any{
				"runtime_agent_instances": map[string]any{"mode": "dual_header"},
			},
		}
	}
	want := RuntimeAuthModeConflictWarning(RuntimeAuthModeDualHeader, required)

	err := ValidateRuntimeAuthModeRequirement(conflicting(), requirements)
	if err == nil {
		t.Fatalf("conflicting mode produced no error, want %q", want)
	}
	if err.Error() != want {
		t.Fatalf("conflict error = %q, want %q", err.Error(), want)
	}

	// The rejection must reach the load path, not just the helper: the same
	// conflict seen through applyManagedConstrainedOverrides has to abort.
	loadErr := applyManagedConstrainedOverrides(conflicting(), requirements)
	if loadErr == nil {
		t.Fatalf("load path accepted a conflicting mode, want %q", want)
	}
	if loadErr.Error() != want {
		t.Fatalf("load path error = %q, want %q", loadErr.Error(), want)
	}

	// End to end: the loader hits the same rejection from the config.toml +
	// requirements.toml pair on disk, so this is a real load failure and not
	// just a helper the loader never calls.
	home := t.TempDir()
	if err := os.WriteFile(ConfigPath(home), []byte("[features.runtime_agent_instances]\nmode = \"dual_header\"\n"), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte("[features.runtime_agent_instances]\nenabled = true\nmode = \"assertion_only\"\n"), 0o600); err != nil {
		t.Fatalf("write requirements.toml: %v", err)
	}
	_, loadFileErr := LoadEffectiveWithOptions(home, nil)
	if loadFileErr == nil {
		t.Fatalf("LoadEffectiveWithOptions accepted a conflicting mode, want %q", want)
	}
	if loadFileErr.Error() != want {
		t.Fatalf("LoadEffectiveWithOptions error = %q, want %q", loadFileErr.Error(), want)
	}

	// No requirement, an empty requirement set, or a matching mode all load.
	for name, tc := range map[string]struct {
		values       map[string]any
		requirements *ConfigRequirements
	}{
		"no requirements object": {conflicting(), nil},
		"no runtime mode":        {conflicting(), &ConfigRequirements{}},
		"matching mode": {map[string]any{
			"features": map[string]any{
				"runtime_agent_instances": map[string]any{"mode": "assertion_only"},
			},
		}, requirements},
		"boolean gate": {map[string]any{
			"features": map[string]any{"runtime_agent_instances": true},
		}, requirements},
		"absent config values": {nil, requirements},
	} {
		if err := ValidateRuntimeAuthModeRequirement(tc.values, tc.requirements); err != nil {
			t.Fatalf("%s: unexpected error %v", name, err)
		}
		if err := applyManagedConstrainedOverrides(tc.values, tc.requirements); err != nil {
			t.Fatalf("%s: load path error %v", name, err)
		}
	}
}
