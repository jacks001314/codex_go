package exec

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/config"
	"codex_go/execpolicy"
)

func writeExecPolicyRules(t *testing.T, home string, body string) {
	t.Helper()
	rulesDir := filepath.Join(home, "rules")
	if err := os.MkdirAll(rulesDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(rulesDir, "default.rules"), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func execPolicyTestDecision(t *testing.T, policy *execpolicy.Policy, command []string) execpolicy.Decision {
	t.Helper()
	if policy == nil {
		t.Fatal("policy = nil")
	}
	output := policy.EvaluateCommand(command)
	if output == nil || output.Decision == nil {
		t.Fatalf("EvaluateCommand(%v) = %#v, want a decision", command, output)
	}
	return *output.Decision
}

// TestExecPolicyForRunLoadsOperatorRulesLikeRust pins that the in-process
// exec/TUI lanes honor $CODEX_HOME/rules like Rust's session ExecPolicyManager.
func TestExecPolicyForRunLoadsOperatorRulesLikeRust(t *testing.T) {
	home := t.TempDir()
	writeExecPolicyRules(t, home, `prefix_rule(pattern = ["git", "status"], decision = "allow")
prefix_rule(pattern = ["curl"], decision = "forbidden")`)

	policy := execPolicyForRun(home, &config.Config{Values: map[string]any{}})
	if got := execPolicyTestDecision(t, policy, []string{"git", "status", "--porcelain"}); got != execpolicy.DecisionAllow {
		t.Fatalf("git status decision = %q, want allow", got)
	}
	if got := execPolicyTestDecision(t, policy, []string{"curl", "-O", "https://example.com"}); got != execpolicy.DecisionForbidden {
		t.Fatalf("curl decision = %q, want forbidden", got)
	}
}

// TestExecPolicyForRunMergesManagedRequirementsLikeRust pins Rust's
// Policy::merge_overlay: a managed prompt rule tightens an operator allow rule.
func TestExecPolicyForRunMergesManagedRequirementsLikeRust(t *testing.T) {
	home := t.TempDir()
	writeExecPolicyRules(t, home, `prefix_rule(pattern = ["git"], decision = "allow")`)
	cfg := &config.Config{
		Values: map[string]any{},
		Requirements: &config.ConfigRequirements{Rules: execpolicy.NewRequirementsPolicy(&execpolicy.Policy{
			Rules: []execpolicy.PrefixRule{{
				Pattern:       [][]string{{"git"}},
				Decision:      execpolicy.DecisionPrompt,
				Justification: "managed prompt",
			}},
		})},
	}

	policy := execPolicyForRun(home, cfg)
	if got := execPolicyTestDecision(t, policy, []string{"git", "status"}); got != execpolicy.DecisionPrompt {
		t.Fatalf("git status decision = %q, want the managed prompt to win", got)
	}
}

func TestExecPolicyForRunWithoutRulesLikeRust(t *testing.T) {
	if policy := execPolicyForRun(t.TempDir(), nil); policy == nil {
		t.Fatal("policy = nil, want an empty policy")
	} else if output := policy.EvaluateCommand([]string{"git", "status"}); output == nil || output.Decision != nil {
		t.Fatalf("EvaluateCommand() = %#v, want no rule match", output)
	}
}

// TestExecModelIgnoresAllowPrefixRulesLikeRust pins the auto_review.ignore_rules
// gate: those models get one-time decisions without reusable allow prefixes.
func TestExecModelIgnoresAllowPrefixRulesLikeRust(t *testing.T) {
	cfg := &config.Config{
		Values:       map[string]any{},
		Requirements: &config.ConfigRequirements{AutoReview: &config.AutoReviewRequirements{IgnoreRules: []string{"gpt-5.4"}}},
	}
	if !execModelIgnoresAllowPrefixRules(cfg, "gpt-5.4") {
		t.Fatal("gpt-5.4 should ignore allow prefix rules")
	}
	if execModelIgnoresAllowPrefixRules(cfg, "gpt-5.1") {
		t.Fatal("unlisted model should keep allow prefix rules")
	}
	if execModelIgnoresAllowPrefixRules(nil, "") {
		t.Fatal("empty model should keep allow prefix rules")
	}
}
