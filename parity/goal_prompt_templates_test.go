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

	continuation := gitOutput(t, rustRepo, "show", candidateRustTo+":codex-rs/ext/goal/templates/goals/continuation.md")
	budget := gitOutput(t, rustRepo, "show", candidateRustTo+":codex-rs/ext/goal/templates/goals/budget_limit.md")
	objective := gitOutput(t, rustRepo, "show", candidateRustTo+":codex-rs/ext/goal/templates/goals/objective_updated.md")

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
