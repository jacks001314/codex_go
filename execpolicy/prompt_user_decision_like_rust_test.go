package execpolicy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestExecPolicyParsesPromptUserDecisionLikeRust pins Rust #58045: the managed
// `prompt-user` decision parses in the shared rule DSL and outranks a plain
// prompt.
func TestExecPolicyParsesPromptUserDecisionLikeRust(t *testing.T) {
	path := filepath.Join(t.TempDir(), "managed.rules")
	body := `prefix_rule(
    pattern = ["rm"],
    decision = "prompt-user",
    justification = "managed checkpoint",
)`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	policy, err := LoadPolicies([]string{path})
	if err != nil {
		t.Fatalf("LoadPolicies() error = %v", err)
	}
	if len(policy.Rules) != 1 || policy.Rules[0].Decision != DecisionPromptUser {
		t.Fatalf("rules = %#v", policy.Rules)
	}
	evaluation := policy.EvaluateCommand([]string{"rm", "-rf", "build"})
	if evaluation.Decision == nil || *evaluation.Decision != DecisionPromptUser {
		t.Fatalf("evaluation = %#v, want prompt-user", evaluation)
	}

	mixed := &Policy{Rules: []PrefixRule{
		{Pattern: [][]string{{"git"}}, Decision: DecisionPrompt},
		{Pattern: [][]string{{"git"}}, Decision: DecisionPromptUser},
	}}
	if got := mixed.EvaluateCommand([]string{"git", "push"}); got.Decision == nil || *got.Decision != DecisionPromptUser {
		t.Fatalf("strictest decision = %#v, want prompt-user", got.Decision)
	}
}

// TestExecPolicyRejectsPromptUserNetworkRuleLikeRust pins the restriction from
// Rust #58045: prompt-user is only valid for managed command rules.
func TestExecPolicyRejectsPromptUserNetworkRuleLikeRust(t *testing.T) {
	path := filepath.Join(t.TempDir(), "default.rules")
	err := AppendNetworkRule(path, "example.com", "https", DecisionPromptUser, "")
	if err == nil || !strings.Contains(err.Error(), "prompt-user is only supported in managed command rules") {
		t.Fatalf("AppendNetworkRule() error = %v", err)
	}
}
