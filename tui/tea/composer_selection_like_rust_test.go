package tea

import (
	"errors"
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

func newComposerSelectionModel(t *testing.T) (*Model, *string) {
	t.Helper()
	var copied string
	model := NewModel(codextui.NewState(nil), Options{
		Width:               120,
		Height:              30,
		AlternateScreenMode: "always",
		CopyOnSelect:        "always",
		OnClipboardWrite: func(text string) error {
			copied = text
			return nil
		},
	})
	model.composer.SetValue("hello world")
	model.View()
	return model, &copied
}

func dragComposerSelection(t *testing.T, model *Model, startColumn, endColumn int) bubbletea.Cmd {
	t.Helper()
	if model.composerTextTopRow < 0 {
		t.Fatalf("composer text row is not registered: %#v", model)
	}
	x := model.composerTextLeft + model.composerPromptWidth
	y := model.composerTextTopRow
	_, _ = model.Update(bubbletea.MouseMsg{
		X: x + startColumn, Y: y, Action: bubbletea.MouseActionPress, Button: bubbletea.MouseButtonLeft,
	})
	_, cmd := model.Update(bubbletea.MouseMsg{
		X: x + endColumn, Y: y, Action: bubbletea.MouseActionRelease, Button: bubbletea.MouseButtonLeft,
	})
	return cmd
}

// TestComposerSelectionCopiesAndTypingReplacesLikeRust mirrors #53753: a drag
// copies editable text on release, keeps its highlight, and the next typed rune
// replaces the selected range.
func TestComposerSelectionCopiesAndTypingReplacesLikeRust(t *testing.T) {
	model, copied := newComposerSelectionModel(t)
	cmd := dragComposerSelection(t, model, 0, len("hello"))
	runTeaCmd(t, model, cmd)
	if *copied != "hello" {
		t.Fatalf("copied composer text = %q, want hello", *copied)
	}
	if !model.composerSelection.active || model.composerSelectedText() != "hello" {
		t.Fatalf("composer selection = %#v/%q", model.composerSelection, model.composerSelectedText())
	}
	if view := model.View(); !strings.Contains(view, "\x1b[7m") {
		t.Fatalf("composer selection was not highlighted:\n%s", view)
	}
	if got := model.composer.Value(); got != "hello world" {
		t.Fatalf("composer value after copy = %q", got)
	}

	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune("X")})
	if got := model.composer.Value(); got != "X world" {
		t.Fatalf("composer value after replacement = %q, want X world", got)
	}
	if model.composerSelection.active {
		t.Fatalf("selection survived replacement: %#v", model.composerSelection)
	}
	if got := model.composerCursorKey(); got != 1 {
		t.Fatalf("cursor after replacement = %d, want 1", got)
	}
}

// TestComposerSelectionBackspaceDeletesLikeRust keeps Backspace targeting the
// selected editable range instead of one character before the caret.
func TestComposerSelectionBackspaceDeletesLikeRust(t *testing.T) {
	model, _ := newComposerSelectionModel(t)
	dragComposerSelection(t, model, 0, len("hello"))
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyBackspace})
	if got := model.composer.Value(); got != " world" {
		t.Fatalf("composer value after Backspace = %q, want %q", got, " world")
	}
	if got := model.composerCursorKey(); got != 0 {
		t.Fatalf("cursor after Backspace = %d, want 0", got)
	}
}

// TestComposerSelectionCtrlCCopiesLikeRust keeps explicit copy available when
// copy-on-select is disabled and retains the selection, like Rust #46895.
func TestComposerSelectionCtrlCCopiesLikeRust(t *testing.T) {
	model, copied := newComposerSelectionModel(t)
	model.copyOnSelectMode = "never"
	if cmd := dragComposerSelection(t, model, 0, len("hello")); cmd != nil {
		t.Fatal("copy_on_select=never copied on release")
	}
	if !model.composerSelection.active {
		t.Fatal("copy_on_select=never dropped the editable selection")
	}
	_, cmd := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyCtrlC})
	runTeaCmd(t, model, cmd)
	if *copied != "hello" {
		t.Fatalf("Ctrl+C copied %q, want hello", *copied)
	}
	if !model.composerSelection.active || model.composerSelectedText() != "hello" {
		t.Fatalf("Ctrl+C did not retain the composer selection: %#v/%q", model.composerSelection, model.composerSelectedText())
	}
	if got := model.composer.Value(); got != "hello world" {
		t.Fatalf("Ctrl+C changed the draft: %q", got)
	}
}

// TestComposerRightClickCopiesAndClearsOnlyWhenConfirmedLikeRust covers the
// right-click half of #46895: success collapses the selection, while a failed
// write keeps it highlighted and preserves the draft and cursor.
func TestComposerRightClickCopiesAndClearsOnlyWhenConfirmedLikeRust(t *testing.T) {
	model, copied := newComposerSelectionModel(t)
	model.copyOnSelectMode = "never"
	dragComposerSelection(t, model, 0, len("hello"))
	cursor := model.composerCursorKey()
	x := model.composerTextLeft + model.composerPromptWidth + 2
	y := model.composerTextTopRow

	_, cmd := model.Update(bubbletea.MouseMsg{
		X: x, Y: y, Action: bubbletea.MouseActionPress, Button: bubbletea.MouseButtonRight,
	})
	runTeaCmd(t, model, cmd)
	if *copied != "hello" {
		t.Fatalf("right-click copied %q, want hello", *copied)
	}
	if model.composerSelection.active {
		t.Fatalf("confirmed right-click did not clear the selection: %#v", model.composerSelection)
	}
	if got := model.composer.Value(); got != "hello world" || model.composerCursorKey() != cursor {
		t.Fatalf("right-click changed draft/cursor: %q/%d, want %q/%d", got, model.composerCursorKey(), "hello world", cursor)
	}

	model, _ = newComposerSelectionModel(t)
	model.copyOnSelectMode = "never"
	model.clipboardWrite = func(string) error { return errors.New("clipboard unavailable") }
	dragComposerSelection(t, model, 0, len("hello"))
	_, cmd = model.Update(bubbletea.MouseMsg{
		X: x, Y: y, Action: bubbletea.MouseActionPress, Button: bubbletea.MouseButtonRight,
	})
	runTeaCmd(t, model, cmd)
	if !model.composerSelection.active || model.composerSelectedText() != "hello" {
		t.Fatalf("failed right-click lost the selection: %#v/%q", model.composerSelection, model.composerSelectedText())
	}
	if !strings.Contains(model.notice, "Copy failed") {
		t.Fatalf("failed right-click notice = %q", model.notice)
	}
}

// A right-click outside the editable bounds is not a selection copy; it clears
// the stale highlight and leaves the press to the paste fallback.
func TestComposerRightClickOutsideSelectionFallsThroughLikeRust(t *testing.T) {
	model, copied := newComposerSelectionModel(t)
	model.copyOnSelectMode = "never"
	dragComposerSelection(t, model, 0, len("hello"))
	handled, cmd := model.handleComposerSelectionMouse(bubbletea.MouseMsg{
		X: 0, Y: 0, Action: bubbletea.MouseActionPress, Button: bubbletea.MouseButtonRight,
	})
	if handled || cmd != nil {
		t.Fatalf("outside right-click handled=%v cmd=%v", handled, cmd != nil)
	}
	if model.composerSelection.active {
		t.Fatalf("outside right-click kept the selection: %#v", model.composerSelection)
	}
	if *copied != "" {
		t.Fatalf("outside right-click copied %q", *copied)
	}
}

// TestComposerSelectionMapsWrappedRowsLikeRust verifies that a selection on a
// continuation row copies the corresponding source runes.
func TestComposerSelectionMapsWrappedRowsLikeRust(t *testing.T) {
	model, _ := newComposerSelectionModel(t)
	model.composer.SetValue(strings.Repeat("word ", 50))
	model.View()
	rows := model.composerVisualRows()
	if len(rows) < 2 || len(rows[1].text) == 0 {
		t.Fatalf("wrapped rows = %#v, want a nonempty second row", rows)
	}
	y := model.composerTextTopRow + 1
	x := model.composerTextLeft + model.composerPromptWidth
	_, _ = model.Update(bubbletea.MouseMsg{
		X: x, Y: y, Action: bubbletea.MouseActionPress, Button: bubbletea.MouseButtonLeft,
	})
	_, _ = model.Update(bubbletea.MouseMsg{
		X: x + codextui.DisplayWidth(string(rows[1].text)), Y: y,
		Action: bubbletea.MouseActionRelease, Button: bubbletea.MouseButtonLeft,
	})
	if got, want := model.composerSelectedText(), string(rows[1].text); got != want {
		t.Fatalf("wrapped selection = %q, want %q", got, want)
	}
}

// TestComposerSelectionClearsOnExternalEdit keeps stale rune offsets from
// surviving draft replacements from commands, history, or the external editor.
func TestComposerSelectionClearsOnExternalEdit(t *testing.T) {
	model, _ := newComposerSelectionModel(t)
	dragComposerSelection(t, model, 0, len("hello"))
	if !model.composerSelection.active {
		t.Fatal("selection did not start")
	}
	model.composer.SetValue("external edit")
	model.View()
	if model.composerSelection.active {
		t.Fatalf("selection survived an external edit: %#v", model.composerSelection)
	}
}

// TestComposerSelectionClearsOnFocusLossLikeRust releases the editable
// selection when the terminal loses focus.
func TestComposerSelectionClearsOnFocusLossLikeRust(t *testing.T) {
	model, _ := newComposerSelectionModel(t)
	dragComposerSelection(t, model, 0, len("hello"))
	model.Update(bubbletea.BlurMsg{})
	if model.composerSelection.active {
		t.Fatalf("selection survived focus loss: %#v", model.composerSelection)
	}
}
