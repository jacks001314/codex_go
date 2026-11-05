package config

import (
	"strings"
	"testing"

	"codex_go/execpolicy"
)

// TestRequirementsRulesPrefixRulesParseLikeRust pins Rust #58045's managed
// `[rules]` section: prompt-user is accepted, the rule keeps its justification,
// and the policy fingerprints per rule.
func TestRequirementsRulesPrefixRulesParseLikeRust(t *testing.T) {
	body := `
[rules]
prefix_rules = [
    { pattern = [{ token = "rm" }], decision = "prompt-user", justification = "managed checkpoint" },
    { pattern = [{ any_of = ["curl", "wget"] }, { token = "-O" }], decision = "forbidden" },
]
`
	requirements, err := ParseRequirementsTOML([]byte(body))
	if err != nil {
		t.Fatalf("ParseRequirementsTOML() error = %v", err)
	}
	if requirements == nil || requirements.Rules == nil {
		t.Fatal("requirements.rules = nil")
	}
	evaluation := requirements.Rules.Policy().EvaluateCommand([]string{"rm", "-rf", "build"})
	if evaluation.Decision == nil || *evaluation.Decision != execpolicy.DecisionPromptUser {
		t.Fatalf("rm decision = %#v, want prompt-user", evaluation.Decision)
	}
	download := requirements.Rules.Policy().EvaluateCommand([]string{"curl", "-O", "https://example.com"})
	if download.Decision == nil || *download.Decision != execpolicy.DecisionForbidden {
		t.Fatalf("curl decision = %#v, want forbidden", download.Decision)
	}
	if fingerprint := requirements.Rules.Fingerprint(); len(fingerprint) != 2 {
		t.Fatalf("fingerprint = %#v", fingerprint)
	}
}

// TestRequirementsRulesRejectAllowLikeRust pins the managed-safety rule: an
// allow decision is not permitted in requirements.toml.
func TestRequirementsRulesRejectAllowLikeRust(t *testing.T) {
	body := `
[rules]
prefix_rules = [
    { pattern = [{ token = "rm" }], decision = "allow" },
]
`
	_, err := ParseRequirementsTOML([]byte(body))
	if err == nil || !strings.Contains(err.Error(), "has decision 'allow', which is not permitted in requirements.toml") {
		t.Fatalf("ParseRequirementsTOML() error = %v", err)
	}
	if !strings.Contains(err.Error(), "use 'prompt', 'prompt-user', or 'forbidden'") {
		t.Fatalf("allow error text = %v", err)
	}
}

// TestRequirementsRulesValidateLikeRust pins the remaining validation paths.
func TestRequirementsRulesValidateLikeRust(t *testing.T) {
	cases := map[string]string{
		"missing decision": "is missing a decision",
		"empty pattern":    "has an empty pattern",
		"empty rules":      "rules prefix_rules cannot be empty",
	}
	bodies := map[string]string{
		"missing decision": "[rules]\nprefix_rules = [{ pattern = [{ token = \"rm\" }] }]\n",
		"empty pattern":    "[rules]\nprefix_rules = [{ pattern = [], decision = \"prompt\" }]\n",
		"empty rules":      "[rules]\nprefix_rules = []\n",
	}
	for name, want := range cases {
		_, err := ParseRequirementsTOML([]byte(bodies[name]))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s error = %v, want %q", name, err, want)
		}
	}
}

func TestRequirementsCanonicalMatchingLikeRust(t *testing.T) {
	policy, err := rulesRequirementFromAny(map[string]any{
		"prefix_rules": []any{map[string]any{
			"pattern":            []any{map[string]any{"token": "bash"}},
			"canonical_matching": true,
			"decision":           "forbidden",
		}},
	})
	if err != nil {
		t.Fatalf("rulesRequirementFromAny() error = %v", err)
	}
	if !policy.Policy().Rules[0].CanonicalMatching {
		t.Fatalf("canonical matching not retained: %#v", policy.Policy().Rules[0])
	}
	if out := policy.Policy().EvaluateCommand([]string{"bAſh"}); out.Decision == nil || *out.Decision != execpolicy.DecisionForbidden {
		t.Fatalf("canonical managed rule decision = %#v", out.Decision)
	}
}
