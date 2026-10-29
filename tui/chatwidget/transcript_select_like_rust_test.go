package chatwidget

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"
)

func newSelectOverlay(t *testing.T, content string) *TranscriptOverlay {
	t.Helper()
	overlay := NewTranscriptOverlay(40, 8, content)
	if !overlay.BeginSelection() {
		t.Fatal("BeginSelection returned false")
	}
	return overlay
}

// Rust #57221: the caret enters at the bottommost visible selectable row and
// character motions adjust it.
func TestTranscriptSelectionCharacterMotionsLikeRust(t *testing.T) {
	overlay := newSelectOverlay(t, "alpha\nbeta\ngamma")
	row, col := overlay.SelectionCursor()
	if row != 2 || col != 0 {
		t.Fatalf("entry cursor = (%d,%d), want (2,0)", row, col)
	}
	overlay.HandleSelectCommand(SelectCmdLineEnd)
	if _, col := overlay.SelectionCursor(); col != 5 {
		t.Fatalf("line end column = %d, want 5", col)
	}
	overlay.HandleSelectCommand(SelectCmdLeft)
	if _, col := overlay.SelectionCursor(); col != 4 {
		t.Fatalf("left column = %d, want 4", col)
	}
	overlay.HandleSelectCommand(SelectCmdRight)
	if _, col := overlay.SelectionCursor(); col != 5 {
		t.Fatalf("right column = %d, want 5", col)
	}
	overlay.HandleSelectCommand(SelectCmdBeginning)
	if row, col := overlay.SelectionCursor(); row != 0 || col != 0 {
		t.Fatalf("document start = (%d,%d), want (0,0)", row, col)
	}
	overlay.HandleSelectCommand(SelectCmdEnd)
	if row, col := overlay.SelectionCursor(); row != 2 || col != 5 {
		t.Fatalf("document end = (%d,%d), want (2,5)", row, col)
	}
}

// Rust #57221: word motions follow alphanumeric word boundaries across rows.
func TestTranscriptSelectionWordMotionsLikeRust(t *testing.T) {
	overlay := newSelectOverlay(t, "one two three\nfour")
	overlay.HandleSelectCommand(SelectCmdBeginning)
	overlay.HandleSelectCommand(SelectCmdWordForward)
	if _, col := overlay.SelectionCursor(); col != 4 {
		t.Fatalf("w column = %d, want 4", col)
	}
	overlay.HandleSelectCommand(SelectCmdWordForward)
	if _, col := overlay.SelectionCursor(); col != 8 {
		t.Fatalf("w column = %d, want 8", col)
	}
	overlay.HandleSelectCommand(SelectCmdWordEnd)
	if _, col := overlay.SelectionCursor(); col != 12 {
		t.Fatalf("e column = %d, want 12", col)
	}
	overlay.HandleSelectCommand(SelectCmdWordBackward)
	if row, col := overlay.SelectionCursor(); row != 0 || col != 8 {
		t.Fatalf("b = (%d,%d), want (0,8)", row, col)
	}
	// Moving past the last word continues on the next row.
	overlay.HandleSelectCommand(SelectCmdEnd)
	overlay.HandleSelectCommand(SelectCmdWordForward)
	overlay.HandleSelectCommand(SelectCmdBeginning)
	overlay.HandleSelectCommand(SelectCmdWordForward)
	overlay.HandleSelectCommand(SelectCmdWordForward)
	overlay.HandleSelectCommand(SelectCmdWordForward)
	if row, col := overlay.SelectionCursor(); row != 1 || col != 0 {
		t.Fatalf("cross-row word = (%d,%d), want (1,0)", row, col)
	}
}

// Rust #57221: `v` marks a character range, `V` marks whole rows, and `o`
// swaps the active end.
func TestTranscriptSelectionMarksLikeRust(t *testing.T) {
	overlay := newSelectOverlay(t, "hello world")
	overlay.HandleSelectCommand(SelectCmdBeginning)
	overlay.HandleSelectCommand(SelectCmdMarkCharacter)
	for i := 0; i < 5; i++ {
		overlay.HandleSelectCommand(SelectCmdRight)
	}
	if got := overlay.SelectedText(); got != "hello" {
		t.Fatalf("character selection = %q, want %q", got, "hello")
	}
	if action := overlay.HandleSelectCommand(SelectCmdCopy); action != SelectActionCopy {
		t.Fatalf("copy action = %v, want SelectActionCopy", action)
	}

	overlay.HandleSelectCommand(SelectCmdClearMark)
	if overlay.SelectionMarked() {
		t.Fatal("clearing the mark left it active")
	}
	if action := overlay.HandleSelectCommand(SelectCmdClearMark); action != SelectActionExit {
		t.Fatalf("escape without a mark = %v, want exit", action)
	}
	if overlay.SelectionActive() {
		t.Fatal("selection mode remained active after exit")
	}

	line := newSelectOverlay(t, "first\nsecond\nthird")
	line.HandleSelectCommand(SelectCmdBeginning)
	line.HandleSelectCommand(SelectCmdMarkLine)
	line.HandleSelectCommand(SelectCmdDown)
	if got := line.SelectedText(); got != "first\nsecond" {
		t.Fatalf("line selection = %q, want %q", got, "first\nsecond")
	}
	line.HandleSelectCommand(SelectCmdSwapEnds)
	line.HandleSelectCommand(SelectCmdDown)
	line.HandleSelectCommand(SelectCmdDown)
	if got := line.SelectedText(); got != "second\nthird" {
		t.Fatalf("swapped selection = %q, want %q", got, "second\nthird")
	}
}

// Rust #57221: rows keep a preferred column across short rows.
func TestTranscriptSelectionPreferredColumnLikeRust(t *testing.T) {
	overlay := newSelectOverlay(t, "longer line\nab\nlonger line")
	overlay.HandleSelectCommand(SelectCmdBeginning)
	overlay.HandleSelectCommand(SelectCmdLineEnd)
	overlay.HandleSelectCommand(SelectCmdDown)
	if row, col := overlay.SelectionCursor(); row != 1 || col != 2 {
		t.Fatalf("clamped row = (%d,%d), want (1,2)", row, col)
	}
	overlay.HandleSelectCommand(SelectCmdDown)
	if row, col := overlay.SelectionCursor(); row != 2 || col != 11 {
		t.Fatalf("restored row = (%d,%d), want (2,11)", row, col)
	}
}

// Rust #57222: `/` opens Find from selection mode and accepting a match moves
// the caret to it.
func TestTranscriptSelectionFindIntegrationLikeRust(t *testing.T) {
	overlay := newSelectOverlay(t, "alpha\nbeta alpha\ngamma")
	overlay.HandleSelectCommand(SelectCmdBeginning)
	if action := overlay.HandleSelectCommand(SelectCmdBeginFind); action != SelectActionBeginFind {
		t.Fatalf("begin find = %v", action)
	}
	overlay.editFindQuery(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune("alpha")})
	if !overlay.AcceptFind() {
		t.Fatal("AcceptFind returned false")
	}
	row, col := overlay.SelectionCursor()
	if row != 0 || col != 0 {
		t.Fatalf("match cursor = (%d,%d), want (0,0)", row, col)
	}
	if action := overlay.HandleSelectCommand(SelectCmdRepeatFindNewer); action != SelectActionChanged {
		t.Fatalf("repeat newer = %v", action)
	}
	if row, _ := overlay.SelectionCursor(); row != 1 {
		t.Fatalf("repeat newer row = %d, want 1", row)
	}
	if action := overlay.HandleSelectCommand(SelectCmdRepeatFindOlder); action != SelectActionChanged {
		t.Fatalf("repeat older = %v", action)
	}
	if row, _ := overlay.SelectionCursor(); row != 0 {
		t.Fatalf("repeat older row = %d, want 0", row)
	}
}

// Rust #57223: a left-button drag selects the covered text.
func TestTranscriptSelectionMouseDragLikeRust(t *testing.T) {
	overlay := NewTranscriptOverlay(40, 8, "alpha\nbeta\ngamma")
	press := bubbletea.MouseMsg{Action: bubbletea.MouseActionPress, Button: bubbletea.MouseButtonLeft, X: 0, Y: 1}
	if !overlay.handleSelectMouse(press) {
		t.Fatal("press was not handled")
	}
	motion := bubbletea.MouseMsg{Action: bubbletea.MouseActionMotion, Button: bubbletea.MouseButtonLeft, X: 4, Y: 2}
	overlay.handleSelectMouse(motion)
	release := bubbletea.MouseMsg{Action: bubbletea.MouseActionRelease, Button: bubbletea.MouseButtonLeft, X: 4, Y: 2}
	overlay.handleSelectMouse(release)
	if !overlay.SelectionActive() || !overlay.SelectionMarked() {
		t.Fatal("drag did not leave a marked selection")
	}
	if got := overlay.SelectedText(); got != "alpha\nbeta" {
		t.Fatalf("drag selection = %q, want %q", got, "alpha\nbeta")
	}
}

// The marked range renders in reverse video and the caret is underlined.
func TestTranscriptSelectionRendersLikeRust(t *testing.T) {
	overlay := newSelectOverlay(t, "abcdef")
	overlay.HandleSelectCommand(SelectCmdBeginning)
	overlay.HandleSelectCommand(SelectCmdMarkCharacter)
	overlay.HandleSelectCommand(SelectCmdRight)
	overlay.HandleSelectCommand(SelectCmdRight)
	view := overlay.View()
	if !strings.Contains(view, "\x1b[7mab") {
		t.Fatalf("selection is not reversed: %q", view)
	}
	if !strings.Contains(view, "\x1b[4m") {
		t.Fatalf("caret is not underlined: %q", view)
	}
}

func sendTranscriptMouse(overlay *TranscriptOverlay, action bubbletea.MouseAction, col int, row int, shift bool) bool {
	return overlay.handleSelectMouse(bubbletea.MouseMsg{
		Action: action,
		Button: bubbletea.MouseButtonLeft,
		Shift:  shift,
		X:      col,
		Y:      row,
	})
}

func clickTranscriptMouse(overlay *TranscriptOverlay, col int, row int, times int) {
	for range times {
		sendTranscriptMouse(overlay, bubbletea.MouseActionPress, col, row, false)
		sendTranscriptMouse(overlay, bubbletea.MouseActionRelease, col, row, false)
	}
}

// Rust #47414: Shift-click extends a double-clicked word from its original
// anchor in both directions, and an ordinary click still starts fresh.
func TestTranscriptShiftClickExtendsWordSelectionLikeRust(t *testing.T) {
	overlay := NewTranscriptOverlay(40, 8, "alpha beta gamma")
	// The second press lands inside "beta" within the click window.
	clickTranscriptMouse(overlay, 9, 1, 2)
	if got := overlay.SelectedText(); got != "beta" {
		t.Fatalf("double-click selection = %q, want %q", got, "beta")
	}
	for _, step := range []struct {
		col  int
		want string
	}{
		{13, "beta gamma"},
		{2, "alpha beta"},
		{13, "beta gamma"},
	} {
		if !sendTranscriptMouse(overlay, bubbletea.MouseActionPress, step.col, 1, true) {
			t.Fatalf("shift press at %d was not handled", step.col)
		}
		if got := overlay.SelectedText(); got != step.want {
			t.Fatalf("shift press at %d = %q, want %q", step.col, got, step.want)
		}
		sendTranscriptMouse(overlay, bubbletea.MouseActionRelease, step.col, 1, true)
	}
	clickTranscriptMouse(overlay, 13, 1, 1)
	if got := overlay.SelectedText(); got != "" {
		t.Fatalf("ordinary click kept selection %q", got)
	}
}

// Rust #47414: Shift-click keeps the gesture's unit and supports continued
// dragging, including selections that started as a whole row.
func TestTranscriptShiftClickPreservesUnitsAndDragLikeRust(t *testing.T) {
	for _, tc := range []struct {
		clicks int
		want   string
	}{
		{1, "beta gam"},
		{3, "alpha beta gamma"},
	} {
		overlay := NewTranscriptOverlay(40, 8, "alpha beta gamma")
		// Repeated presses on one cell raise the click count while the gesture
		// stays open, matching Rust's begin_selection(clicks) setup.
		for range tc.clicks {
			sendTranscriptMouse(overlay, bubbletea.MouseActionPress, 6, 1, false)
		}
		overlay.handleSelectMouse(bubbletea.MouseMsg{Action: bubbletea.MouseActionMotion, Button: bubbletea.MouseButtonLeft, X: 10, Y: 1})
		overlay.handleSelectMouse(bubbletea.MouseMsg{Action: bubbletea.MouseActionRelease, Button: bubbletea.MouseButtonLeft, X: 10, Y: 1})
		if !sendTranscriptMouse(overlay, bubbletea.MouseActionPress, 12, 1, true) {
			t.Fatalf("clicks=%d shift press was not handled", tc.clicks)
		}
		overlay.handleSelectMouse(bubbletea.MouseMsg{Action: bubbletea.MouseActionMotion, Button: bubbletea.MouseButtonLeft, Shift: true, X: 14, Y: 1})
		overlay.handleSelectMouse(bubbletea.MouseMsg{Action: bubbletea.MouseActionRelease, Button: bubbletea.MouseButtonLeft, Shift: true, X: 14, Y: 1})
		if got := overlay.SelectedText(); got != tc.want {
			t.Fatalf("clicks=%d selection = %q, want %q", tc.clicks, got, tc.want)
		}
	}
}
