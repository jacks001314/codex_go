package tea

import (
	"testing"

	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
)

// composerUndoTestModel binds undo/redo to platform-independent keys so the
// assertions do not depend on the host's default chords (#61954).
func composerUndoTestModel(t *testing.T) *Model {
	t.Helper()
	keymap := codextui.NewKeymapConfig()
	if err := keymap.Set("composer", "undo", []string{"f6"}); err != nil {
		t.Fatalf("set composer.undo: %v", err)
	}
	if err := keymap.Set("composer", "redo", []string{"f5"}); err != nil {
		t.Fatalf("set composer.redo: %v", err)
	}
	return NewModel(codextui.NewState(nil), Options{Width: 200, Height: 36, KeymapConfig: keymap})
}

func composerPress(m *Model, msg keymsg.KeyMsg) *Model {
	updated, _ := m.Update(msg)
	return updated.(*Model)
}

func composerTypeText(m *Model, text string) *Model {
	for _, r := range text {
		m = composerPress(m, keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune{r}})
	}
	return m
}

func composerUndo(m *Model) *Model { return composerPress(m, keymsg.KeyMsg{Type: keymsg.KeyF6}) }
func composerRedo(m *Model) *Model { return composerPress(m, keymsg.KeyMsg{Type: keymsg.KeyF5}) }

// Rust #61954: one run of typing undoes as a single step and redoes back.
func TestComposerUndoGroupsTypingLikeRust(t *testing.T) {
	m := composerUndoTestModel(t)
	m = composerTypeText(m, "abc")
	if got := m.composer.Value(); got != "abc" {
		t.Fatalf("draft = %q, want abc", got)
	}
	m = composerUndo(m)
	if got := m.composer.Value(); got != "" {
		t.Fatalf("after undo = %q, want the whole typing run removed", got)
	}
	m = composerRedo(m)
	if got := m.composer.Value(); got != "abc" {
		t.Fatalf("after redo = %q, want abc", got)
	}
}

// Consecutive Backspaces form one step, so undo restores the pre-deletion text
// instead of replaying one character at a time.
func TestComposerUndoGroupsBackspacesLikeRust(t *testing.T) {
	m := composerUndoTestModel(t)
	m = composerTypeText(m, "abcd")
	m = composerPress(m, keymsg.KeyMsg{Type: keymsg.KeyBackspace})
	m = composerPress(m, keymsg.KeyMsg{Type: keymsg.KeyBackspace})
	if got := m.composer.Value(); got != "ab" {
		t.Fatalf("draft after backspace = %q, want ab", got)
	}
	m = composerUndo(m)
	if got := m.composer.Value(); got != "abcd" {
		t.Fatalf("after undo = %q, want abcd", got)
	}
}

// A detected paste is its own undo step and never merges with preceding typing.
func TestComposerUndoKeepsPasteSeparateLikeRust(t *testing.T) {
	m := composerUndoTestModel(t)
	m = composerTypeText(m, "ab")
	m = composerPress(m, keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune("cd"), Paste: true})
	if got := m.composer.Value(); got != "abcd" {
		t.Fatalf("draft after paste = %q, want abcd", got)
	}
	m = composerUndo(m)
	if got := m.composer.Value(); got != "ab" {
		t.Fatalf("first undo = %q, want the paste removed", got)
	}
	m = composerUndo(m)
	if got := m.composer.Value(); got != "" {
		t.Fatalf("second undo = %q, want the typing removed", got)
	}
}

// New input after an undo invalidates redo, and undo/redo are consumed even
// when the history is empty.
func TestComposerUndoInvalidatesRedoAndConsumesEmptyLikeRust(t *testing.T) {
	m := composerUndoTestModel(t)
	m = composerTypeText(m, "ab")
	m = composerUndo(m)
	m = composerTypeText(m, "c")
	m = composerRedo(m)
	if got := m.composer.Value(); got != "c" {
		t.Fatalf("redo after new input = %q, want c", got)
	}

	empty := composerUndoTestModel(t)
	empty = composerUndo(empty)
	empty = composerRedo(empty)
	if got := empty.composer.Value(); got != "" {
		t.Fatalf("empty-history draft = %q, want empty", got)
	}
}
