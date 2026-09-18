package parity

import (
	"path/filepath"
	"strings"
	"testing"

	"codex_go/state"
)

// TestRustGuardianAutoReviewInstructionsMatchGo is the shared-fixture
// differential for the bundled auto-review instruction text: Rust defines
// REJECTION_INSTRUCTIONS and TIMEOUT_INSTRUCTIONS as `concat!` literals in
// prompts/src/model_messages/guardian.rs, and ResolvedAutoReviewMessages falls
// back to them whenever the model catalog omits the field, so Go vendors the
// same strings (state.GuardianRejectionInstructions /
// state.GuardianTimeoutInstructions).
//
// The Rust text is read through `git show` so a Windows autocrlf checkout
// cannot mask a real difference.
func TestRustGuardianAutoReviewInstructionsMatchGo(t *testing.T) {
	root := rustSnapshotRoot(t)
	rustRepo := filepath.Dir(root)
	source := string(gitOutput(t, rustRepo, "show", "HEAD:codex-rs/prompts/src/model_messages/guardian.rs"))
	for _, testCase := range []struct {
		rustConst string
		goValue   string
		marker    string
	}{
		{
			rustConst: "REJECTION_INSTRUCTIONS",
			goValue:   state.GuardianRejectionInstructions(),
			marker:    "The agent must not attempt to achieve the same outcome via workaround,",
		},
		{
			rustConst: "TIMEOUT_INSTRUCTIONS",
			goValue:   state.GuardianTimeoutInstructions(),
			marker:    "The automatic permission approval review did not finish before its deadline.",
		},
	} {
		want := rustConcatConst(t, source, testCase.rustConst)
		if !strings.HasPrefix(want, testCase.marker) {
			t.Fatalf("Rust %s lost %q: %q", testCase.rustConst, testCase.marker, want)
		}
		if testCase.goValue != want {
			t.Fatalf("guardian instruction drift for %s\nRust: %q\nGo:   %q", testCase.rustConst, want, testCase.goValue)
		}
	}
}

// rustConcatConst returns the concatenated string literals of one
// `const NAME: &str = concat!(...);` definition.
func rustConcatConst(t *testing.T, source, name string) string {
	t.Helper()
	marker := "const " + name + ": &str = concat!("
	start := strings.Index(source, marker)
	if start < 0 {
		t.Fatalf("Rust source lost %s", marker)
	}
	block := source[start+len(marker):]
	end := strings.Index(block, ");")
	if end < 0 {
		t.Fatalf("Rust source has an unterminated %s concat! block", name)
	}
	block = block[:end]
	var builder strings.Builder
	for {
		open := strings.IndexByte(block, '"')
		if open < 0 {
			break
		}
		rest := block[open+1:]
		close := strings.IndexByte(rest, '"')
		if close < 0 {
			t.Fatalf("Rust source has an unterminated literal in %s", name)
		}
		builder.WriteString(rest[:close])
		block = rest[close+1:]
	}
	return builder.String()
}
