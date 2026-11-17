package tea

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	codextui "codex_go/tui"
	historycell "codex_go/tui/history_cell"
	"codex_go/tui/transcript_image"
)

// Rust #58718 paints the conversation, including image previews, inside the
// supplied bounds. A preview is centered in the inset width and offset by the
// rectangle origin, while the surrounding viewport cells stay untouched for
// the host that owns them.
func TestConversationInsetPlacesImageInsideRectLikeRust(t *testing.T) {
	path := writeTranscriptTestPNG(t, 40, 32)
	inset := ConversationRect{X: 6, Y: 4, Width: 50, Height: 18}
	model := NewModel(codextui.NewState(nil), Options{
		Width:               80,
		Height:              24,
		LocalSession:        true,
		PetEnv:              map[string]string{"KITTY_WINDOW_ID": "1"},
		InitialHistoryCells: []historycell.HistoryCell{historycell.NewImageGenerationCall("img-1", "completed", "a red cube", path)},
		ConversationInset:   &inset,
	})
	model.transcriptImageCell = transcript_image.CellSize{Width: 8, Height: 16}
	view := model.viewString()

	targets := model.transcriptImages.targetsLocked()
	if len(targets) != 1 {
		t.Fatalf("targets = %#v", targets)
	}
	target := targets[0].key
	wantX := inset.X + max((inset.Width-target.columns)/2, 0)
	if target.x != wantX {
		t.Fatalf("image column = %d, want %d centered in the inset", target.x, wantX)
	}
	if target.x < inset.X || target.x+target.columns > inset.X+inset.Width {
		t.Fatalf("image columns %d..%d escape the inset %d..%d", target.x, target.x+target.columns, inset.X, inset.X+inset.Width)
	}
	if target.y < inset.Y || target.y+target.rows > inset.Y+inset.Height {
		t.Fatalf("image rows %d..%d escape the inset %d..%d", target.y, target.y+target.rows, inset.Y, inset.Y+inset.Height)
	}

	for index, line := range strings.Split(ansi.Strip(view), "\n") {
		if index < inset.Y || index >= inset.Y+inset.Height {
			if strings.TrimSpace(line) != "" {
				t.Fatalf("row %d outside the inset painted %q", index, line)
			}
			continue
		}
		if strings.TrimSpace(codextui.TruncateToWidth(line, inset.X)) != "" {
			t.Fatalf("row %d paints before the inset: %q", index, line)
		}
	}

	terminal := newTestVirtualTerminal(80, 24)
	terminal.moveTo(0, 23)
	terminal.WriteString(strings.ReplaceAll(view, "\n", "\r\n"))
	terminal.moveTo(target.x, target.y)
	terminal.WriteString("IMG")
	lines := strings.Split(terminal.Snapshot(), "\n")
	imageRow := -1
	for index, line := range lines {
		if strings.Contains(line, "IMG") {
			imageRow = index
		}
	}
	if imageRow < inset.Y || imageRow >= inset.Y+inset.Height {
		t.Fatalf("painted image row = %d, want inside the inset %d..%d\n%s", imageRow, inset.Y, inset.Y+inset.Height, terminal.Snapshot())
	}
}
