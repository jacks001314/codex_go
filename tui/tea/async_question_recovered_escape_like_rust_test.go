package tea

import "testing"

// TestEscapeRecoveredCommandDraftTextLikeRust pins Rust #49678: recovered
// answers escape shell and slash command drafts while preserving path-like
// slash drafts, already escaped drafts, and drafts with a literal leading space.
func TestEscapeRecoveredCommandDraftTextLikeRust(t *testing.T) {
	cases := map[string]string{
		"!echo partial":   "\\!echo partial",
		"  !echo partial": "  \\!echo partial",
		"/":               "\\/",
		"/diff":           "\\/diff",
		"\t/diff":         "\t\\/diff",
		" /diff":          " /diff",
		"/tmp/file":       "/tmp/file",
		"\\/diff":         "\\/diff",
		"echo":            "echo",
	}
	for input, want := range cases {
		if got := escapeRecoveredCommandDraftText(input); got != want {
			t.Fatalf("escapeRecoveredCommandDraftText(%q) = %q, want %q", input, got, want)
		}
	}
}
