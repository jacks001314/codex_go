package parity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRustReviewRubricMatchesGo is the shared-fixture differential for the
// review rubric: Rust embeds prompts/templates/review/rubric.md and Go vendors
// the same document (review/rubric.md via //go:embed, exported as
// review.ReviewPrompt), which is the reviewer instructions for both the TUI
// review flow and `codex exec review`.
//
// The Rust blob is read through `git show` so a Windows autocrlf checkout
// cannot mask a real difference, and the Go copy is compared with CRLF
// normalized to LF for the same reason.
func TestRustReviewRubricMatchesGo(t *testing.T) {
	root := rustSnapshotRoot(t)
	rustRepo := filepath.Dir(root)
	want := string(gitOutput(t, rustRepo, "show", "HEAD:codex-rs/prompts/templates/review/rubric.md"))
	if !strings.Contains(want, "## Repository Rule Attribution") {
		t.Fatalf("Rust review rubric lost its rule-attribution section:\n%s", want)
	}
	data, err := os.ReadFile(filepath.Join("..", "review", "rubric.md"))
	if err != nil {
		t.Fatalf("ReadFile(review/rubric.md): %v", err)
	}
	got := strings.ReplaceAll(string(data), "\r\n", "\n")
	if got != want {
		t.Fatalf("review rubric drift\nRust: %q\nGo:   %q", want, got)
	}
}
