package historycell

import (
	"strings"
	"testing"
)

func TestThreadRecapHistoryCellUsesLabeledCheckpointLayout(t *testing.T) {
	cell := NewThreadRecapHistoryCell("Automatic recaps stay compact on wide terminals.")
	rendered := strings.Join(cell.DisplayLines(64), "\n")
	want := "\u2500 Conversation recap " + strings.Repeat("\u2500", 43) +
		"\n\n  Automatic recaps stay compact on wide terminals."
	if rendered != want {
		t.Fatalf("rendered =\n%q\nwant\n%q", rendered, want)
	}
}

func TestThreadRecapHistoryCellWrapsInNarrowTerminals(t *testing.T) {
	cell := NewThreadRecapHistoryCell("Keep conversation recaps readable in narrow terminals.")
	rendered := strings.Join(cell.DisplayLines(32), "\n")
	want := "\u2500 Conversation recap " + strings.Repeat("\u2500", 11) +
		"\n\n  Keep conversation recaps\n  readable in narrow terminals."
	if rendered != want {
		t.Fatalf("rendered =\n%q\nwant\n%q", rendered, want)
	}
}

func TestThreadRecapHistoryCellPreservesHeadingAndLineBreaks(t *testing.T) {
	cell := NewThreadRecapHistoryCell("Finished the parser.\nNext: run the focused tests.")
	if raw := strings.Join(cell.RawLines(), "\n"); raw != "Conversation recap\nFinished the parser.\nNext: run the focused tests." {
		t.Fatalf("raw = %q", raw)
	}
	rendered := strings.Join(cell.DisplayLines(48), "\n")
	want := "\u2500 Conversation recap " + strings.Repeat("\u2500", 27) +
		"\n\n  Finished the parser.\n  Next: run the focused tests."
	if rendered != want {
		t.Fatalf("rendered =\n%q\nwant\n%q", rendered, want)
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
