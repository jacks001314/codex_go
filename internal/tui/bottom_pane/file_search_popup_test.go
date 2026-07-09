package bottompane

import (
	"strings"
	"testing"

	"codex_go/internal/tui"
)

func TestFileSearchPopupMatchesRustStateFlow(t *testing.T) {
	popup := NewFileSearchPopup()
	if rows := popup.Rows(80); !bottomPaneContainsRow(rows, "  loading...") {
		t.Fatalf("initial rows = %#v", rows)
	}

	popup.SetEmptyPrompt()
	if popup.CalculateRequiredHeight() != 1 {
		t.Fatalf("empty prompt height = %d", popup.CalculateRequiredHeight())
	}
	if rows := popup.Rows(80); !bottomPaneContainsRow(rows, "  no matches") {
		t.Fatalf("empty prompt rows = %#v", rows)
	}

	popup.SetQuery("file")
	popup.SetMatches("stale", []FileSearchMatch{{Path: "stale.go"}})
	if len(popup.Matches) != 0 || !popup.Waiting {
		t.Fatalf("stale result should be ignored: %#v", popup)
	}

	matches := make([]FileSearchMatch, 0, MaxPopupRows+2)
	for idx := 0; idx < MaxPopupRows+2; idx++ {
		matches = append(matches, FileSearchMatch{Path: "src/file_" + formatInt(idx) + ".go"})
	}
	popup.SetMatches("file", matches)
	if popup.Waiting || popup.DisplayQuery != "file" {
		t.Fatalf("popup state after matches = %#v", popup)
	}
	if len(popup.Matches) != MaxPopupRows {
		t.Fatalf("matches len = %d, want %d", len(popup.Matches), MaxPopupRows)
	}
	if popup.CalculateRequiredHeight() != MaxPopupRows {
		t.Fatalf("height = %d", popup.CalculateRequiredHeight())
	}
	selected, ok := popup.SelectedMatch()
	if !ok || selected.Path != "src/file_0.go" {
		t.Fatalf("selected = %#v ok=%v", selected, ok)
	}
}

func TestFileSearchPopupNavigationRowsAndSelectionColorBar(t *testing.T) {
	popup := NewFileSearchPopup()
	popup.SetQuery("file")
	popup.SetMatches("file", []FileSearchMatch{
		{Path: "src/main.go"},
		{Path: "src/really/long/path/component/file.go"},
	})

	popup.MoveDown()
	selected, ok := popup.SelectedMatch()
	if !ok || selected.Path != "src/really/long/path/component/file.go" {
		t.Fatalf("selected = %#v ok=%v", selected, ok)
	}
	rows := popup.Rows(20)
	if !bottomPaneContainsRow(rows, tui.RenderSelectedRow("  src/really/long/pa")) {
		t.Fatalf("rows missing selected row:\n%s", strings.Join(rows, "\n"))
	}
	for _, row := range rows {
		if strings.Contains(row, "\x1b[") && !strings.Contains(row, "...") {
			continue
		}
		if strings.Contains(row, "...") {
			t.Fatalf("file search rows should wrap instead of ellipsis-truncating:\n%s", strings.Join(rows, "\n"))
		}
	}

	popup.MoveDown()
	selected, ok = popup.SelectedMatch()
	if !ok || selected.Path != "src/main.go" {
		t.Fatalf("wrap selected = %#v ok=%v", selected, ok)
	}
}

func TestFileSearchPopupWrapsWidePathsByDisplayWidth(t *testing.T) {
	popup := NewFileSearchPopup()
	popup.SetQuery("wide")
	popup.SetMatches("wide", []FileSearchMatch{{Path: "src/中文文件名很长.go"}})

	rows := popup.Rows(12)
	if len(rows) < 2 {
		t.Fatalf("wide path should wrap, got %#v", rows)
	}
	for _, row := range rows {
		if width := tui.DisplayWidth(stripANSIForSelectionTest(row)); width > 12 {
			t.Fatalf("row exceeds width: %q width=%d rows=%#v", row, width, rows)
		}
	}
}
