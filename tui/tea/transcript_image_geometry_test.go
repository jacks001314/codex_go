package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	historycell "codex_go/tui/history_cell"
	"codex_go/tui/transcript_image"
)

func TestTranscriptImageViewFillsTerminalHeight(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 30}, {60, 16}} {
		model := NewModel(codextui.NewState(nil), Options{Width: size[0], Height: size[1]})
		view := model.View()
		rows := strings.Count(view, "\n") + 1
		if rows != size[1]-1 {
			t.Fatalf("%dx%d view rows = %d, want terminal height minus the reserved cursor row", size[0], size[1], rows)
		}
		if model.transcriptImageFrameRows != rows {
			t.Fatalf("%dx%d recorded frame rows = %d, want %d", size[0], size[1], model.transcriptImageFrameRows, rows)
		}
	}
}

func TestTranscriptImagePositionMatchesInlineRendererLikeRust(t *testing.T) {
	path := writeTranscriptTestPNG(t, 40, 32)
	model := NewModel(codextui.NewState(nil), Options{
		Width:               80,
		Height:              24,
		LocalSession:        true,
		PetEnv:              map[string]string{"KITTY_WINDOW_ID": "1"},
		InitialHistoryCells: []historycell.HistoryCell{historycell.NewImageGenerationCall("img-1", "completed", "a red cube", path)},
	})
	model.transcriptImageCell = transcript_image.CellSize{Width: 8, Height: 16}
	view := model.View()

	// Bubble Tea's inline renderer starts at the shell cursor on the bottom
	// row; each newline scrolls the terminal instead of resetting to row zero.
	terminal := newTestVirtualTerminal(80, 24)
	terminal.moveTo(0, 23)
	terminal.WriteString(strings.ReplaceAll(view, "\n", "\r\n"))

	targets := model.transcriptImages.targetsLocked()
	if len(targets) != 1 {
		t.Fatalf("targets = %#v", targets)
	}
	terminal.moveTo(targets[0].key.x, targets[0].key.y)
	terminal.WriteString("IMG")

	lines := strings.Split(terminal.Snapshot(), "\n")
	imageRow := -1
	generatedRow := -1
	composerRow := -1
	for index, line := range lines {
		switch {
		case strings.Contains(line, "IMG"):
			imageRow = index
			if strings.TrimSpace(line) != "IMG" {
				t.Fatalf("image row %d overlaps text: %q", index, line)
			}
		case strings.Contains(line, "Generated Image"):
			generatedRow = index
		case strings.Contains(line, "Ask gcode"):
			composerRow = index
		}
	}
	if imageRow < 0 || generatedRow < 0 || composerRow < 0 {
		t.Fatalf("rows = image:%d generated:%d composer:%d\n%s", imageRow, generatedRow, composerRow, terminal.Snapshot())
	}
	if imageRow <= generatedRow || imageRow >= composerRow {
		t.Fatalf("image row %d is outside the transcript (generated %d, composer %d)", imageRow, generatedRow, composerRow)
	}
}
