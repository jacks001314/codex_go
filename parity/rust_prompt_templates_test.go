package parity

import (
	"path/filepath"
	"strings"
	"testing"

	"codex_go/compact"
	"codex_go/review"
)

// TestRustCompactAndReviewTemplatesMatchGo is the shared-fixture differential
// for the remaining vendored prompt documents: the compaction prompt and
// summary prefix (prompts/templates/compact) and the review exit XML
// (prompts/templates/review).
func TestRustCompactAndReviewTemplatesMatchGo(t *testing.T) {
	root := rustSnapshotRoot(t)
	rustRepo := filepath.Dir(root)
	readprompt := func(path string) string {
		t.Helper()
		return strings.ReplaceAll(string(gitOutput(t, rustRepo, "show", "HEAD:codex-rs/"+path)), "\r\n", "\n")
	}
	cases := []struct {
		name string
		rust string
		go_  string
	}{
		{"compact/prompt.md", readprompt("prompts/templates/compact/prompt.md"), compact.SummarizationPrompt},
		{"compact/summary_prefix.md", readprompt("prompts/templates/compact/summary_prefix.md"), compact.SummaryPrefix},
		{"review/exit_interrupted.xml", readprompt("prompts/templates/review/exit_interrupted.xml"), review.RenderReviewExitInterrupted()},
		{"review/exit_success.xml", readprompt("prompts/templates/review/exit_success.xml"), review.RenderReviewExitSuccess("{{results}}")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.rust == tc.go_ {
				return
			}
			// Rust renders these documents through its template parser, which
			// trims the trailing newlines; accept only that normalization (not
			// arbitrary whitespace differences).
			if strings.TrimRight(tc.rust, "\n") == strings.TrimRight(tc.go_, "\n") {
				return
			}
			t.Fatalf("template drift for %s\nRust: %q\nGo:   %q", tc.name, tc.rust, tc.go_)
		})
	}
}
