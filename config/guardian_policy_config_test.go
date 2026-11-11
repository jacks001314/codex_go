package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGuardianPolicyConfigLikeRust mirrors Rust Config::guardian_policy_config:
// the managed `guardian_policy_config` requirement wins over `[auto_review]
// policy`, and a value that normalizes to empty is treated as unset so the
// lower-priority layer still applies. #60646 retired the config.toml policy
// template.
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
	if got, ok := cfg.GuardianPolicyTemplate(); ok || got != "" {
		t.Fatalf("GuardianPolicyTemplate() = %q, %v, want the retired config value ignored", got, ok)
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

// TestGuardianExtraPolicyConfigLikeRust mirrors Rust #47125/#60646:
// Config::guardian_extra_policy is supplied only by the managed requirement;
// the retired `[auto_review] extra_policy` value is ignored.
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
	if got, ok := cfg.GuardianExtraPolicy(); ok || got != "" {
		t.Fatalf("GuardianExtraPolicy() with an empty requirement = %q, %v, want unset", got, ok)
	}

	cfg.Values = map[string]any{"auto_review": map[string]any{"extra_policy": "Config extra policy."}}
	if got, ok := cfg.GuardianExtraPolicy(); ok || got != "" {
		t.Fatalf("GuardianExtraPolicy() with the retired config value = %q, %v, want unset", got, ok)
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

// TestGuardianConversationHistoryConfigLikeRust covers the two
// [auto_review] overrides added by #49036. A blank prompt is an explicit
// override, while a non-positive token budget keeps the bundled default.
func TestGuardianConversationHistoryConfigLikeRust(t *testing.T) {
	cfg := &Config{Values: map[string]any{"auto_review": map[string]any{
		"experimental_conversation_history_prompt": "History override.",
		"conversation_history_max_output_tokens":   int64(800),
	}}}
	if got, ok := cfg.GuardianConversationHistoryPrompt(); !ok || got != "History override." {
		t.Fatalf("GuardianConversationHistoryPrompt() = %q, %v", got, ok)
	}
	if got := cfg.GuardianConversationHistoryMaxOutputTokens(4000); got != 800 {
		t.Fatalf("GuardianConversationHistoryMaxOutputTokens() = %d, want 800", got)
	}

	cfg.Values = map[string]any{"auto_review": map[string]any{
		"experimental_conversation_history_prompt": "",
		"conversation_history_max_output_tokens":   int64(0),
	}}
	if got, ok := cfg.GuardianConversationHistoryPrompt(); !ok || got != "" {
		t.Fatalf("blank prompt override = %q, %v, want explicit empty string", got, ok)
	}
	if got := cfg.GuardianConversationHistoryMaxOutputTokens(4000); got != 4000 {
		t.Fatalf("invalid max output tokens = %d, want fallback 4000", got)
	}
}

func TestLoadGuardianConversationHistoryConfigLikeRust(t *testing.T) {
	home := t.TempDir()
	body := "[auto_review]\n" +
		"experimental_conversation_history_prompt = \"Loaded history override.\"\n" +
		"conversation_history_max_output_tokens = 900\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(home)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got, ok := cfg.GuardianConversationHistoryPrompt(); !ok || got != "Loaded history override." {
		t.Fatalf("loaded prompt = %q, %v", got, ok)
	}
	if got := cfg.GuardianConversationHistoryMaxOutputTokens(4000); got != 900 {
		t.Fatalf("loaded max output tokens = %d, want 900", got)
	}
}

// TestGuardianCircuitBreakActionLikeRust covers Rust #48796's opt-in strict
// action. The default, including an absent or unknown value, keeps legacy
// interrupted turns without a structured error.
func TestGuardianCircuitBreakActionLikeRust(t *testing.T) {
	tests := []struct {
		name string
		cfg  *Config
		want string
	}{
		{name: "nil config", want: "default"},
		{name: "absent", cfg: &Config{Values: map[string]any{}}, want: "default"},
		{name: "explicit default", cfg: &Config{Values: map[string]any{"auto_review": map[string]any{"circuit_break_action": "default"}}}, want: "default"},
		{name: "strict", cfg: &Config{Values: map[string]any{"auto_review": map[string]any{"circuit_break_action": "strict"}}}, want: "strict"},
		{name: "strict case insensitive", cfg: &Config{Values: map[string]any{"auto_review": map[string]any{"circuit_break_action": "STRICT"}}}, want: "strict"},
		{name: "unknown", cfg: &Config{Values: map[string]any{"auto_review": map[string]any{"circuit_break_action": "unknown"}}}, want: "default"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if got := testCase.cfg.GuardianCircuitBreakAction(); got != testCase.want {
				t.Fatalf("GuardianCircuitBreakAction() = %q, want %q", got, testCase.want)
			}
		})
	}
}

// Rust #59107: the denial reconsideration prompt is trimmed, blank values are
// unset, and a prompt above 1,000 UTF-8 bytes is rejected at load time.
func TestGuardianDenialReconsiderationPromptLikeRust(t *testing.T) {
	cfg := &Config{Values: map[string]any{"auto_review": map[string]any{
		"experimental_denial_reconsideration_prompt": "  Reconsider this denial.  ",
	}}}
	if got, ok := cfg.GuardianDenialReconsiderationPrompt(); !ok || got != "Reconsider this denial." {
		t.Fatalf("GuardianDenialReconsiderationPrompt() = %q, %v", got, ok)
	}
	if err := cfg.ValidateGuardianDenialReconsiderationPrompt(); err != nil {
		t.Fatalf("ValidateGuardianDenialReconsiderationPrompt() error = %v", err)
	}

	cfg.Values = map[string]any{"auto_review": map[string]any{
		"experimental_denial_reconsideration_prompt": "   ",
	}}
	if got, ok := cfg.GuardianDenialReconsiderationPrompt(); ok {
		t.Fatalf("blank prompt = %q, true, want unset", got)
	}

	oversized := strings.Repeat("x", 1001)
	cfg.Values = map[string]any{"auto_review": map[string]any{
		"experimental_denial_reconsideration_prompt": oversized,
	}}
	// Rust #59659 retired the key: an oversized legacy value stays
	// parse-compatible and no longer fails configuration loading.
	if err := cfg.ValidateGuardianDenialReconsiderationPrompt(); err != nil {
		t.Fatalf("retired prompt validation error = %v", err)
	}
	if err := ValidateConfigValues(cfg.Values); err != nil {
		t.Fatalf("ValidateConfigValues() error = %v", err)
	}

	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[auto_review]\nexperimental_denial_reconsideration_prompt = \""+strings.Repeat("y", 1001)+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(home); err != nil {
		t.Fatalf("Load() error = %v, want the retired key ignored", err)
	}
}
