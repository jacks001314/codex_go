package bottompane

import (
	"testing"

	codextui "codex_go/internal/tui"
)

func TestUnifiedExecFooterSummaryMatchesRustGrammar(t *testing.T) {
	footer := NewUnifiedExecFooter()
	if !footer.IsEmpty() {
		t.Fatalf("new footer should be empty")
	}
	if changed := footer.SetProcesses([]string{"rg foo"}); !changed {
		t.Fatalf("first set should report changed")
	}
	if changed := footer.SetProcesses([]string{"rg foo"}); changed {
		t.Fatalf("same process list should not report changed")
	}
	got, ok := footer.SummaryText()
	want := "1 background terminal running · /ps to view · /stop to close"
	if !ok || got != want {
		t.Fatalf("summary = %q ok=%v, want %q", got, ok, want)
	}

	footer.SetProcesses([]string{"one", "two"})
	got, ok = footer.SummaryText()
	want = "2 background terminals running · /ps to view · /stop to close"
	if !ok || got != want {
		t.Fatalf("plural summary = %q ok=%v, want %q", got, ok, want)
	}
}

func TestUnifiedExecFooterRenderLinesAndStateCompatibility(t *testing.T) {
	footer := NewUnifiedExecFooter()
	footer.SetProcesses([]string{"one"})
	if height := footer.DesiredHeight(80); height != 1 {
		t.Fatalf("height = %d, want 1", height)
	}
	rows := footer.RenderLines(16)
	if len(rows) != 1 || rows[0] != "  1 background t" {
		t.Fatalf("rows = %#v", rows)
	}
	if rows := footer.RenderLines(3); len(rows) != 0 {
		t.Fatalf("narrow rows = %#v", rows)
	}

	summary, ok := (UnifiedExecFooterState{Processes: []string{"one"}}).SummaryText()
	if !ok || summary == "" {
		t.Fatalf("state summary = %q ok=%v", summary, ok)
	}
}

func TestUnifiedExecFooterTruncatesByDisplayWidth(t *testing.T) {
	row := truncateFooterRunes("  中文 background", 7)
	if codextui.DisplayWidth(row) > 7 {
		t.Fatalf("row exceeds display width: %q width=%d", row, codextui.DisplayWidth(row))
	}
	if row != "  中文 " {
		t.Fatalf("row = %q", row)
	}
}
