package config

import (
	"strings"
	"testing"

	"codex_go/sandbox"
)

func TestRemoteSandboxConfigMatchesHostnameLikeRust(t *testing.T) {
	// Mirrors Rust apply_remote_sandbox_config: the first entry whose hostname
	// pattern matches the execution host replaces the top-level
	// allowed_sandbox_modes.
	values := map[string]any{
		"allowed_sandbox_modes": []any{"read-only"},
		"remote_sandbox_config": []any{
			map[string]any{
				"hostname_patterns":     []any{"*.org", "runner-??.ci"},
				"allowed_sandbox_modes": []any{"workspace-write", "danger-full-access"},
			},
			map[string]any{
				"hostname_patterns":     []any{"backup-*.local"},
				"allowed_sandbox_modes": []any{"read-only"},
			},
		},
	}
	// Direct map test with a resolver.
	cfg := &ConfigRequirements{}
	configs, parseErr := parseRemoteSandboxConfigs(values)
	if parseErr != nil {
		t.Fatalf("parseRemoteSandboxConfigs: %v", parseErr)
	}
	applyRemoteSandboxConfig(cfg, configs, func() string { return "BUILD.org" })
	if len(cfg.AllowedSandboxModes) != 2 || cfg.AllowedSandboxModes[0] != sandbox.SandboxWorkspaceWrite || cfg.AllowedSandboxModes[1] != sandbox.SandboxDangerFullAccess {
		t.Fatalf("matched modes = %#v", cfg.AllowedSandboxModes)
	}

	// '?' wildcard matches a single character.
	cfg2 := &ConfigRequirements{}
	applyRemoteSandboxConfig(cfg2, configs, func() string { return "runner-01.ci" })
	if len(cfg2.AllowedSandboxModes) != 2 || cfg2.AllowedSandboxModes[0] != sandbox.SandboxWorkspaceWrite {
		t.Fatalf("? match modes = %#v", cfg2.AllowedSandboxModes)
	}

	// Non-matching hostname keeps the top-level value.
	cfg3 := &ConfigRequirements{AllowedSandboxModes: []sandbox.SandboxMode{sandbox.SandboxReadOnly}}
	applyRemoteSandboxConfig(cfg3, configs, func() string { return "other.host" })
	if len(cfg3.AllowedSandboxModes) != 1 || cfg3.AllowedSandboxModes[0] != sandbox.SandboxReadOnly {
		t.Fatalf("unmatched modes = %#v", cfg3.AllowedSandboxModes)
	}
}

func TestParseRemoteSandboxConfigRequiresHostnamePatternsListLikeRust(t *testing.T) {
	// Mirrors Rust deserialize_remote_sandbox_config_requires_hostname_patterns_list:
	// hostname_patterns must be a list; a string is a type error.
	values := map[string]any{
		"remote_sandbox_config": []any{
			map[string]any{
				"hostname_patterns":     "*.org",
				"allowed_sandbox_modes": []any{"read-only"},
			},
		},
	}
	_, err := parseRemoteSandboxConfigs(values)
	if err == nil {
		t.Fatal("string hostname_patterns must be rejected")
	}
	if !strings.Contains(err.Error(), "hostname_patterns list") {
		t.Fatalf("error = %v", err)
	}
}

func TestNormalizeHostnameAndWildcardMatchLikeRust(t *testing.T) {
	if normalized, ok := normalizeHostname("BUILD.ORG."); !ok || normalized != "build.org" {
		t.Fatalf("normalize = %q %v", normalized, ok)
	}
	if _, ok := normalizeHostname("  "); ok {
		t.Fatal("empty hostname should be rejected")
	}
	if !hostnameMatchesAnyPattern("build.org", []string{"*.org"}) {
		t.Fatal("*.org should match build.org")
	}
	if !hostnameMatchesAnyPattern("runner-01.ci", []string{"runner-??.ci"}) {
		t.Fatal("runner-??.ci should match runner-01.ci")
	}
	if hostnameMatchesAnyPattern("runner-123.ci", []string{"runner-??.ci"}) {
		t.Fatal("runner-??.ci should not match runner-123.ci (two chars vs one ?)")
	}
	if !hostnameMatchesAnyPattern("BACKUP-A.local", []string{"backup-*.local"}) {
		t.Fatal("case-insensitive backup-*.local should match BACKUP-A.local")
	}
}

// TestRemoteSandboxConfigDoesNotOverrideHigherPrecedenceModes mirrors Rust
// remote_sandbox_config_does_not_override_higher_precedence_sandbox_modes
// (config/src/config_requirements.rs): the high-precedence layer's explicit
// allowed_sandbox_modes is not replaced by a lower-precedence layer whose
// remote_sandbox_config matches the execution host. Go's merge direction
// (base = low precedence, overlay = high precedence, overlay wins) matches
// Rust merge_unset_fields (first-set wins when the higher source is merged
// first).
func TestRemoteSandboxConfigDoesNotOverrideHigherPrecedenceModes(t *testing.T) {
	// High precedence (LegacyManagedConfigTomlFromMdm): explicit read-only,
	// no remote match on the execution host.
	highValues := map[string]any{
		"allowed_sandbox_modes": []any{"read-only"},
	}
	high, err := configRequirementsFromMap(highValues)
	if err != nil {
		t.Fatalf("parse high precedence: %v", err)
	}
	// Low precedence (unknown): remote_sandbox_config matches the host and
	// would widen the modes to read-only + workspace-write.
	lowValues := map[string]any{
		"remote_sandbox_config": []any{
			map[string]any{
				"hostname_patterns":     []any{"runner-*.ci.example.com"},
				"allowed_sandbox_modes": []any{"read-only", "workspace-write"},
			},
		},
	}
	low, err := configRequirementsFromMapWithHostname(lowValues, "runner-01.ci.example.com")
	if err != nil {
		t.Fatalf("parse low precedence: %v", err)
	}
	merged := mergeConfigRequirements(low, high)
	if len(merged.AllowedSandboxModes) != 1 || merged.AllowedSandboxModes[0] != sandbox.SandboxReadOnly {
		t.Fatalf("merged modes = %#v, want [read-only] (high precedence wins)", merged.AllowedSandboxModes)
	}
}
