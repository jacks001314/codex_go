package chatwidget

import (
	"strings"
	"testing"
	"time"

	historycell "codex_go/tui/history_cell"
)

func TestSplitFlapTranscriptCellAnimatesAndPreservesRawLines(t *testing.T) {
	now := time.Now()
	inner := historycell.NewPlainHistoryCell([]string{"› GATE 73"})
	cell := NewSplitFlapTranscriptCell(inner, "user", nil, 0, true, now)
	cell.Now = func() time.Time { return now.Add(285 * time.Millisecond) }

	lines := cell.DisplayLines(16)
	if len(lines) != 1 {
		t.Fatalf("animated lines = %#v", lines)
	}
	if got := strings.TrimRight(lines[0], " "); got != "› GATE 73 TEG" {
		t.Fatalf("animated line = %q", got)
	}
	if raw := cell.RawLines(); len(raw) != 1 || raw[0] != "› GATE 73" {
		t.Fatalf("raw lines = %#v", raw)
	}
	if _, ok := cell.AnimationTick(); !ok {
		t.Fatal("cell should still be animating at 285ms")
	}
}

func TestSplitFlapTranscriptCellReducedMotionIsTransparent(t *testing.T) {
	now := time.Now()
	inner := historycell.NewPlainHistoryCell([]string{"› GATE 73"})
	cell := NewSplitFlapTranscriptCell(inner, "user", nil, 0, false, now)
	lines := cell.DisplayLines(16)
	if len(lines) != 1 || lines[0] != "› GATE 73" {
		t.Fatalf("reduced-motion lines = %#v", lines)
	}
	if _, ok := cell.AnimationTick(); ok {
		t.Fatal("reduced motion should not animate")
	}
}

func TestSplitFlapTranscriptCellSettlesAfterTheAnimationWindow(t *testing.T) {
	now := time.Now()
	inner := historycell.NewPlainHistoryCell([]string{"› HELLO"})
	cell := NewSplitFlapTranscriptCell(inner, "assistant", nil, 0, true, now)
	cell.Now = func() time.Time { return now.Add(SplitFlapAnimationDuration) }
	lines := cell.DisplayLines(12)
	if got := strings.TrimRight(lines[0], " "); got != "› HELLO" {
		t.Fatalf("settled line = %q", got)
	}
	if _, ok := cell.AnimationTick(); ok {
		t.Fatal("settled cell still animates")
	}
}

func TestSplitFlapTranscriptCellReusesPreviousPrefixTiles(t *testing.T) {
	now := time.Now()
	firstInner := historycell.NewPlainHistoryCell([]string{"› GATE"})
	first := NewSplitFlapTranscriptCell(firstInner, "user", nil, 0, true, now)
	for index := range first.Board.TileArrivals {
		first.Board.TileArrivals[index] = first.Board.TileArrivals[index].Add(-SplitFlapAnimationDuration)
	}
	nextInner := historycell.NewPlainHistoryCell([]string{"› GATE 73"})
	next := NewSplitFlapTranscriptCell(nextInner, "user", &first, 0, true, now)
	next.Now = func() time.Time { return now }
	if got := strings.TrimRight(next.DisplayLines(16)[0], " "); !strings.HasPrefix(got, "› GATE ") {
		t.Fatalf("appended cell line = %q", got)
	}
	if len(next.Board.TileArrivals) < 4 || next.Board.TileArrivals[0] != first.Board.TileArrivals[0] {
		t.Fatal("appended cell did not retain settled tiles")
	}
}
