package config

import "testing"

// TestGuardianPolicyConfigLikeRust mirrors Rust Config::guardian_policy_config:
// the managed `guardian_policy_config` requirement wins over `[auto_review]
// policy`, a value that normalizes to empty is treated as unset so the
// lower-priority layer still applies, and `[auto_review]
// experimental_policy_template` is the reviewer prompt template.
func TestGuardianPolicyConfigLikeRust(t *testing.T) {
	managed := "  Managed policy.  "
	cfg := &Config{
		Values: map[string]any{"auto_review": map[string]any{
			"policy":                       "Config policy.",
			"experimental_policy_template": "Template: {{ tenant_policy_config }}",
		}},
		Requirements: &ConfigRequirements{GuardianPolicyConfig: &managed},
	}
	if got, ok := cfg.GuardianPolicyConfig(); !ok || got != "Managed policy." {
		t.Fatalf("GuardianPolicyConfig() = %q, %v, want the managed policy", got, ok)
	}
	if got, ok := cfg.GuardianPolicyTemplate(); !ok || got != "Template: {{ tenant_policy_config }}" {
		t.Fatalf("GuardianPolicyTemplate() = %q, %v", got, ok)
	}

	// A requirement that normalizes to empty falls back to the config value.
	empty := " \n\t "
	cfg.Requirements.GuardianPolicyConfig = &empty
	if got, ok := cfg.GuardianPolicyConfig(); !ok || got != "Config policy." {
		t.Fatalf("GuardianPolicyConfig() with an empty requirement = %q, %v", got, ok)
	}

	// Unset everywhere reports no override.
	if got, ok := (&Config{}).GuardianPolicyConfig(); ok || got != "" {
		t.Fatalf("GuardianPolicyConfig() = %q, %v, want unset", got, ok)
	}
	if got, ok := (&Config{}).GuardianPolicyTemplate(); ok || got != "" {
		t.Fatalf("GuardianPolicyTemplate() = %q, %v, want unset", got, ok)
	}
}

// TestRequirementsFileParsesGuardianPolicyConfig mirrors Rust
// requirements_toml.guardian_policy_config.
func TestRequirementsFileParsesGuardianPolicyConfig(t *testing.T) {
	requirements, err := ParseRequirementsTOML([]byte("guardian_policy_config = \"  Managed policy.  \"\n"))
	if err != nil {
		t.Fatalf("ParseRequirementsTOML() error = %v", err)
	}
	// Go's requirements parser trims string requirements (as it does for the
	// other managed paths); the Config accessor normalizes again, so an
	// empty-after-trim requirement still lets the config value apply.
	if requirements == nil || requirements.GuardianPolicyConfig == nil || *requirements.GuardianPolicyConfig != "Managed policy." {
		t.Fatalf("requirements = %#v, want the managed policy", requirements)
	}
}

// TestGuardianExtraPolicyConfigLikeRust mirrors Rust #47125's
// Config::guardian_extra_policy precedence: the managed `guardian_extra_policy`
// requirement wins over `[auto_review] extra_policy`, and a blank value in
// either layer is ignored.
func TestGuardianExtraPolicyConfigLikeRust(t *testing.T) {
	managed := "  Managed extra policy.  "
	cfg := &Config{
		Values:       map[string]any{"auto_review": map[string]any{"extra_policy": "Config extra policy."}},
		Requirements: &ConfigRequirements{GuardianExtraPolicy: &managed},
	}
	if got, ok := cfg.GuardianExtraPolicy(); !ok || got != "Managed extra policy." {
		t.Fatalf("GuardianExtraPolicy() = %q, %v, want the managed policy", got, ok)
	}

	empty := " \n\t "
	cfg.Requirements.GuardianExtraPolicy = &empty
	if got, ok := cfg.GuardianExtraPolicy(); !ok || got != "Config extra policy." {
		t.Fatalf("GuardianExtraPolicy() with an empty requirement = %q, %v", got, ok)
	}

	cfg.Values = map[string]any{"auto_review": map[string]any{"extra_policy": "   "}}
	if got, ok := cfg.GuardianExtraPolicy(); ok || got != "" {
		t.Fatalf("GuardianExtraPolicy() with a blank config value = %q, %v, want unset", got, ok)
	}
	if got, ok := (&Config{}).GuardianExtraPolicy(); ok || got != "" {
		t.Fatalf("GuardianExtraPolicy() = %q, %v, want unset", got, ok)
	}
}

// TestRequirementsFileParsesGuardianExtraPolicy mirrors Rust
// requirements_toml.guardian_extra_policy.
func TestRequirementsFileParsesGuardianExtraPolicy(t *testing.T) {
	requirements, err := ParseRequirementsTOML([]byte("guardian_extra_policy = \"  Managed extra policy.  \"\n"))
	if err != nil {
		t.Fatalf("ParseRequirementsTOML() error = %v", err)
	}
	if requirements == nil || requirements.GuardianExtraPolicy == nil || *requirements.GuardianExtraPolicy != "Managed extra policy." {
		t.Fatalf("requirements = %#v, want the managed extra policy", requirements)
	}
	if configRequirementsEmpty(requirements) {
		t.Fatal("configRequirementsEmpty() = true for a guardian_extra_policy-only requirement")
	}
}
