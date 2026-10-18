package markdown

import (
	"reflect"
	"strings"
	"testing"

	"codex_go/utils"
)

func plainRenderedLines(rendered string) []string {
	lines := strings.Split(utils.StripANSI(rendered), "\n")
	for index, line := range lines {
		lines[index] = strings.TrimRight(line, " ")
	}
	for len(lines) > 0 && lines[0] == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// Rust #46899: completed lists separate every sibling when any item spans
// multiple rendered rows; per-list policy keeps nested single-line lists
// compact and quote blank lines inside the quote.
func TestUniformListSpacingMatchesRust(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   []string
	}{
		{
			name:   "wrapped ordered list",
			source: "1. One\n2. This item has enough words to wrap\n3. Three\n4. Four",
			want: []string{
				"1. One",
				"",
				"2. This item has enough",
				"words to wrap",
				"",
				"3. Three",
				"",
				"4. Four",
			},
		},
		{
			name:   "nested and quoted lists",
			source: "- Parent\n  - Short\n  - Small\n- Other\n\n> - Quote\n> - This quoted item has enough words to wrap\n> - End",
			want: []string{
				"• Parent",
				"    • Short",
				"    • Small",
				"",
				"• Other",
				"",
				"│",
				"│ • Quote",
				"│",
				"│ • This quoted item has",
				"│ enough words to wrap",
				"│",
				"│ • End",
			},
		},
		{
			name:   "code block item",
			source: "- One\n- ```text\n  code\n  ```\n- Three\n\n  ```text\n  after paragraph\n  ```",
			want: []string{
				"• One",
				"",
				"•",
				"code",
				"",
				"• Three",
				"after paragraph",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rendered, err := RenderUniformListsWithThemeCwd(tc.source, 24, "", "")
			if err != nil {
				t.Fatalf("RenderUniformListsWithThemeCwd() error = %v", err)
			}
			if got := plainRenderedLines(rendered); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("uniform list lines:\n got: %#v\nwant: %#v", got, tc.want)
			}
		})
	}
}

// Single-line lists stay compact, and width-dependent regroup is recomputed by
// each render.
func TestUniformListSpacingKeepsSingleLineListsCompact(t *testing.T) {
	source := "- https://example.com/a-very-long-path-with-many-segments\n- Short\n- End"
	narrow, err := RenderUniformListsWithThemeCwd(source, 24, "", "")
	if err != nil {
		t.Fatal(err)
	}
	wantNarrow := []string{
		"• https://example.com/a-very-long-path-with-many-segments",
		"• Short",
		"• End",
	}
	if got := plainRenderedLines(narrow); !reflect.DeepEqual(got, wantNarrow) {
		t.Fatalf("single-line list = %#v, want %#v", got, wantNarrow)
	}

	source = "1. One\n2. Short\n3. Three"
	compact, err := RenderWithThemeCwd(source, 80, "", "")
	if err != nil {
		t.Fatal(err)
	}
	uniform, err := RenderUniformListsWithThemeCwd(source, 80, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if compact != uniform {
		t.Fatalf("wide single-line list changed spacing:\ncompact=%q\nuniform=%q", compact, uniform)
	}
}

// Uniform spacing is a line insertion pass; OSC 8 link metadata on retained
// rows must survive untouched.
func TestUniformListSpacingPreservesRenderedLinks(t *testing.T) {
	source := "- [One](https://example.com/one)\n- [Two](https://example.com/two)\n- Three"
	rendered, err := RenderUniformListsWithThemeCwd(source, 100, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(rendered, "\x1b]8;;https://example.com/one"); got != 1 {
		t.Fatalf("first link occurrences = %d, want 1", got)
	}
	if got := strings.Count(rendered, "\x1b]8;;https://example.com/two"); got != 1 {
		t.Fatalf("second link occurrences = %d, want 1", got)
	}
}

func TestUniformListSpacingIgnoresTableRowsStartingWithBullet(t *testing.T) {
	if info := parseRenderedListLine("│ • item │"); info.kind != renderedListNone {
		t.Fatalf("table row parsed as list item: %#v", info)
	}
}
