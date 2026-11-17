package tea

import (
	"strings"
	"testing"
	"time"

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

// composerClock installs a controllable clock; the returned function advances it.
func composerClock(m *Model) func(time.Duration) {
	current := time.Unix(1_700_000_000, 0)
	m.now = func() time.Time { return current }
	return func(advance time.Duration) { current = current.Add(advance) }
}

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

// Rust continues one typing step only while the next keystroke arrives within
// half a second, so a pause starts a new undo step.
func TestComposerUndoTypingPauseStartsNewStepLikeRust(t *testing.T) {
	m := composerUndoTestModel(t)
	advance := composerClock(m)
	m = composerTypeText(m, "ab")
	advance(600 * time.Millisecond)
	m = composerTypeText(m, "cd")
	if got := m.composer.Value(); got != "abcd" {
		t.Fatalf("draft = %q, want abcd", got)
	}
	m = composerUndo(m)
	if got := m.composer.Value(); got != "ab" {
		t.Fatalf("after undo = %q, want only the second run removed", got)
	}
	m = composerUndo(m)
	if got := m.composer.Value(); got != "" {
		t.Fatalf("after second undo = %q, want the first run removed", got)
	}
}

// Even without pauses a typing run longer than a second splits into a new step.
func TestComposerUndoTypingSpanStartsNewStepLikeRust(t *testing.T) {
	m := composerUndoTestModel(t)
	advance := composerClock(m)
	m = composerTypeText(m, "ab")
	advance(400 * time.Millisecond)
	m = composerTypeText(m, "cd")
	advance(400 * time.Millisecond)
	m = composerTypeText(m, "ef")
	advance(400 * time.Millisecond)
	m = composerTypeText(m, "g")
	if got := m.composer.Value(); got != "abcdefg" {
		t.Fatalf("draft = %q, want abcdefg", got)
	}
	m = composerUndo(m)
	if got := m.composer.Value(); got != "abcdef" {
		t.Fatalf("after undo = %q, want the keystroke past the one-second span removed", got)
	}
	m = composerUndo(m)
	if got := m.composer.Value(); got != "" {
		t.Fatalf("after second undo = %q, want the whole first run removed", got)
	}
}

// Moving the cursor ends the typing run, so later typing is its own step.
func TestComposerUndoCursorMoveStartsNewStepLikeRust(t *testing.T) {
	m := composerUndoTestModel(t)
	m = composerTypeText(m, "ab")
	m = composerPress(m, keymsg.KeyMsg{Type: keymsg.KeyLeft})
	m = composerTypeText(m, "cd")
	if got := m.composer.Value(); got == "" {
		t.Fatalf("draft was cleared")
	}
	m = composerUndo(m)
	if got := m.composer.Value(); got != "ab" {
		t.Fatalf("after undo = %q, want ab", got)
	}
}

// A Backspace after a pause starts a new step, so undo restores one run at a time.
func TestComposerUndoBackspacePauseStartsNewStepLikeRust(t *testing.T) {
	m := composerUndoTestModel(t)
	advance := composerClock(m)
	m = composerTypeText(m, "abcd")
	m = composerPress(m, keymsg.KeyMsg{Type: keymsg.KeyBackspace})
	m = composerPress(m, keymsg.KeyMsg{Type: keymsg.KeyBackspace})
	advance(600 * time.Millisecond)
	m = composerPress(m, keymsg.KeyMsg{Type: keymsg.KeyBackspace})
	m = composerPress(m, keymsg.KeyMsg{Type: keymsg.KeyBackspace})
	if got := m.composer.Value(); got != "" {
		t.Fatalf("draft = %q, want empty", got)
	}
	m = composerUndo(m)
	if got := m.composer.Value(); got != "ab" {
		t.Fatalf("after undo = %q, want ab", got)
	}
	m = composerUndo(m)
	if got := m.composer.Value(); got != "abcd" {
		t.Fatalf("after second undo = %q, want abcd", got)
	}
}

// Rust raises MAX_UNDO_BYTES to 8 MiB and trims both stacks when the total
// exceeds it, so one huge paste cannot pin every older step.
func TestEditHistoryByteBudgetTrimsLikeRust(t *testing.T) {
	h := editHistory{}
	huge := strings.Repeat("x", maxEditHistoryBytes/2+1)
	h.undo = append(h.undo,
		composerDraftSnapshot{value: huge},
		composerDraftSnapshot{value: huge},
	)
	h.trim()
	if len(h.undo) != 1 {
		t.Fatalf("undo steps = %d, want the oldest dropped", len(h.undo))
	}
	h.undo = append(h.undo, composerDraftSnapshot{value: huge})
	h.redo = []composerDraftSnapshot{{value: huge}}
	h.trim()
	if len(h.undo) != 0 || len(h.redo) > 1 {
		t.Fatalf("history kept undo=%d redo=%d past the byte budget", len(h.undo), len(h.redo))
	}
}

// Moving a draft larger than the budget onto the redo stack clears it instead of
// keeping an unbounded snapshot (Rust `EditHistory::step`).
func TestEditHistoryOversizeRedoClearsLikeRust(t *testing.T) {
	h := editHistory{}
	h.undo = []composerDraftSnapshot{{value: "before"}}
	h.redo = []composerDraftSnapshot{{value: "stale"}}
	oversize := composerDraftSnapshot{value: strings.Repeat("y", maxEditHistoryBytes+1)}
	if _, ok := h.step(editUndo, oversize); !ok {
		t.Fatal("undo did not step")
	}
	if len(h.redo) != 0 {
		t.Fatalf("redo kept %d snapshots after an oversize draft", len(h.redo))
	}
}

// Rust #61954 keeps the composer editing history across Vim mode changes: only
// the Vim pending-command state resets, not the undo stack.
func TestComposerUndoSurvivesVimToggleLikeRust(t *testing.T) {
	m := composerUndoTestModel(t)
	advance := composerClock(m)
	m = composerTypeText(m, "ab")
	m.toggleVimMode()
	m.toggleVimMode()
	advance(600 * time.Millisecond)
	m = composerTypeText(m, "cd")
	if got := m.composer.Value(); got != "abcd" {
		t.Fatalf("draft = %q, want abcd", got)
	}
	m = composerUndo(m)
	if got := m.composer.Value(); got != "ab" {
		t.Fatalf("after undo = %q, want ab: the toggle must not drop the history", got)
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
