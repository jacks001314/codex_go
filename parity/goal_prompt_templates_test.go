package parity

import (
	"path/filepath"
	"testing"

	"codex_go/prompt"
)

// TestRustGoalPromptTemplatesMatchGo is the djalign dynamic-layer method-1
// shared-fixture differential for the goal developer prompt templates: Rust
// embeds ext/goal/templates/goals/*.md via include_str! (ext/goal/src/
// steering.rs) and renders them with {{ objective }}, {{ tokens_used }}, etc.
// Go mirrors the same templates in prompt/goal.go. The model-visible prompt
// text must match byte-for-byte (trailing newline included).
//
// The Rust side is pinned by blob content (candidateRustTo), so upstream edits
// break the contract instead of silently drifting.
func TestRustGoalPromptTemplatesMatchGo(t *testing.T) {
	root := rustSnapshotRoot(t)
	rustRepo := filepath.Dir(root)

	// The goal templates changed in the sync26/sync27 range (#40628 adds the
	// "No-progress check" to continuation.md), which is newer than the
	// certification baseline (candidateRustTo). Verify against the current
	// freeze target so these templates stay pinned to the latest upstream.
	target := "bde9db1375667c50dcc0c2b52532a4e2672571c2"
	continuation := gitOutput(t, rustRepo, "show", target+":codex-rs/ext/goal/templates/goals/continuation.md")
	budget := gitOutput(t, rustRepo, "show", target+":codex-rs/ext/goal/templates/goals/budget_limit.md")
	objective := gitOutput(t, rustRepo, "show", target+":codex-rs/ext/goal/templates/goals/objective_updated.md")

	cases := []struct {
		name string
		got  string
		want []byte
	}{
		{name: "continuation", got: prompt.GoalTemplateContinuation(), want: continuation},
		{name: "budget_limit", got: prompt.GoalTemplateBudgetLimit(), want: budget},
		{name: "objective_updated", got: prompt.GoalTemplateObjectiveUpdated(), want: objective},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != string(tc.want) {
				t.Fatalf("Go goal template %s differs from Rust blob:\n--- go (%d bytes) ---\n%s\n--- rust (%d bytes) ---\n%s",
					tc.name, len(tc.got), tc.got, len(tc.want), tc.want)
			}
		})
	}
}
