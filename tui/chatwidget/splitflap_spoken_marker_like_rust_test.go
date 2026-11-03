package chatwidget

import (
	"strings"
	"testing"
	"time"

	historycell "codex_go/tui/history_cell"
	"codex_go/utils"
)

// Rust 3caf9f9586 changes the split-flap marker from a forced cyan to the
// span's own colour, so a live spoken prompt keeps its bold red chevron. The
// board still measures visible text only.
func TestSplitFlapKeepsSpokenPromptMarkerColourLikeRust(t *testing.T) {
	started := time.Now()
	inner := historycell.NewSpokenUserPrompt("hello")
	cell := NewSplitFlapTranscriptCell(inner, "user", nil, 0, true, started)
	if cell.Board == nil {
		t.Fatal("no board")
	}
	if cell.Board.MarkerSGR != historycell.SpokenPromptMarkerSGR {
		t.Fatalf("marker SGR = %q", cell.Board.MarkerSGR)
	}
	if strings.ContainsRune(cell.Board.Target, 0x1b) {
		t.Fatalf("board target kept escape sequences: %q", cell.Board.Target)
	}
	cell.Now = func() time.Time { return started.Add(SplitFlapAnimationDuration) }
	lines := cell.DisplayLines(40)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, historycell.SpokenPromptMarkerSGR+"\u203a") {
		t.Fatalf("settled frame lost the red chevron: %q", joined)
	}
	if !strings.Contains(joined, "hello") {
		t.Fatalf("settled frame lost its text: %q", joined)
	}
	for _, line := range lines {
		if width := len([]rune(utils.StripANSI(line))); width != 40 && width != 0 {
			t.Fatalf("row width = %d after stripping escapes: %q", width, line)
		}
	}

	// The styled projection paints the same marker red and bold.
	styled := cell.DisplayStyledLines(40)
	markerStyled := false
	for _, line := range styled {
		for _, span := range line {
			if !strings.Contains(span.Text, "\u203a") {
				continue
			}
			if span.Style.Foreground == flapColorRed && span.Style.Bold {
				markerStyled = true
			}
		}
	}
	if !markerStyled {
		t.Fatalf("styled marker was not red and bold: %#v", styled)
	}

	// A plain transcript cell keeps the previous behaviour.
	plain := NewSplitFlapTranscriptCell(historycell.NewPlainHistoryCell([]string{"\u203a GATE"}), "user", nil, 0, true, started)
	if plain.Board.MarkerSGR != "" {
		t.Fatalf("plain cell picked up a marker colour: %q", plain.Board.MarkerSGR)
	}
	plain.Now = func() time.Time { return started.Add(SplitFlapAnimationDuration) }
	if strings.Contains(strings.Join(plain.DisplayLines(20), "\n"), historycell.SpokenPromptMarkerSGR) {
		t.Fatal("plain cell painted a spoken marker")
	}
}
