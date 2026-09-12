package historycell

import (
	"strings"
	"testing"
)

func TestSpokenHistoryCellLabelsSpeakers(t *testing.T) {
	user := NewSpokenHistoryCell("user", "hello")
	lines := user.DisplayLines(80)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], SpokenUserPrefix) || !strings.HasSuffix(lines[0], "hello") {
		t.Fatalf("user lines = %#v", lines)
	}
	assistant := NewSpokenHistoryCell("assistant", "hi")
	lines = assistant.DisplayLines(80)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], SpokenAssistantPrefix) {
		t.Fatalf("assistant lines = %#v", lines)
	}
	// An empty or unknown role renders as the primary speaker.
	unknown := NewSpokenHistoryCell("", "hi")
	if lines := unknown.DisplayLines(80); !strings.HasPrefix(lines[0], SpokenAssistantPrefix) {
		t.Fatalf("unknown role lines = %#v", lines)
	}
}

func TestSpokenHistoryCellIndentsContinuationLines(t *testing.T) {
	cell := NewSpokenHistoryCell("user", "first\nsecond")
	lines := cell.DisplayLines(80)
	if len(lines) != 2 {
		t.Fatalf("lines = %#v", lines)
	}
	if !strings.HasPrefix(lines[1], SpokenCaptionPrefix) || !strings.HasSuffix(lines[1], "second") {
		t.Fatalf("continuation line = %q", lines[1])
	}
	if raw := cell.RawLines(); len(raw) != 2 {
		t.Fatalf("raw lines = %#v", raw)
	}
	if lines := NewSpokenHistoryCell("user", "   ").DisplayLines(80); len(lines) != 1 || lines[0] != SpokenUserPrefix {
		t.Fatalf("blank caption lines = %#v", lines)
	}
}

func TestSpokenHistoryCellSatisfiesHistoryCell(t *testing.T) {
	var cell HistoryCell = NewSpokenHistoryCell("user", "text")
	if _, ok := cell.(SpokenHistoryCell); !ok {
		t.Fatalf("cell type = %T", cell)
	}
}
