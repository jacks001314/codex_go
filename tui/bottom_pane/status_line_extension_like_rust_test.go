package bottompane

import (
	"strings"
	"testing"

	"codex_go/tui"
)

func extensionStatusLine(t *testing.T, tree string) StatusLine {
	t.Helper()
	line, err := StatusLineFromUITreeJSON([]byte(tree))
	if err != nil {
		t.Fatalf("StatusLineFromUITreeJSON() error = %v", err)
	}
	return line
}

// TestStatusLineExtensionClipsLinksToFooterLayoutLikeRust mirrors Rust
// status_surface_tests::extension_status_line_clips_links_to_footer_layout:
// the UI-tree renderer keeps link cells at wide widths and clips both the text
// and the link range at narrow widths, never linking the overflow ellipsis.
func TestStatusLineExtensionClipsLinksToFooterLayoutLikeRust(t *testing.T) {
	line := extensionStatusLine(t, `{
		"type": "row", "props": {"gap": 1}, "children": [
			"plain",
			{ "type": "row", "props": {"gap": 1}, "children": [
				"\u00b7",
				{ "type": "link", "props": { "href": "https://example.com/pr/42" }, "children": [
					"PR #42"
				] }
			] }
		]
	}`)
	options := StatusLineRenderOptions{ThemeID: "dark", ColorLevel: tui.ColorTrue}
	for _, testCase := range []struct {
		width int
		want  string
	}{
		{12, "PR"},
		{40, "PR #42"},
	} {
		rows := line.RenderRows(testCase.width, 1, StatusLineFocused, options)
		if len(rows) != 1 {
			t.Fatalf("width %d rows = %d, want 1", testCase.width, len(rows))
		}
		var linkedText strings.Builder
		for _, link := range tui.OSC8LinksInLine(rows[0].Styled) {
			plain := []rune(rows[0].Plain)
			start := min(link.Start, len(plain))
			end := min(link.End, len(plain))
			linkedText.WriteString(string(plain[start:end]))
			if strings.Contains(string(plain[start:end]), "\u2026") {
				t.Fatalf("width %d linked ellipsis: %q", testCase.width, rows[0].Styled)
			}
		}
		if !strings.HasPrefix(linkedText.String(), testCase.want) {
			t.Fatalf("width %d linked text = %q, want %q (row %q)", testCase.width, linkedText.String(), testCase.want, rows[0].Plain)
		}
		if testCase.width == 12 {
			if !strings.HasSuffix(rows[0].Plain, "\u2026") {
				t.Fatalf("narrow row lacks ellipsis: %q", rows[0].Plain)
			}
			if !strings.Contains(rows[0].Plain, "plain") {
				t.Fatalf("narrow row dropped leading text: %q", rows[0].Plain)
			}
		}
	}
}

// TestStatusLineExtensionUsesNaturalHeightLikeRust mirrors #60031's natural
// width measurement: wrapping rows are counted at the supplied width instead of
// rendering the tree at its maximum width.
func TestStatusLineExtensionUsesNaturalHeightLikeRust(t *testing.T) {
	line := extensionStatusLine(t, `{"type":"row","props":{"wrap":"wrap","gap":1},"children":[
		"Ready", {"type":"row","props":{"gap":1},"children":["PR",
		{"type":"link","props":{"href":"https://example.com/pr/12345"},"children":["12345"]}]}]}`)
	if got := line.Height(40, 4); got != 1 {
		t.Fatalf("wide height = %d, want 1", got)
	}
	if got := line.Height(10, 4); got != 2 {
		t.Fatalf("narrow height = %d, want 2", got)
	}
	if got := line.Height(10, 1); got != 1 {
		t.Fatalf("viewport-clamped height = %d, want 1", got)
	}
	rows := line.RenderRows(10, 4, StatusLineFocused, StatusLineRenderOptions{ThemeID: "dark", ColorLevel: tui.ColorTrue})
	if len(rows) != 2 {
		t.Fatalf("narrow rows = %d, want 2", len(rows))
	}
}

// TestStatusLineExtensionClippedViewportKeepsLinkLikeRust mirrors Rust
// status_line_tests::prepared_tree_paints_only_the_footer_viewport: the last
// visible row is clipped with an unlinked ellipsis while earlier rows keep
// their hyperlink cells.
func TestStatusLineExtensionClippedViewportKeepsLinkLikeRust(t *testing.T) {
	url := "https://example.com/build"
	line := extensionStatusLine(t, `{"type":"link","props":{"href":"`+url+`"},"children":["ok\nlarge build\na\nb\nc\nd"]}`)
	if got := line.Height(6, 4); got != 4 {
		t.Fatalf("height = %d, want 4", got)
	}
	rows := line.RenderRows(6, 4, StatusLineFocused, StatusLineRenderOptions{ThemeID: "dark", ColorLevel: tui.ColorTrue})
	if len(rows) != 4 {
		t.Fatalf("rows = %d, want 4", len(rows))
	}
	if got := strings.TrimSpace(rows[3].Plain); got != "a\u2026" {
		t.Fatalf("last row = %q, want a…", got)
	}
	links := tui.OSC8LinksInLine(rows[3].Styled)
	if len(links) != 1 || links[0].Destination != url || links[0].End > 1 {
		t.Fatalf("last-row links = %#v", links)
	}
	if len(tui.OSC8LinksInLine(rows[2].Styled)) != 1 {
		t.Fatalf("row two lost its link: %q", rows[2].Styled)
	}
}

// TestStatusLineExtensionUnfocusedRowsAreDimLikeRust covers Rust
// status_line::render's plain style path: an unfocused surface keeps the layout
// but suppresses theme colours and hyperlinks.
func TestStatusLineExtensionUnfocusedRowsAreDimLikeRust(t *testing.T) {
	line := extensionStatusLine(t, `"one\ntwo\nthree\nfour"`)
	rows := line.RenderRows(40, 4, StatusLineUnfocused, StatusLineRenderOptions{ThemeID: "dark", ColorLevel: tui.ColorTrue})
	if len(rows) != 4 {
		t.Fatalf("rows = %d, want 4", len(rows))
	}
	for index, row := range rows {
		if !strings.Contains(row.Styled, statusLineDimSGR) {
			t.Fatalf("row %d is not dim: %q", index, row.Styled)
		}
		if len(tui.OSC8LinksInLine(row.Styled)) != 0 {
			t.Fatalf("row %d kept a hyperlink: %q", index, row.Styled)
		}
	}
}

// TestStatusLineNativeHeightMatchesPaintLikeRust keeps the native status line
// on the same measure/paint path as extension content.
func TestStatusLineNativeHeightMatchesPaintLikeRust(t *testing.T) {
	line, ok := StatusLineFromSegments([]StatusLineSegment{
		{Item: StatusLineModelName, Text: "gpt-5"},
		{Item: StatusLineCurrentDir, Text: "~/a/very/long/project/path"},
		{Item: StatusLineGitBranch, Text: "feature/long-branch"},
	}, true)
	if !ok {
		t.Fatal("StatusLineFromSegments() returned no line")
	}
	height := line.Height(12, 4)
	if height < 2 {
		t.Fatalf("height = %d, want at least 2", height)
	}
	rows := line.RenderRows(12, 4, StatusLineFocused, StatusLineRenderOptions{AccentColorSGR: func(StatusLineAccent) string { return "" }})
	if len(rows) != height {
		t.Fatalf("rows = %d, height = %d", len(rows), height)
	}
	clipped := line.RenderRows(12, 1, StatusLineFocused, StatusLineRenderOptions{})
	if len(clipped) != 1 || !strings.HasSuffix(clipped[0].Plain, "\u2026") {
		t.Fatalf("clipped rows = %#v", clipped)
	}
	dimmed := line.RenderRows(80, 1, StatusLineUnfocused, StatusLineRenderOptions{})
	if len(dimmed) != 1 || !strings.Contains(dimmed[0].Styled, statusLineDimSGR) || strings.Contains(dimmed[0].Styled, "\x1b[36m") {
		t.Fatalf("unfocused row = %q", dimmed[0].Styled)
	}
}

// TestStatusLineExtensionCachesLayoutLikeRust covers Rust CachedLayout: height
// and render share one prepared-tree layout for the same viewport, and a bounds
// change invalidates it so a resize reflows instead of clipping stale rows.
func TestStatusLineExtensionCachesLayoutLikeRust(t *testing.T) {
	line := extensionStatusLine(t, `"one\ntwo\nthree"`)
	if got := line.Height(20, 3); got != 3 {
		t.Fatalf("height = %d, want 3", got)
	}
	if line.layoutCache == nil || !line.layoutCache.valid || line.layoutCache.width != 20 || line.layoutCache.height != 3 {
		t.Fatalf("layout cache = %#v, want a valid 20x3 entry", line.layoutCache)
	}
	rows := line.RenderRows(20, 3, StatusLineFocused, StatusLineRenderOptions{ThemeID: "dark", ColorLevel: tui.ColorTrue})
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	if line.layoutCache.width != 20 || line.layoutCache.height != 3 {
		t.Fatalf("render changed the cached bounds: %#v", line.layoutCache)
	}
	if got := line.Height(10, 3); got != 3 {
		t.Fatalf("narrow height = %d, want 3", got)
	}
	if line.layoutCache.width != 10 {
		t.Fatalf("width change kept the stale layout: %#v", line.layoutCache)
	}
}
