package tea

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	codextui "codex_go/tui"
)

// Rust #58718 paints the conversation inside the supplied bounds and places
// the terminal cursor in viewport coordinates: the caret row/column must be
// translated by the inset origin, not left relative to the inset rectangle.
func TestConversationInsetOffsetsComposerCursorLikeRust(t *testing.T) {
	model := insetTestModel(t)
	inset := ConversationRect{X: 4, Y: 3, Width: 30, Height: 12}
	model.conversationInset = &inset
	model.composer.SetValue("hello inset")

	view := model.View()
	if view.Cursor == nil {
		t.Fatal("expected a composer cursor")
	}
	lines := strings.Split(ansi.Strip(view.Content), "\n")
	row := -1
	for index, line := range lines {
		if strings.Contains(line, "hello inset") {
			row = index
			break
		}
	}
	if row < 0 {
		t.Fatalf("draft row missing from the inset view: %q", view.Content)
	}
	if view.Cursor.Y != row {
		t.Fatalf("cursor row = %d, want the draft row %d", view.Cursor.Y, row)
	}
	prefix := lines[row][:strings.Index(lines[row], "hello inset")]
	wantX := codextui.DisplayWidth(prefix) + codextui.DisplayWidth("hello inset")
	if view.Cursor.X != wantX {
		t.Fatalf("cursor column = %d, want %d on %q", view.Cursor.X, wantX, lines[row])
	}
	if view.Cursor.Y < inset.Y || view.Cursor.Y >= inset.Y+inset.Height {
		t.Fatalf("cursor row %d escaped the inset rows %d..%d", view.Cursor.Y, inset.Y, inset.Y+inset.Height)
	}
}
