package chatwidget

import (
	"strings"
	"testing"
)

// Rust parity: codex-rs/tui/src/transcript_view/select_mode/blocks.rs and
// select_mode_tests.rs (#63047). The pager rebuilds the Markdown block tree
// from rendered rows, so these tests drive real rendered transcripts.

func blockRow(t *testing.T, overlay *TranscriptOverlay, needle string) int {
	t.Helper()
	for index, line := range overlay.plainLines {
		if strings.Contains(line, needle) {
			return index
		}
	}
	t.Fatalf("row containing %q not found in:\n%s", needle, overlay.content)
	return 0
}

func newBlockSelectOverlay(t *testing.T, source string) *TranscriptOverlay {
	t.Helper()
	overlay := NewTranscriptOverlay(80, 24, tableSelectContent(t, source))
	overlay.BeginSelection()
	if !overlay.SelectionActive() {
		t.Fatal("selection mode did not start")
	}
	return overlay
}

func markBlockAt(t *testing.T, overlay *TranscriptOverlay, needle string) {
	t.Helper()
	row := blockRow(t, overlay, needle)
	overlay.selectMode.anchor = selectPoint{Row: row}
	overlay.selectMode.activeEnd = overlay.selectMode.anchor
	if action := overlay.HandleSelectCommand(SelectCmdMarkMarkdown); action != SelectActionChanged {
		t.Fatalf("mark markdown at %q action = %v", needle, action)
	}
}

func TestMarkdownMarkCopiesParagraphsAndMovesBetweenSiblingsLikeRust(t *testing.T) {
	overlay := newBlockSelectOverlay(t, "first paragraph text\n\nsecond paragraph text\n\n- item one\n- item two")
	markBlockAt(t, overlay, "first paragraph text")
	if got := overlay.SelectedText(); got != "first paragraph text" {
		t.Fatalf("paragraph copy = %q", got)
	}
	// `l` moves both ends to the next sibling paragraph.
	if action := overlay.HandleSelectCommand(SelectCmdRight); action != SelectActionChanged {
		t.Fatalf("next sibling action = %v", action)
	}
	if got := overlay.SelectedText(); got != "second paragraph text" {
		t.Fatalf("sibling paragraph copy = %q", got)
	}
	// `h` returns to the first paragraph; `j` extends across both.
	overlay.HandleSelectCommand(SelectCmdLeft)
	if got := overlay.SelectedText(); got != "first paragraph text" {
		t.Fatalf("back to first paragraph = %q", got)
	}
	overlay.HandleSelectCommand(SelectCmdDown)
	want := "first paragraph text\n\nsecond paragraph text"
	if got := overlay.SelectedText(); got != want {
		t.Fatalf("extended paragraph copy = %q, want %q", got, want)
	}
	// Toggling the mark off clears the copy.
	if action := overlay.HandleSelectCommand(SelectCmdMarkMarkdown); action != SelectActionChanged {
		t.Fatalf("toggle off action = %v", action)
	}
	if overlay.SelectionMarked() || overlay.SelectedText() != "" {
		t.Fatalf("second mark markdown did not clear: marked=%v text=%q", overlay.SelectionMarked(), overlay.SelectedText())
	}
}

func TestMarkdownMarkCopiesListMarkersLikeRust(t *testing.T) {
	overlay := newBlockSelectOverlay(t, "- alpha\n- beta\n- gamma")
	markBlockAt(t, overlay, "beta")
	if got := overlay.SelectedText(); got != "- beta" {
		t.Fatalf("list item copy = %q", got)
	}
	// Tab expands the item to its list and Shift+Tab restores it.
	if action := overlay.HandleSelectCommand(SelectCmdExpandBlock); action != SelectActionChanged {
		t.Fatalf("expand list action = %v", action)
	}
	if got := overlay.SelectedText(); got != "- alpha\n- beta\n- gamma" {
		t.Fatalf("list copy = %q", got)
	}
	if action := overlay.HandleSelectCommand(SelectCmdShrinkBlock); action != SelectActionChanged {
		t.Fatalf("shrink list action = %v", action)
	}
	if got := overlay.SelectedText(); got != "- beta" {
		t.Fatalf("shrunk item copy = %q", got)
	}
}

func TestMarkdownMarkExpandsNestedItemsLikeRust(t *testing.T) {
	overlay := newBlockSelectOverlay(t, "- alpha\n  - beta\n  - gamma\n- delta")
	markBlockAt(t, overlay, "beta")
	if got := overlay.SelectedText(); !strings.HasSuffix(strings.TrimSpace(got), "- beta") {
		t.Fatalf("nested item copy = %q", got)
	}
	// Nested list, then the containing item, then the outer list.
	overlay.HandleSelectCommand(SelectCmdExpandBlock)
	got := overlay.SelectedText()
	if !strings.Contains(got, "- beta") || !strings.Contains(got, "- gamma") || strings.Contains(got, "alpha") {
		t.Fatalf("nested list copy = %q", got)
	}
	overlay.HandleSelectCommand(SelectCmdExpandBlock)
	got = overlay.SelectedText()
	if !strings.Contains(got, "- alpha") || !strings.Contains(got, "- beta") || strings.Contains(got, "delta") {
		t.Fatalf("containing item copy = %q", got)
	}
	overlay.HandleSelectCommand(SelectCmdExpandBlock)
	got = overlay.SelectedText()
	if !strings.Contains(got, "- alpha") || !strings.Contains(got, "- delta") {
		t.Fatalf("outer list copy = %q", got)
	}
}

func TestMarkdownMarkExpandsHeadingSectionsLikeRust(t *testing.T) {
	source := "# Title\n\nintro paragraph\n\n## Sub\n\nsub text\n\n# Other\n\nother text"
	overlay := newBlockSelectOverlay(t, source)
	markBlockAt(t, overlay, "sub text")
	if got := overlay.SelectedText(); got != "sub text" {
		t.Fatalf("subsection paragraph copy = %q", got)
	}
	overlay.HandleSelectCommand(SelectCmdExpandBlock)
	if got := overlay.SelectedText(); got != "## Sub\n\nsub text" {
		t.Fatalf("subsection copy = %q", got)
	}
	overlay.HandleSelectCommand(SelectCmdExpandBlock)
	want := "# Title\n\nintro paragraph\n\n## Sub\n\nsub text"
	if got := overlay.SelectedText(); got != want {
		t.Fatalf("section copy = %q, want %q", got, want)
	}
	// The next same-rank heading ends the section.
	if strings.Contains(overlay.SelectedText(), "other text") {
		t.Fatal("section swallowed the next heading")
	}
	// Shift+Tab unwinds one level and heading expansion stops at the section.
	overlay.HandleSelectCommand(SelectCmdShrinkBlock)
	if got := overlay.SelectedText(); got != "## Sub\n\nsub text" {
		t.Fatalf("shrunk subsection copy = %q", got)
	}
}

func TestMarkdownMarkCopiesQuotesLikeRust(t *testing.T) {
	overlay := newBlockSelectOverlay(t, "> quote one\n>\n> quote two\n\nplain text")
	markBlockAt(t, overlay, "quote one")
	want := "> quote one\n>\n> quote two"
	if got := overlay.SelectedText(); got != want {
		t.Fatalf("quote copy = %q, want %q", got, want)
	}
}

func TestMarkdownMarkRoundTripsTableRectangleLikeRust(t *testing.T) {
	overlay := newBlockSelectOverlay(t, tableSelectSource)
	cell := tableCellPosition{Table: 0, Row: 2, Col: 1}
	overlay.selectMode.anchor = overlay.tableCellPoint(cell)
	overlay.selectMode.activeEnd = overlay.selectMode.anchor
	overlay.HandleSelectCommand(SelectCmdMarkMarkdown)
	if got := overlay.SelectedText(); got != "Open" {
		t.Fatalf("cell mark copy = %q", got)
	}
	if action := overlay.HandleSelectCommand(SelectCmdExpandBlock); action != SelectActionChanged {
		t.Fatalf("expand table action = %v", action)
	}
	want := "| Task | Status |\n|---|---|\n| One | Done |\n| Two | Open |"
	if got := overlay.SelectedText(); got != want {
		t.Fatalf("table block copy = %q, want %q", got, want)
	}
	if action := overlay.HandleSelectCommand(SelectCmdShrinkBlock); action != SelectActionChanged {
		t.Fatalf("shrink table action = %v", action)
	}
	if got := overlay.SelectedText(); got != "Open" {
		t.Fatalf("restored cell copy = %q", got)
	}
}

func TestMarkdownMarkCopiesRawFencesWithLanguageLikeRust(t *testing.T) {
	content := "```go\nfunc main() {}\n```\n\nplain text"
	overlay := NewTranscriptOverlay(80, 24, content)
	overlay.BeginSelection()
	row := blockRow(t, overlay, "func main()")
	overlay.selectMode.anchor = selectPoint{Row: row}
	overlay.selectMode.activeEnd = overlay.selectMode.anchor
	if action := overlay.HandleSelectCommand(SelectCmdMarkMarkdown); action != SelectActionChanged {
		t.Fatalf("fence mark action = %v", action)
	}
	want := "```go\nfunc main() {}\n```"
	if got := overlay.SelectedText(); got != want {
		t.Fatalf("fence copy = %q, want %q", got, want)
	}
}

func TestMarkdownMarkHintNamesTheBlockLikeRust(t *testing.T) {
	overlay := newBlockSelectOverlay(t, "# Title\n\nparagraph")
	if hint := overlay.BlockMarkHint(); hint != "" {
		t.Fatalf("hint before marking = %q", hint)
	}
	markBlockAt(t, overlay, "# Title")
	hint := overlay.BlockMarkHint()
	if !strings.Contains(hint, "heading") || !strings.Contains(hint, "tab expand") {
		t.Fatalf("heading hint = %q", hint)
	}
}
