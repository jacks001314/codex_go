package tui

// Rust parity: codex-rs/tui/src/ui_tree/render.rs and render/prepare.rs. These
// cases pin the prepare/layout/render pipeline against the upstream tests for
// measurement, whitespace collapsing, wrapping, overflow and link columns.

import (
	"strings"
	"testing"
)

func uiTreeText(children ...any) map[string]any {
	if children == nil {
		children = []any{}
	}
	return map[string]any{"type": "text", "children": children}
}

func uiTreeRow(gap int, wrap string, children ...any) map[string]any {
	props := map[string]any{"gap": gap}
	if wrap != "" {
		props["wrap"] = wrap
	}
	return map[string]any{"type": "row", "props": props, "children": children}
}

func uiTreeBold(children ...any) map[string]any {
	if children == nil {
		children = []any{}
	}
	return map[string]any{"type": "bold", "children": children}
}

func uiTreeItalic(children ...any) map[string]any {
	if children == nil {
		children = []any{}
	}
	return map[string]any{"type": "italic", "children": children}
}

func uiTreeUnderline(children ...any) map[string]any {
	if children == nil {
		children = []any{}
	}
	return map[string]any{"type": "underline", "children": children}
}

func uiTreeLink(href string, children ...any) map[string]any {
	if children == nil {
		children = []any{}
	}
	return map[string]any{"type": "link", "props": map[string]any{"href": href}, "children": children}
}

func uiTreePreparedRows(tree *UINode, width int, height int) []string {
	lines := tree.Prepare().RenderLines(width, height)
	rows := make([]string, len(lines))
	for index, line := range lines {
		rows[index] = strings.TrimRight(line.Text, " ")
	}
	return rows
}

// TestUITreePreparedMeasuresViewportLikeRust mirrors
// ui_tree::tests::measures_viewport_sizes_in_cells.
func TestUITreePreparedMeasuresViewportLikeRust(t *testing.T) {
	wrapped := MustUITree(mustJSON(t, uiTreeRow(1, "wrap", "ab\ncd", "\u754c\u754c")))
	if width, height := wrapped.Prepare().Measure(4, 8); width != 4 || height != 3 {
		t.Fatalf("wrapped measure = (%d,%d), want (4,3)", width, height)
	}
	wide := MustUITree(mustJSON(t, "a\u754c\nnext"))
	prepared := wide.Prepare()
	if width, height := prepared.Measure(3, 1); width != 2 || height != 1 {
		t.Fatalf("wide measure = (%d,%d), want (2,1)", width, height)
	}
	if rows := uiTreePreparedRows(wide, 3, 1); len(rows) != 1 || rows[0] != "a\u2026" {
		t.Fatalf("wide rows = %#v", rows)
	}
}

// TestUITreePreparedCollapsesWhitespaceLikeRust mirrors
// ui_tree::tests::whitespace_collapses_across_styled_leaves.
func TestUITreePreparedCollapsesWhitespaceLikeRust(t *testing.T) {
	tree := MustUITree(mustJSON(t, uiTreeText("  build\t", uiTreeBold("  ready  "), "\nnext")))
	rows := uiTreePreparedRows(tree, 20, 2)
	if len(rows) != 2 || rows[0] != "build ready" || rows[1] != "next" {
		t.Fatalf("rows = %#v", rows)
	}
}

// TestUITreePreparedSanitizesTerminalCommandsLikeRust mirrors
// ui_tree::tests::terminal_commands_are_sanitized_within_each_string.
func TestUITreePreparedSanitizesTerminalCommandsLikeRust(t *testing.T) {
	const url = "https://example.com/ready"
	tree := MustUITree(mustJSON(t, uiTreeText(
		"\u001b[31mre\rd\u001b[0m \u001b]8;;https://example.com\u0007label\u001b]8;;\u001b\\\u0007",
		" \u001b[31",
		uiTreeLink(url, "Ready"),
	)))
	lines := tree.Prepare().RenderLines(40, 1)
	if len(lines) != 1 || lines[0].Text != "red label Ready" {
		t.Fatalf("sanitized lines = %#v", lines)
	}
	if len(lines[0].Hyperlinks) != 1 {
		t.Fatalf("hyperlinks = %#v", lines[0].Hyperlinks)
	}
	link := lines[0].Hyperlinks[0]
	if link.Start != 10 || link.End != 15 || link.Destination != url {
		t.Fatalf("link = %#v", link)
	}
}

// TestUITreePreparedWrapsNonBreakingLabelLikeRust mirrors
// ui_tree::tests::wrapping_row_moves_a_hash_and_nonbreaking_pr_label_together.
func TestUITreePreparedWrapsNonBreakingLabelLikeRust(t *testing.T) {
	const href = "https://example.com/pull/123"
	label := "PR\u00a0#123"
	tree := MustUITree(mustJSON(t, uiTreeRow(1, "wrap",
		"gpt-5",
		uiTreeRow(1, "", "a1b2c3d", uiTreeLink(href, label)),
	)))
	cases := []struct {
		width  int
		plain  []string
		linked [2]int
	}{
		{21, []string{"gpt-5 a1b2c3d " + label}, [2]int{14, 21}},
		{20, []string{"gpt-5", "a1b2c3d " + label}, [2]int{8, 15}},
		{12, []string{"gpt-5", "a1b2c3d PR\u00a0\u2026"}, [2]int{8, 11}},
	}
	for _, tc := range cases {
		lines := tree.Prepare().RenderLines(tc.width, 4)
		rows := make([]string, len(lines))
		for index, line := range lines {
			rows[index] = line.Text
		}
		if strings.Join(rows, "|") != strings.Join(tc.plain, "|") {
			t.Fatalf("width %d rows = %#v, want %#v", tc.width, rows, tc.plain)
		}
		last := lines[len(lines)-1]
		if len(last.Hyperlinks) != 1 {
			t.Fatalf("width %d hyperlinks = %#v", tc.width, last.Hyperlinks)
		}
		if got := last.Hyperlinks[0]; got.Start != tc.linked[0] || got.End != tc.linked[1] || got.Destination != href {
			t.Fatalf("width %d link = %#v, want %v", tc.width, got, tc.linked)
		}
	}
}

// TestUITreePreparedWrappedLinkOffsetsLikeRust mirrors
// ui_tree::tests::wrapped_link_offsets_follow_each_row_and_stop_before_clipping.
func TestUITreePreparedWrappedLinkOffsetsLikeRust(t *testing.T) {
	const href = "https://example.com"
	tree := MustUITree(mustJSON(t, uiTreeRow(0, "",
		">",
		uiTreeLink(href, uiTreeRow(0, "wrap", "12345", "67")),
	)))
	cases := []struct {
		width  int
		height int
		rows   []string
		links  [][2]int
	}{
		{6, 2, []string{">12345", " 67"}, [][2]int{{1, 6}, {1, 3}}},
		{4, 2, []string{">12\u2026", " 67"}, [][2]int{{1, 3}, {1, 3}}},
		{6, 1, []string{">1234\u2026"}, [][2]int{{1, 5}}},
	}
	for _, tc := range cases {
		lines := tree.Prepare().RenderLines(tc.width, tc.height)
		if len(lines) != len(tc.rows) {
			t.Fatalf("width %d lines = %#v", tc.width, lines)
		}
		for index, line := range lines {
			if strings.TrimRight(line.Text, " ") != strings.TrimRight(tc.rows[index], " ") {
				t.Fatalf("width %d row %d = %q, want %q", tc.width, index, line.Text, tc.rows[index])
			}
			if len(line.Hyperlinks) != 1 {
				t.Fatalf("width %d row %d hyperlinks = %#v", tc.width, index, line.Hyperlinks)
			}
			if got := line.Hyperlinks[0]; got.Start != tc.links[index][0] || got.End != tc.links[index][1] || got.Destination != href {
				t.Fatalf("width %d row %d link = %#v", tc.width, index, got)
			}
		}
	}
}

// TestUITreePreparedRendersInheritedStylesLikeRust checks that bold, italic,
// underline and semantic colours reach RenderStyled after inheritance.
func TestUITreePreparedRendersInheritedStylesLikeRust(t *testing.T) {
	restore := SetStdoutColorLevelForTest(ColorTrue)
	defer restore()
	tree := MustUITree(mustJSON(t, uiTreeText(
		uiTreeBold("B"),
		uiTreeItalic("I"),
		uiTreeUnderline("U"),
		map[string]any{"type": "text", "props": map[string]any{"color": "red"}, "children": []any{"R"}},
	)))
	lines := tree.Prepare().RenderLines(10, 1)
	if len(lines) != 1 || lines[0].Text != "BIUR" {
		t.Fatalf("styled lines = %#v", lines)
	}
	rendered := lines[0].RenderStyled("", ColorTrue)
	for _, want := range []string{"\u001b[1mB", "\u001b[3mI", "\u001b[4mU", "\u001b[38;5;1mR"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered %q missing %q", rendered, want)
		}
	}
}

// TestUITreePreparedSplitsGraphemesLikeRust mirrors
// ui_tree::tests::split_graphemes_use_the_first_style_and_require_unanimous_links.
func TestUITreePreparedSplitsGraphemesLikeRust(t *testing.T) {
	const url = "https://example.com/a"
	tree := MustUITree(mustJSON(t, uiTreeText(
		uiTreeBold(uiTreeRow(0, "", "R"), "e"),
		uiTreeItalic(),
		"\u0301",
		uiTreeLink(url, "\U0001f469"),
		uiTreeLink(url, "\u200d\U0001f4bb"),
		uiTreeLink(url, "\U0001f469"),
		uiTreeLink("https://example.com/b", "\u200d\U0001f4bb"),
		uiTreeBold("\u0600"),
		" x",
	)))
	lines := tree.Prepare().RenderLines(20, 1)
	if len(lines) != 1 {
		t.Fatalf("lines = %#v", lines)
	}
	if want := "Re\u0301\U0001f469\u200d\U0001f4bb\U0001f469\u200d\U0001f4bb\u0600 x"; lines[0].Text != want {
		t.Fatalf("text = %q, want %q", lines[0].Text, want)
	}
	cells := lines[0].buildCells()
	if len(cells) < 6 {
		t.Fatalf("cells = %#v", cells)
	}
	if cells[0].text != "R" || !cells[0].style.Bold {
		t.Fatalf("first cell = %#v", cells[0])
	}
	if cells[1].text != "e\u0301" || !cells[1].style.Bold {
		t.Fatalf("joined cell = %#v", cells[1])
	}
	if cells[2].text != "\U0001f469\u200d\U0001f4bb" || cells[2].href != url || !cells[2].style.Underline {
		t.Fatalf("unanimous link cell = %#v", cells[2])
	}
	if cells[3].text != "\U0001f469\u200d\U0001f4bb" || cells[3].href != "" || !cells[3].style.Underline {
		t.Fatalf("conflicting link cell = %#v", cells[3])
	}
}

// TestUITreeThemeColorResolutionLikeRust pins the theme.rs contract: named
// colours are ANSI slots, literal colours need a truecolor terminal, status
// roles fall back to their accent slot and unknown tokens never decode.
func TestUITreeThemeColorResolutionLikeRust(t *testing.T) {
	if got := uiTreeTerminalColor("", UIColorRed, ColorTrue); got.Index == nil || *got.Index != 1 || got.RGB != nil {
		t.Fatalf("named colour = %#v", got)
	}
	if got := uiTreeTerminalColor("", "#112233", ColorUnknown); got.Index != nil || got.RGB != nil {
		t.Fatalf("literal colour at unknown level = %#v", got)
	}
	if got := uiTreeTerminalColor("", "#112233", ColorTrue); got.RGB == nil || got.RGB.R != 0x11 || got.RGB.G != 0x22 || got.RGB.B != 0x33 {
		t.Fatalf("literal colour = %#v", got)
	}
	if got := uiTreeTerminalColor("", "status-branch", ColorTrue); got.Index == nil || *got.Index != 5 {
		t.Fatalf("status fallback = %#v", got)
	}
	for _, raw := range []string{"", "status-unknown", "#12345", "#gggggg", "rgb"} {
		if _, err := ParseUIColor(raw); err == nil {
			t.Fatalf("ParseUIColor(%q) accepted an unsupported token", raw)
		}
	}
	for _, raw := range []string{"wrap-reverse", "WRAP"} {
		if _, err := ParseUIRowWrap(raw); err == nil {
			t.Fatalf("ParseUIRowWrap(%q) accepted an unsupported token", raw)
		}
	}
}
