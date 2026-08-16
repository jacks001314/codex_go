package tea

import (
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"
)

// These tests verify the composer editor kill/yank actions (ctrl-k / ctrl-u /
// kill_whole_line / ctrl-y) that the bubbletea textarea does not handle
// natively, mirroring Rust bottom_pane/textarea.rs: kills cut into a
// single-entry kill buffer and yank pastes it at the cursor.

func editorKey(keyType bubbletea.KeyType) bubbletea.KeyMsg {
	return bubbletea.KeyMsg{Type: keyType}
}

// TestEditorKillLineEndAndStartLikeRust pins ctrl-k (kill to line end) and
// ctrl-u (kill to line start) on a single line.
func TestEditorKillLineEndAndStartLikeRust(t *testing.T) {
	m := vimTestModel("hello brave world")
	m.composer.SetCursor(6)
	m = vimEscape(m) // no-op; vimTestModel starts in normal mode with Vim on
	updated, _ := m.Update(editorKey(bubbletea.KeyCtrlK))
	m = updated.(*Model)
	if got := m.composer.Value(); got != "hello " {
		t.Fatalf("ctrl-k value = %q, want hello ", got)
	}
	if got := m.composerKillBuffer; got != "brave world" {
		t.Fatalf("kill buffer = %q, want brave world", got)
	}

	m = vimTestModel("hello brave world")
	m.composer.SetCursor(6)
	updated, _ = m.Update(editorKey(bubbletea.KeyCtrlU))
	m = updated.(*Model)
	if got := m.composer.Value(); got != "brave world" {
		t.Fatalf("ctrl-u value = %q, want brave world", got)
	}
	if got := m.composerKillBuffer; got != "hello " {
		t.Fatalf("kill buffer = %q, want hello ", got)
	}
}

// TestEditorKillAtLineBoundaryMergesLinesLikeRust pins the boundary behavior:
// ctrl-k at the line end kills the trailing newline, ctrl-u at the line start
// kills the preceding newline, merging the lines.
func TestEditorKillAtLineBoundaryMergesLinesLikeRust(t *testing.T) {
	m := vimTestModel("line one\nline two")
	// Move to the end of line zero (row 0, col 8): SetValue leaves the cursor
	// on the last row, so move up first.
	m.composer.CursorUp()
	m.composer.SetCursor(0)
	m = vimKeyPress(m, '$')
	updated, _ := m.Update(editorKey(bubbletea.KeyCtrlK))
	m = updated.(*Model)
	if got := m.composer.Value(); got != "line oneline two" {
		t.Fatalf("ctrl-k at EOL value = %q, want line oneline two", got)
	}
	if got := m.composerKillBuffer; got != "\n" {
		t.Fatalf("kill buffer = %q, want newline", got)
	}

	m = vimTestModel("line one\nline two")
	// Move to the start of line one (row 1, col 0).
	// Cursor is already on the last row (line one).
	m.composer.SetCursor(0)
	updated, _ = m.Update(editorKey(bubbletea.KeyCtrlU))
	m = updated.(*Model)
	if got := m.composer.Value(); got != "line oneline two" {
		t.Fatalf("ctrl-u at BOL value = %q, want line oneline two", got)
	}
}

// TestEditorKillWholeLineAndYankLikeRust pins kill_whole_line and ctrl-y.
func TestEditorKillWholeLineAndYankLikeRust(t *testing.T) {
	m := vimTestModel("line zero\nline one\nline two")
	m.composer.CursorUp()
	m.composer.CursorUp()
	m.composer.SetCursor(0)
	// Kill the whole first line (ctrl-k twice? no - use the whole-line action;
	// it has no default binding in the keymap, so drive editorKillRange via
	// the helper through a ctrl-k at BOL is not whole-line. Call the model
	// method directly for the no-default-binding action.)
	m.killComposerWholeLine()
	if got := m.composer.Value(); got != "line one\nline two" {
		t.Fatalf("kill_whole_line value = %q, want line one\\nline two", got)
	}
	if got := m.composerKillBuffer; got != "line zero\n" {
		t.Fatalf("kill buffer = %q, want line zero\\n", got)
	}

	// yank pastes the buffer at the cursor (row 0, col 0).
	m.composer.SetCursor(0)
	updated, _ := m.Update(editorKey(bubbletea.KeyCtrlY))
	m = updated.(*Model)
	if got := m.composer.Value(); got != "line zero\nline one\nline two" {
		t.Fatalf("ctrl-y value = %q, want restored line zero\\nline one\\nline two", got)
	}
}
