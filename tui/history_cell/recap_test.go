package historycell

import (
	"strings"
	"testing"

	"codex_go/tui"
)

func TestThreadRecapHistoryCellUsesHangingRecapLayout(t *testing.T) {
	cell := NewThreadRecapHistoryCell("Automatic recaps stay compact on wide terminals.")
	rendered := strings.Join(cell.DisplayLines(100), "\n")
	want := "  \u21b3 Recap: Automatic recaps stay compact on wide terminals."
	if rendered != want {
		t.Fatalf("rendered =\n%q\nwant\n%q", rendered, want)
	}
}

func TestThreadRecapHistoryCellWrapsInNarrowTerminals(t *testing.T) {
	cell := NewThreadRecapHistoryCell("Keep conversation recaps readable in narrow terminals.")
	rendered := strings.Join(cell.DisplayLines(32), "\n")
	want := "  \u21b3 Recap: Keep conversation\n           recaps readable in\n           narrow terminals."
	if rendered != want {
		t.Fatalf("rendered =\n%q\nwant\n%q", rendered, want)
	}
}

func TestThreadRecapHistoryCellPreservesLineBreaksAndNextAction(t *testing.T) {
	next := "run the focused tests."
	cell := NewThreadRecapHistoryCell("Finished the parser.").WithNextAction(&next)
	if raw := strings.Join(cell.RawLines(), "\n"); raw != "Conversation recap\nFinished the parser.\nNext: run the focused tests." {
		t.Fatalf("raw = %q", raw)
	}
	rendered := strings.Join(cell.DisplayLines(100), "\n")
	want := "  \u21b3 Recap: Finished the parser.\n           Next: run the focused tests."
	if rendered != want {
		t.Fatalf("rendered =\n%q\nwant\n%q", rendered, want)
	}
}

func TestThreadRecapHistoryCellKeepsNextActionMultiline(t *testing.T) {
	next := "run the focused tests.\nthen push."
	cell := NewThreadRecapHistoryCell("Done.").WithNextAction(&next)
	if raw := strings.Join(cell.RawLines(), "\n"); raw != "Conversation recap\nDone.\nNext: run the focused tests.\nthen push." {
		t.Fatalf("raw = %q", raw)
	}
}

func TestThreadRecapHistoryCellFallsBackWhenPrefixDoesNotFit(t *testing.T) {
	cell := NewThreadRecapHistoryCell("word word word")
	lines := cell.DisplayLines(12)
	if len(lines) == 0 || lines[0] != "\u21b3 Recap:" {
		t.Fatalf("narrow header = %#v", lines)
	}
	for _, line := range lines[1:] {
		if strings.HasPrefix(line, "  \u21b3") {
			t.Fatalf("narrow layout kept the hanging prefix: %#v", lines)
		}
	}
	if got := cell.DisplayLines(0); got != nil {
		t.Fatalf("zero width should render nothing: %#v", got)
	}
}

// TestThreadRecapHistoryCellWrapsNextActionURLsInNarrowTerminals mirrors Rust's
// #45089 URL-preserving narrow wrap.
func TestThreadRecapHistoryCellWrapsNextActionURLsInNarrowTerminals(t *testing.T) {
	next := "Review https://example.com/review/42."
	cell := NewThreadRecapHistoryCell("The caf\u00e9 draft is ready.").WithNextAction(&next)
	lines := cell.DisplayLines(32)
	joined := strings.Join(lines, "\n")
	for _, line := range lines {
		if tui.DisplayWidth(line) > 30 {
			t.Fatalf("line exceeds the wrap width: %q\n%s", line, joined)
		}
	}
	if !strings.Contains(joined, "Next: ") {
		t.Fatalf("next action missing:\n%s", joined)
	}
}

func TestThreadRecapHistoryCellBoundsUnicodeAtRuneBoundary(t *testing.T) {
	cell := NewThreadRecapHistoryCell(strings.Repeat("\u6700\u65b0\u306e\u9032\u6357\U0001f31f", 20))
	rendered := strings.Join(cell.DisplayLines(40), "\n")
	if !strings.HasPrefix(rendered, "  \u21b3 Recap: ") {
		t.Fatalf("rendered =\n%s", rendered)
	}
}

func TestThreadRecapLoadingCellRows(t *testing.T) {
	cell := NewThreadRecapLoadingCell()
	if got := cell.DisplayLines(40); len(got) != 1 || got[0] != "\u2022 Generating conversation recap\u2026" {
		t.Fatalf("display = %#v", got)
	}
	if got := cell.RawLines(); len(got) != 1 || got[0] != "Generating conversation recap..." {
		t.Fatalf("raw = %#v", got)
	}
}
