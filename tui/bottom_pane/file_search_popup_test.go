package bottompane

import (
	"strings"
	"testing"

	"codex_go/tui"
)

func TestFileSearchPopupMatchesRustStateFlow(t *testing.T) {
	popup := NewFileSearchPopup()
	if rows := popup.Rows(80); !bottomPaneContainsRow(rows, "  loading...") {
		t.Fatalf("initial rows = %#v", rows)
	}

	popup.SetEmptyPrompt()
	if popup.CalculateRequiredHeight() != 3 {
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
	if popup.CalculateRequiredHeight() != MaxPopupRows+2 {
		t.Fatalf("height = %d, want %d", popup.CalculateRequiredHeight(), MaxPopupRows+2)
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
	if len(rows) != 2 {
		t.Fatalf("rows should stay single-line: %#v", rows)
	}
	if !strings.Contains(rows[1], "›") || !strings.Contains(rows[1], "file.go") {
		t.Fatalf("rows missing selected row:\n%s", strings.Join(rows, "\n"))
	}
	for _, row := range rows {
		if width := tui.DisplayWidth(stripANSIForSelectionTest(row)); width > 20 {
			t.Fatalf("row exceeds width: %q width=%d", row, width)
		}
	}
	if width := tui.DisplayWidth(stripANSIForSelectionTest(rows[1])); width != 20 {
		t.Fatalf("selected row should fill width: %q width=%d", rows[1], width)
	}

	popup.MoveDown()
	selected, ok = popup.SelectedMatch()
	if !ok || selected.Path != "src/main.go" {
		t.Fatalf("wrap selected = %#v ok=%v", selected, ok)
	}
}

func TestFileSearchPopupShortensWidePathsByDisplayWidth(t *testing.T) {
	popup := NewFileSearchPopup()
	popup.SetQuery("wide")
	popup.SetMatches("wide", []FileSearchMatch{{Path: "src/中文文件名很长.go"}})

	rows := popup.Rows(12)
	if len(rows) != 1 {
		t.Fatalf("wide path should shorten to one row, got %#v", rows)
	}
	for _, row := range rows {
		if width := tui.DisplayWidth(stripANSIForSelectionTest(row)); width > 12 {
			t.Fatalf("row exceeds width: %q width=%d rows=%#v", row, width, rows)
		}
	}
	if !strings.Contains(rows[0], "…") || strings.Contains(rows[0], "...") {
		t.Fatalf("wide filename should use the single-cell ellipsis: %q", rows[0])
	}
}

func TestFileSearchPopupPreservesDistinguishingFilenamesLikeRust(t *testing.T) {
	for _, tc := range []struct {
		parent string
		width  int
	}{
		{parent: "src/shared/long_directory", width: 28},
		{parent: "long_directory_name_that_fills_row", width: 40},
		{parent: "長いディレクトリ名/e\u0301tudes", width: 80},
	} {
		popup := NewFileSearchPopup()
		popup.SetQuery("parser")
		popup.SetMatches("parser", []FileSearchMatch{
			{Path: tc.parent + "/parser_alpha.rs", Indices: []int{0, 1, 2, 3, 4, 5}},
			{Path: tc.parent + "/parser_beta.rs", Indices: []int{0, 1, 2, 3, 4, 5}},
		})
		rows := popup.Rows(tc.width)
		if len(rows) != 2 {
			t.Fatalf("width %d rows = %#v", tc.width, rows)
		}
		plainRows := make([]string, len(rows))
		for i, row := range rows {
			plainRows[i] = stripANSIForSelectionTest(row)
		}
		joined := strings.Join(plainRows, "\n")
		if !strings.Contains(joined, "parser_alpha.rs") || !strings.Contains(joined, "parser_beta.rs") {
			t.Fatalf("width %d lost distinguishing filenames:\n%s", tc.width, joined)
		}
		if !strings.Contains(rows[0], "›") || !strings.Contains(rows[0], "\x1b[") {
			t.Fatalf("width %d selected row = %q", tc.width, rows[0])
		}
		if !strings.Contains(rows[1], "\x1b[1m") {
			t.Fatalf("width %d unselected fuzzy match is not highlighted: %q", tc.width, rows[1])
		}
		for _, row := range rows {
			if width := tui.DisplayWidth(stripANSIForSelectionTest(row)); width > tc.width {
				t.Fatalf("width %d row exceeds width: %q width=%d", tc.width, row, width)
			}
		}
		popup.MoveDown()
		if selected, ok := popup.SelectedMatch(); !ok || !strings.HasSuffix(selected.Path, "parser_beta.rs") {
			t.Fatalf("width %d selection identity = %#v ok=%v", tc.width, selected, ok)
		}
	}
}
