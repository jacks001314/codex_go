package appserver

import (
	"strings"
	"testing"

	"codex_go/session"
)

// TestSearchSnippetNormalizesWhitespaceOnlyAfterMatching mirrors Rust #60846:
// the match runs against the raw text, so a query containing newlines or tabs
// still hits, while the returned excerpt collapses whitespace runs.
func TestSearchSnippetNormalizesWhitespaceOnlyAfterMatching(t *testing.T) {
	record := &session.Record{Title: "Fixture", Preview: "preview"}
	record.Items = []session.Item{{Type: "user_message", Role: "user", Text: "find  \"needle\"\nunder\tC:\\docs"}}

	got := searchSnippet(record, "find  \"needle\"\nunder\tC:\\docs")
	want := "find \"needle\" under C:\\docs"
	if got != want {
		t.Fatalf("snippet = %q, want %q", got, want)
	}
}

// TestSearchSnippetKeepsEllipsesForTrimmedWindows pins the surrounding context
// markers Rust adds when the excerpt is cut out of a longer text.
func TestSearchSnippetKeepsEllipsesForTrimmedWindows(t *testing.T) {
	pad := "0123456789"
	record := &session.Record{Title: "Fixture", Preview: "preview"}
	record.Items = []session.Item{{Type: "user_message", Role: "user", Text: pad + pad + pad + pad + pad + pad + "needle" + pad + pad + pad + pad + pad + pad + pad + pad + pad + pad}}

	got := searchSnippet(record, "needle")
	if len(got) < len("needle") {
		t.Fatalf("snippet = %q", got)
	}
	if !strings.HasPrefix(got, "... ") || !strings.HasSuffix(got, " ...") {
		t.Fatalf("snippet = %q, want surrounding ellipses", got)
	}
}
