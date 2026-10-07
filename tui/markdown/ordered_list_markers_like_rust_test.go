package markdown

import (
	"strings"
	"testing"

	"codex_go/utils"
)

// Rust #48800 (upstream abc8f0c9a1): "Use the terminal palette for ordered
// Markdown list markers". `MarkdownStyles::ordered_list_marker` becomes
// `Style::new().light_blue()` (the bright-blue ANSI palette entry, xterm index
// 12) instead of the resolved accent colour, so the ordinal marker follows the
// terminal palette while the item text keeps the default colour.
//
// Ported Rust tests: `list_ordered`, `list_ordered_custom_start`,
// `nested_unordered_in_ordered`, `nested_ordered_in_unordered`,
// `blockquote_with_ordered_list`,
// `ordered_list_markers_use_terminal_palette_snapshot`
// (markdown_render_tests.rs) and
// `e2e_stream_nested_mixed_lists_ordered_marker_uses_terminal_palette`
// (markdown_stream.rs). The Rust snapshots also cover the streaming renderer;
// in Go every transcript path (live cells, the streaming controller at
// tui/streaming/controller.go:459, the agent overview) renders through
// markdown.RenderWithThemeCwd, so one implementation covers all of them.

const (
	rustLightBlueSGR      = "\x1b[38;5;12m" // ratatui Color::LightBlue, palette index 12
	rustLightBlueResetSGR = "\x1b[39m"      // ratatui Color::Reset foreground
)

func renderedLines(t *testing.T, source string) []string {
	t.Helper()
	rendered, err := Render(source, 80)
	if err != nil {
		t.Fatalf("Render(%q) error: %v", source, err)
	}
	lines := strings.Split(rendered, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(utils.StripANSI(line)) == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}

// TestOrderedListMarkersUseTerminalPaletteLikeRust mirrors Rust `list_ordered`,
// `list_ordered_custom_start` and
// `ordered_list_markers_use_terminal_palette_snapshot`: "1. "/"2. " and a custom
// start ("3. "/"4. ") are painted with the palette LightBlue, and the item text
// after the marker stays in the default colour.
func TestOrderedListMarkersUseTerminalPaletteLikeRust(t *testing.T) {
	if orderedListMarkerSGR != rustLightBlueSGR {
		t.Fatalf("ordered marker SGR = %q, want LightBlue %q", orderedListMarkerSGR, rustLightBlueSGR)
	}

	lines := renderedLines(t, "1. List item 1\n2. List item 2\n")
	want := []string{"1. List item 1", "2. List item 2"}
	if len(lines) != len(want) {
		t.Fatalf("rendered %d lines, want %d: %q", len(lines), len(want), lines)
	}
	for index, line := range lines {
		if plain := utils.StripANSI(line); plain != want[index]+strings.Repeat(" ", 80-len(want[index])) {
			t.Fatalf("line %d plain = %q, want %q padded", index, plain, want[index])
		}
		marker := want[index][:3] // "1. "
		if !strings.HasPrefix(line, rustLightBlueSGR+marker+rustLightBlueResetSGR) {
			t.Fatalf("line %d = %q, want the %q marker in LightBlue", index, line, marker)
		}
		body := "List item " + want[index][:1]
		if strings.Contains(line, rustLightBlueSGR+body) {
			t.Fatalf("line %d item text is painted with the marker colour: %q", index, line)
		}
	}

	custom := renderedLines(t, "3. First\n4. Second\n")
	if len(custom) != 2 {
		t.Fatalf("custom-start render = %q", custom)
	}
	for index, marker := range []string{"3. ", "4. "} {
		if !strings.HasPrefix(custom[index], rustLightBlueSGR+marker+rustLightBlueResetSGR) {
			t.Fatalf("custom-start line %d = %q, want %q in LightBlue", index, custom[index], marker)
		}
	}
}

// TestNestedListMarkersUseTerminalPaletteLikeRust mirrors Rust
// `nested_unordered_in_ordered` and `nested_ordered_in_unordered`: the ordered
// marker span carries its own indentation and is LightBlue, while bullets stay
// unstyled.
func TestNestedListMarkersUseTerminalPaletteLikeRust(t *testing.T) {
	lines := renderedLines(t, "1. Outer\n    - Inner A\n    - Inner B\n2. Next\n")
	if len(lines) != 4 {
		t.Fatalf("nested render = %q", lines)
	}
	if !strings.HasPrefix(lines[0], rustLightBlueSGR+"1. "+rustLightBlueResetSGR+"Outer") {
		t.Fatalf("outer marker = %q", lines[0])
	}
	for _, index := range []int{1, 2} {
		if strings.Contains(lines[index], rustLightBlueSGR) {
			t.Fatalf("bullet line %d is painted with the marker colour: %q", index, lines[index])
		}
		if !strings.HasPrefix(utils.StripANSI(lines[index]), "    \u2022 Inner ") {
			t.Fatalf("bullet line %d = %q", index, utils.StripANSI(lines[index]))
		}
	}
	if !strings.HasPrefix(lines[3], rustLightBlueSGR+"2. "+rustLightBlueResetSGR+"Next") {
		t.Fatalf("closing marker = %q", lines[3])
	}

	nested := renderedLines(t, "- Outer\n    1. One\n    2. Two\n\n- Last\n")
	if len(nested) != 4 {
		t.Fatalf("nested render = %q", nested)
	}
	if strings.Contains(nested[0], rustLightBlueSGR) || strings.Contains(nested[3], rustLightBlueSGR) {
		t.Fatalf("bullet lines painted with the marker colour: %q", nested)
	}
	for index, marker := range []string{"    1. ", "    2. "} {
		if !strings.HasPrefix(nested[index+1], rustLightBlueSGR+marker+rustLightBlueResetSGR) {
			t.Fatalf("nested ordered line %d = %q, want %q in LightBlue", index, nested[index+1], marker)
		}
	}
}

// TestBlockquoteOrderedListMarkersUseTerminalPaletteLikeRust mirrors Rust
// `blockquote_with_ordered_list`: the blockquote indent stays unstyled and only
// the ordinal marker takes the palette colour. (The Go TUI paints blockquotes
// with a box-drawing bar instead of Rust's "> " prefix.)
func TestBlockquoteOrderedListMarkersUseTerminalPaletteLikeRust(t *testing.T) {
	lines := renderedLines(t, "> 1. first\n> 2. second\n")
	markers := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.Contains(line, rustLightBlueSGR) {
			markers = append(markers, line)
		}
	}
	if len(markers) != 2 {
		t.Fatalf("blockquote render = %q", lines)
	}
	for index, marker := range []string{"1. ", "2. "} {
		line := markers[index]
		if !strings.HasPrefix(line, blockQuoteIndentToken+rustLightBlueSGR+marker+rustLightBlueResetSGR) {
			t.Fatalf("blockquote line %d = %q, want the indent plain and %q in LightBlue", index, line, marker)
		}
		if strings.HasPrefix(line, rustLightBlueSGR) {
			t.Fatalf("blockquote indent painted with the marker colour: %q", line)
		}
	}
}

// TestOrderedListMarkersLeaveCodeLikeRust mirrors the scope of Rust's
// `ordered_list_marker`: the colour comes from the parsed list item, so a "1. "
// line inside a fenced code block is never repainted.
func TestOrderedListMarkersLeaveCodeLikeRust(t *testing.T) {
	lines := renderedLines(t, "```\n1. not a list\n2. still code\n```\n")
	if len(lines) == 0 {
		t.Fatalf("code render was empty")
	}
	for _, line := range lines {
		if strings.Contains(line, rustLightBlueSGR) {
			t.Fatalf("code line repainted with the ordered marker colour: %q", line)
		}
	}
	if plain := utils.StripANSI(lines[0]); !strings.Contains(plain, "1. not a list") {
		t.Fatalf("code body = %q", plain)
	}
}

// TestOrderedListMarkerSnapshotSpansLikeRust mirrors
// `ordered_list_markers_use_terminal_palette_snapshot`: the marker span is
// styled on its own while the link and inline-code spans keep their own
// styling.
func TestOrderedListMarkerSnapshotSpansLikeRust(t *testing.T) {
	lines := renderedLines(t, "1. plain [plain](https://example.com) `code`\n")
	if len(lines) != 1 {
		t.Fatalf("render = %q", lines)
	}
	line := lines[0]
	if !strings.HasPrefix(line, rustLightBlueSGR+"1. "+rustLightBlueResetSGR) {
		t.Fatalf("snapshot line = %q", line)
	}
	if !strings.Contains(line, "\x1b[4m") {
		t.Fatalf("link underline style lost: %q", line)
	}
	if plain := utils.StripANSI(line); !strings.HasPrefix(plain, "1. plain plain https://example.com code") {
		t.Fatalf("snapshot plain text = %q", plain)
	}
	if count := strings.Count(line, rustLightBlueSGR); count != 1 {
		t.Fatalf("marker colour applied %d times in %q", count, line)
	}
}
