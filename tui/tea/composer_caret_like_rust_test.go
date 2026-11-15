package tea

import (
	"strings"
	"testing"

	bubbletea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	codextui "codex_go/tui"
)

func composerCursor(t *testing.T, model *Model) *bubbletea.Cursor {
	t.Helper()
	view := model.View()
	if view.Cursor == nil {
		t.Fatalf("rendered composer has no visible caret:\n%q", view.Content)
	}
	return view.Cursor
}

func newCaretComposerModel(t *testing.T, width int, height int) *Model {
	t.Helper()
	return NewModel(codextui.NewState(nil), Options{Width: width, Height: height})
}

// TestComposerCaretUsesThinBarLikeRust pins the terminal cursor Rust uses for
// the composer: the draft text keeps its own cells instead of turning the
// character under the caret into a reverse-video block.
func TestComposerCaretUsesThinBarLikeRust(t *testing.T) {
	model := newCaretComposerModel(t, 80, 24)
	model.composer.SetValue("ni hao a")
	model.composer.CursorEnd()

	cursor := composerCursor(t, model)
	if cursor.Shape != bubbletea.CursorBar {
		t.Fatalf("caret shape = %v, want the thin bar cursor", cursor.Shape)
	}
	if got, want := cursor.X, codextui.DisplayWidth(model.composer.Prompt)+len("ni hao a"); got != want {
		t.Fatalf("caret column = %d, want %d", got, want)
	}
	if strings.Contains(model.renderComposerWindow(), "\x1b[7m") {
		t.Fatalf("composer painted a reverse-video block caret:\n%q", model.renderComposerWindow())
	}
	if got := model.composer.Value(); got != "ni hao a" {
		t.Fatalf("draft after render = %q, want it unchanged", got)
	}
	if line, column := model.composer.Line(), model.composer.Column(); line != 0 || column != 8 {
		t.Fatalf("caret after render = (%d,%d), want (0,8)", line, column)
	}
}

// TestComposerCaretRendersAtCursorPositionLikeRust checks that the terminal
// cursor follows the model's caret instead of the render pass resetting it, so
// a mid-draft edit keeps the bar over the character being replaced.
func TestComposerCaretRendersAtCursorPositionLikeRust(t *testing.T) {
	model := newCaretComposerModel(t, 80, 24)
	model.composer.SetValue("ni hao a")
	model.composer.SetCursorColumn(7)

	cursor := composerCursor(t, model)
	if got, want := cursor.X, codextui.DisplayWidth(model.composer.Prompt)+7; got != want {
		t.Fatalf("caret column = %d, want %d", got, want)
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

	view := model.View()
	if view.Cursor == nil {
		t.Fatalf("scrolled composer has no visible caret:\n%q", view.Content)
	}
	rows := strings.Split(ansi.Strip(view.Content), "\n")
	if view.Cursor.Y < 0 || view.Cursor.Y >= len(rows) {
		t.Fatalf("caret row = %d, want inside %d rendered rows", view.Cursor.Y, len(rows))
	}
	if row := strings.TrimRight(rows[view.Cursor.Y], " "); !strings.HasSuffix(row, "draft-row-last") {
		t.Fatalf("caret row = %q, want the last visible draft row", row)
	}
	if got, want := view.Cursor.X, codextui.DisplayWidth(model.composer.Prompt)+len("draft-row-last"); got != want {
		t.Fatalf("caret column = %d, want %d", got, want)
	}
}
