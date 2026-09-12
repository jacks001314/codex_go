package chatwidget

import (
	"strings"
	"testing"
	"time"

	historycell "codex_go/tui/history_cell"
)

func TestSplitFlapStyledProjectionMatchesPlainText(t *testing.T) {
	now := time.Now()
	for _, role := range []string{"user", "assistant"} {
		inner := historycell.NewPlainHistoryCell([]string{"› GATE 73"})
		cell := NewSplitFlapTranscriptCell(inner, role, nil, 0, true, now)
		for _, milliseconds := range []int{0, 45, 90, 180, 285, 675} {
			elapsed := time.Duration(milliseconds) * time.Millisecond
			cell.Now = func() time.Time { return now.Add(elapsed) }
			plain := cell.DisplayLines(16)
			styled := historycell.PlainLines(cell.DisplayStyledLines(16))
			if len(plain) != len(styled) {
				t.Fatalf("%s %dms line count drift: %v vs %v", role, milliseconds, plain, styled)
			}
			for index := range plain {
				if plain[index] != styled[index] {
					t.Fatalf("%s %dms line %d drift: %q vs %q", role, milliseconds, index, plain[index], styled[index])
				}
			}
		}
	}
}

func TestSplitFlapStyledSpansCarryBoardAndSpeakerColours(t *testing.T) {
	now := time.Now()
	inner := historycell.NewPlainHistoryCell([]string{"› GATE"})
	cell := NewSplitFlapTranscriptCell(inner, "user", nil, 0, true, now)

	// While the last tile is still flipping the flipped glyph is dark grey.
	cell.Now = func() time.Time { return now.Add(90 * time.Millisecond) }
	styled := cell.DisplayStyledLines(16)
	flipping := false
	for _, span := range styled[0] {
		if span.Style.Background != flapColorBlack {
			t.Fatalf("span %q background = %q, want black", span.Text, span.Style.Background)
		}
		if span.Style.Foreground == flapColorDarkGray {
			flipping = true
		}
	}
	if !flipping {
		t.Fatalf("no flipping span at 90ms: %#v", styled[0])
	}

	// A settled tile in the afterglow window glows in the speaker's colour.
	cell.Now = func() time.Time { return now.Add(SplitFlapTileSettleDuration + 10*time.Millisecond) }
	styled = cell.DisplayStyledLines(16)
	glow := ""
	for _, span := range styled[0] {
		if span.Style.Foreground == flapColorCyan && strings.ContainsAny(span.Text, "GATE") {
			glow = span.Text
		}
	}
	if glow == "" {
		t.Fatalf("no user afterglow span: %#v", styled[0])
	}

	assistantInner := historycell.NewPlainHistoryCell([]string{"› GATE"})
	assistant := NewSplitFlapTranscriptCell(assistantInner, "assistant", nil, 0, true, now)
	assistant.Now = func() time.Time { return now.Add(SplitFlapTileSettleDuration + 10*time.Millisecond) }
	found := false
	for _, span := range assistant.DisplayStyledLines(16)[0] {
		if span.Style.Foreground == flapColorMagenta {
			found = true
		}
	}
	if !found {
		t.Fatalf("assistant afterglow missing: %#v", assistant.DisplayStyledLines(16)[0])
	}
}

func TestSplitFlapStyledReducedMotionIsUnstyled(t *testing.T) {
	now := time.Now()
	inner := historycell.NewPlainHistoryCell([]string{"› GATE"})
	cell := NewSplitFlapTranscriptCell(inner, "user", nil, 0, false, now)
	styled := cell.DisplayStyledLines(16)
	if len(styled) != 1 || styled[0].Plain() != "› GATE" {
		t.Fatalf("reduced-motion styled lines = %#v", styled)
	}
	for _, span := range styled[0] {
		if span.Style != (historycell.CellStyle{}) {
			t.Fatalf("reduced motion applied style: %#v", span.Style)
		}
	}
}
