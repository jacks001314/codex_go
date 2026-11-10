package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	"codex_go/utils"
)

// TestFullscreenMarkdownKeepsFittingURLOnOneRowLikeRust pins Rust #60416: a
// documentation URL that fits on its own row moves there intact instead of
// splitting across the transcript column. The expected rows are the upstream
// snapshot (codex_tui__chatwidget__tests__rate_limit_switch... variant of
// conversation_in_offset_rect) rendered through Go's Markdown column.
func TestFullscreenMarkdownKeepsFittingURLOnOneRowLikeRust(t *testing.T) {
	message := codextui.Message{
		Role: codextui.RoleAssistant,
		Text: "See the documentation at https://example.com/docs/reference.",
	}
	// The assistant cell reserves two columns for its bullet gutter, so a
	// 46-column transcript renders a 44-column Markdown column like the Rust
	// snapshot.
	rows := make([]string, 0, 4)
	for _, line := range transcriptMessageDisplayLines(message, 46, "", true, ".", false) {
		trimmed := strings.TrimRight(utils.StripANSI(line), " ")
		if trimmed == "" {
			continue
		}
		rows = append(rows, trimmed)
	}
	want := []string{
		"• See the documentation at",
		"  https://example.com/docs/reference.",
	}
	if len(rows) != len(want) {
		t.Fatalf("rows = %#v, want %#v", rows, want)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Fatalf("row %d = %q, want %q (all rows %#v)", i, rows[i], want[i], rows)
		}
	}
	if strings.Count(strings.Join(rows, "\n"), "https://example.com/docs/reference") != 1 {
		t.Fatalf("the URL must stay on exactly one row: %#v", rows)
	}
}
