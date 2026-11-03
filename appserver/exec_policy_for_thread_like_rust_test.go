package appserver

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/config"
	"codex_go/execpolicy"
	"codex_go/session"
)

func writeExecPolicyTestRules(t *testing.T, home string, files map[string]string) {
	t.Helper()
	rulesDir := filepath.Join(home, "rules")
	if err := os.MkdirAll(rulesDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(rulesDir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}
}

func execPolicyDecision(t *testing.T, policy *execpolicy.Policy, command []string) execpolicy.Decision {
	t.Helper()
	if policy == nil {
		t.Fatalf("policy = nil")
	}
	output := policy.EvaluateCommand(command)
	if output == nil || output.Decision == nil {
		t.Fatalf("EvaluateCommand(%v) = %#v, want a decision", command, output)
	}
	return *output.Decision
}

// TestExecPolicyForThreadLoadsEveryUserRuleFileLikeRust pins Rust's
// collect_policy_files: every `*.rules` file under `$CODEX_HOME/rules` is
// loaded, in lexical order, not just default.rules.
func TestExecPolicyForThreadLoadsEveryUserRuleFileLikeRust(t *testing.T) {
	home := t.TempDir()
	writeExecPolicyTestRules(t, home, map[string]string{
		"default.rules": `prefix_rule(pattern = ["git"], decision = "allow")`,
		"extra.rules":   `prefix_rule(pattern = ["curl"], decision = "forbidden")`,
	})
	router := NewDefaultRuntimeRouterWithOptions(session.NewStore(home), home, &RuntimeRouterOptions{})
	t.Cleanup(func() { _ = router.Close() })

	policy := router.execPolicyForThread("thread-rules")
	if got := execPolicyDecision(t, policy, []string{"git", "status"}); got != execpolicy.DecisionAllow {
		t.Fatalf("git status decision = %q, want allow", got)
	}
	if got := execPolicyDecision(t, policy, []string{"curl", "-O", "https://example.com"}); got != execpolicy.DecisionForbidden {
		t.Fatalf("curl decision = %q, want forbidden", got)
	}
}

// TestExecPolicyForThreadMergesManagedRequirementsLikeRust pins Rust's
// Policy::merge_overlay: a managed prompt rule tightens a user allow rule for
// the same program.
func TestExecPolicyForThreadMergesManagedRequirementsLikeRust(t *testing.T) {
	home := t.TempDir()
	writeExecPolicyTestRules(t, home, map[string]string{
		"default.rules": `prefix_rule(pattern = ["git"], decision = "allow")`,
	})
	requirements := &config.ConfigRequirements{Rules: execpolicy.NewRequirementsPolicy(&execpolicy.Policy{
		Rules: []execpolicy.PrefixRule{{
			Pattern:       [][]string{{"git"}},
			Decision:      execpolicy.DecisionPrompt,
			Justification: "managed prompt",
		}},
	})}
	router := NewDefaultRuntimeRouterWithOptions(session.NewStore(home), home, &RuntimeRouterOptions{Requirements: requirements})
	t.Cleanup(func() { _ = router.Close() })

	policy := router.execPolicyForThread("thread-managed")
	if got := execPolicyDecision(t, policy, []string{"git", "status"}); got != execpolicy.DecisionPrompt {
		t.Fatalf("git status decision = %q, want the managed prompt to win", got)
	}
}

// TestExecPolicyForThreadStartsEmptyWithoutRulesLikeRust pins the missing
// directory case: Rust's session policy starts empty rather than failing.
func TestExecPolicyForThreadStartsEmptyWithoutRulesLikeRust(t *testing.T) {
	home := t.TempDir()
	router := NewDefaultRuntimeRouterWithOptions(session.NewStore(home), home, &RuntimeRouterOptions{})
	t.Cleanup(func() { _ = router.Close() })

	policy := router.execPolicyForThread("thread-empty")
	if policy == nil {
		t.Fatal("policy = nil, want an empty policy")
	}
	if output := policy.EvaluateCommand([]string{"git", "status"}); output == nil || output.Decision != nil {
		t.Fatalf("EvaluateCommand() = %#v, want no rule match", output)
	}
}
