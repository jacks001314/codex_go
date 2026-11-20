package tui

// Rust parity: codex-rs/tui/src/ui_tree_tests.rs (upstream #62637). Centered and
// end-aligned rows place each wrapped line inside its own available width, paint
// the alignment padding with the row background, keep that padding out of link
// targets, and leave authored blank lines unpainted.

import (
	"strings"
	"testing"
)

func uiTreeAlignedRow(gap int, wrap string, justify string, background string, children ...any) map[string]any {
	props := map[string]any{"gap": gap}
	if wrap != "" {
		props["wrap"] = wrap
	}
	if justify != "" {
		props["justify"] = justify
	}
	if background != "" {
		props["background"] = background
	}
	return map[string]any{"type": "row", "props": props, "children": children}
}

func uiTreeAlignedLines(t *testing.T, value map[string]any, width int, height int) []UILine {
	t.Helper()
	return uiTreeJustifyTree(t, value).Prepare().RenderLines(width, height)
}

func uiTreeAlignedPainted(t *testing.T, value map[string]any, width int, height int) string {
	t.Helper()
	rows := uiTreePreparedRows(uiTreeJustifyTree(t, value), width, height)
	return strings.Join(rows, "\n")
}

func uiTreeLinkedColumns(line UILine) string {
	start, end := uiTreeLinkedRange(line)
	if start == end {
		return ""
	}
	return line.Text[start:end]
}

// uiTreeLinkedRange reports the columns the rendered line links, merging the
// per-chunk hyperlink runs the renderer emits.
func uiTreeLinkedRange(line UILine) (int, int) {
	start := -1
	end := -1
	for _, link := range line.Hyperlinks {
		if start < 0 || link.Start < start {
			start = link.Start
		}
		if link.End > end {
			end = link.End
		}
	}
	if start < 0 {
		return 0, 0
	}
	return start, end
}

func uiTreeBackgroundCells(line UILine) int {
	count := 0
	for _, span := range line.Spans {
		if span.Style.Background != UIColor("red") {
			continue
		}
		count += DisplayWidth(span.Text)
	}
	return count
}

func TestUITreeCenterAndEndAlignEachWrappedLineLikeRust(t *testing.T) {
	layout := func(justify string) string {
		children := []any{uiTreeText("界"), uiTreeText("bb"), uiTreeText("xyz")}
		rows := uiTreeJustifyRows(t, uiTreeAlignedRow(1, "wrap", justify, "", children...), 8, 2)
		for index, row := range rows {
			rows[index] = "|" + row + "|"
		}
		return strings.Join(rows, "\n")
	}
	if got := layout("center"); got != "| 界 bb|\n|  xyz|" {
		t.Fatalf("center layout = %q", got)
	}
	if got := layout("end"); got != "|   界 bb|\n|     xyz|" {
		t.Fatalf("end layout = %q", got)
	}
}

func TestUITreeEndAlignmentUsesSparePaddingForOverflowLikeRust(t *testing.T) {
	tree := uiTreeAlignedRow(1, "wrap", "end", "", "界", uiTreeText("12345678"))
	prepared := uiTreeJustifyTree(t, tree).Prepare()
	if width, height := prepared.Measure(8, 1); width != 8 || height != 1 {
		t.Fatalf("measure = %dx%d, want 8x1", width, height)
	}
	if got := "|" + uiTreeAlignedPainted(t, tree, 8, 1) + "|"; got != "|     界…|" {
		t.Fatalf("one-row render = %q", got)
	}
	if got := uiTreeAlignedPainted(t, tree, 8, 2); got != "      界\n12345678" {
		t.Fatalf("two-row render = %q", got)
	}
}

func TestUITreeCenterAndEndPlaceStringsAsOneItemLikeRust(t *testing.T) {
	layout := func(justify string) [2]string {
		inner := uiTreeAlignedRow(1, "", justify, "", "L", nil, "R")
		outer := uiTreeAlignedRow(0, "", "", "", inner)
		return [2]string{
			uiTreeAlignedPainted(t, inner, 10, 1),
			uiTreeAlignedPainted(t, outer, 10, 1),
		}
	}
	if got := layout("center"); got != [2]string{"    LR", "LR"} {
		t.Fatalf("center layout = %#v", got)
	}
	if got := layout("end"); got != [2]string{"        LR", "LR"} {
		t.Fatalf("end layout = %#v", got)
	}
}

func TestUITreeAlignedRowsPadContentAndKeepLinksOnTextLikeRust(t *testing.T) {
	const href = "https://example.com/pr"
	for _, testCase := range []struct{ justify, expected string }{{"center", "  PR"}, {"end", "     PR"}} {
		row := func(child any) map[string]any {
			return uiTreeAlignedRow(0, "", testCase.justify, "", child)
		}
		blank := uiTreeJustifyTree(t, row("\n")).Prepare()
		if width, height := blank.Measure(5, 2); width != 0 || height != 1 {
			t.Fatalf("%s blank measure = %dx%d, want 0x1", testCase.justify, width, height)
		}
		if got := uiTreeAlignedPainted(t, row("abcdef"), 3, 1); got != "ab…" {
			t.Fatalf("%s overflow = %q", testCase.justify, got)
		}
		link := row(map[string]any{"type": "link", "props": map[string]any{"href": href}, "children": []any{"PR"}})
		lines := uiTreeAlignedLines(t, link, 7, 1)
		if len(lines) != 1 || lines[0].Text != testCase.expected {
			t.Fatalf("%s link line = %#v", testCase.justify, lines)
		}
		if got := uiTreeLinkedColumns(lines[0]); got != "PR" {
			t.Fatalf("%s linked columns = %q, want the link text only", testCase.justify, got)
		}
	}
}

func TestUITreeOffscreenBlankLinesCountTowardNestedHeightLikeRust(t *testing.T) {
	for _, justify := range []string{"center", "end"} {
		inner := uiTreeAlignedRow(6, "", justify, "", uiTreeText(""), uiTreeText("\n\nB"))
		tree := uiTreeAlignedRow(0, "wrap", "", "", inner, "ok")
		if got := uiTreeAlignedPainted(t, tree, 5, 1); got != "…" {
			t.Fatalf("%s one-row render = %q", justify, got)
		}
		if got := uiTreeAlignedPainted(t, tree, 5, 2); got != "\n…" {
			t.Fatalf("%s two-row render = %q", justify, got)
		}
	}
}

func TestUITreeNestedAlignedRowsLeaveBlankLinesUnpaintedLikeRust(t *testing.T) {
	for _, justify := range []string{"center", "end"} {
		row := func(children []any) map[string]any {
			inner := uiTreeAlignedRow(1, "", "", "", children...)
			return uiTreeAlignedRow(0, "", justify, "red", inner)
		}
		blank := uiTreeJustifyTree(t, row([]any{"\n", "\n"})).Prepare()
		if width, height := blank.Measure(8, 2); width != 0 || height != 1 {
			t.Fatalf("%s blank measure = %dx%d, want 0x1", justify, width, height)
		}
		lines := uiTreeAlignedLines(t, row([]any{"A\n\n", "B\n\n"}), 8, 2)
		if len(lines) == 0 || uiTreeBackgroundCells(lines[0]) == 0 {
			t.Fatalf("%s first line has no row background: %#v", justify, lines)
		}
		if len(lines) > 1 && strings.TrimSpace(lines[1].Text) != "" {
			t.Fatalf("%s painted an authored blank line: %#v", justify, lines[1])
		}
	}
}

func TestUITreeStartAndBetweenKeepGapsBeforeBlankChildrenLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		justify string
		blank   int
		visible int
	}{{"start", 1, 2}, {"between", 8, 8}} {
		for _, rowCase := range []struct {
			first    any
			expected int
		}{{"\n", testCase.blank}, {"A", testCase.visible}} {
			tree := uiTreeAlignedRow(1, "", testCase.justify, "red", rowCase.first, uiTreeText("\n"))
			prepared := uiTreeJustifyTree(t, tree).Prepare()
			if width, _ := prepared.Measure(8, 1); width != rowCase.expected {
				t.Fatalf("%s first %#v measure width = %d, want %d", testCase.justify, rowCase.first, width, rowCase.expected)
			}
			lines := uiTreeAlignedLines(t, tree, 8, 1)
			if len(lines) != 1 || uiTreeBackgroundCells(lines[0]) != rowCase.expected {
				t.Fatalf("%s first %#v background cells = %#v", testCase.justify, rowCase.first, lines)
			}
		}
	}
}

func TestUITreeLinkAroundAlignedRowKeepsPaddingUnlinkedLikeRust(t *testing.T) {
	const href = "https://example.com/pr"
	for _, testCase := range []struct {
		justify string
		leading int
	}{{"center", 2}, {"end", 5}} {
		inner := uiTreeAlignedRow(1, "", testCase.justify, "red", "P", uiTreeText("R"))
		tree := map[string]any{
			"type":     "link",
			"props":    map[string]any{"href": href},
			"children": []any{inner},
		}
		lines := uiTreeAlignedLines(t, tree, 8, 1)
		if len(lines) != 1 {
			t.Fatalf("%s lines = %#v", testCase.justify, lines)
		}
		line := lines[0]
		if !strings.HasPrefix(line.Text, strings.Repeat(" ", testCase.leading)) {
			t.Fatalf("%s text = %q", testCase.justify, line.Text)
		}
		if start, end := uiTreeLinkedRange(line); start != testCase.leading || end != testCase.leading+3 {
			t.Fatalf("%s hyperlink columns = %d..%d, want %d..%d", testCase.justify, start, end, testCase.leading, testCase.leading+3)
		}
		for _, span := range line.Spans {
			if span.Style.Background != UIColor("red") {
				t.Fatalf("%s span lost the row background: %#v", testCase.justify, span)
			}
		}
		if got := uiTreeLinkedColumns(line); got != "P R" {
			t.Fatalf("%s linked columns = %q", testCase.justify, got)
		}
	}
}
