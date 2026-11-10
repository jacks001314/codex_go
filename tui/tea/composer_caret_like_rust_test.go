package tea

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	codextui "codex_go/tui"
)

// reverseVideoPrefix returns the plain text the composer painted before the
// caret cell. Bubbles renders the caret as a reverse-video cell, so the text in
// front of that sequence is the caret's painted position.
func reverseVideoPrefix(t *testing.T, rendered string) string {
	t.Helper()
	index := -1
	for _, marker := range []string{"\x1b[7m", "\x1b[7;"} {
		if found := strings.Index(rendered, marker); found >= 0 && (index < 0 || found < index) {
			index = found
		}
	}
	if index < 0 {
		t.Fatalf("rendered composer has no visible caret:\n%q", rendered)
	}
	return ansi.Strip(rendered[:index])
}

func newCaretComposerModel(t *testing.T, width int, height int) *Model {
	t.Helper()
	return NewModel(codextui.NewState(nil), Options{Width: width, Height: height})
}

// TestComposerCaretRendersAfterDraftLikeRust pins the full-height prompt render
// used to top-anchor the window: resetting the textarea viewport must not move
// the painted caret to the first row, which is where the draft's leading
// character was being shown under the block cursor.
func TestComposerCaretRendersAfterDraftLikeRust(t *testing.T) {
	model := newCaretComposerModel(t, 80, 24)
	model.composer.SetValue("ni hao a")
	model.composer.CursorEnd()

	prefix := reverseVideoPrefix(t, model.renderComposerWindow())
	if !strings.HasSuffix(prefix, "ni hao a") {
		t.Fatalf("caret painted at %q, want it after the whole draft", prefix)
	}
	if got := model.composer.Value(); got != "ni hao a" {
		t.Fatalf("draft after render = %q, want it unchanged", got)
	}
	if line, column := model.composer.Line(), model.composer.Column(); line != 0 || column != 8 {
		t.Fatalf("caret after render = (%d,%d), want (0,8)", line, column)
	}
}

// TestComposerCaretRendersAtCursorPositionLikeRust checks that the painted
// caret follows the model's caret instead of the render pass resetting it, so a
// mid-draft edit keeps the block cursor over the character being replaced.
func TestComposerCaretRendersAtCursorPositionLikeRust(t *testing.T) {
	model := newCaretComposerModel(t, 80, 24)
	model.composer.SetValue("ni hao a")
	model.composer.SetCursorColumn(7)

	prefix := reverseVideoPrefix(t, model.renderComposerWindow())
	if !strings.HasSuffix(prefix, "ni hao ") {
		t.Fatalf("caret painted at %q, want it over the last character", prefix)
	}
}

// TestComposerCaretStaysInScrolledWindowLikeRust covers the long-draft path:
// the window is cropped to the last composerTargetRows rows and the caret still
// has to be painted on the row it belongs to.
func TestComposerCaretStaysInScrolledWindowLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		Width:               40,
		Height:              30,
		AlternateScreenMode: "always",
	})
	lines := make([]string, 30)
	for index := range lines {
		lines[index] = "draft-row-" + string(rune('a'+index%26))
	}
	lines[len(lines)-1] = "draft-row-last"
	model.composer.SetValue(strings.Join(lines, "\n"))
	model.composer.CursorEnd()

	window := model.renderComposerWindow()
	rows := strings.Split(window, "\n")
	if len(rows) != model.composerTargetRows() {
		t.Fatalf("composer window rows = %d, want %d", len(rows), model.composerTargetRows())
	}
	prefix := reverseVideoPrefix(t, rows[len(rows)-1])
	if !strings.HasSuffix(prefix, "draft-row-last") {
		t.Fatalf("caret painted at %q, want it after the last visible draft row", prefix)
	}
}
