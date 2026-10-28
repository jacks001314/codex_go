package markdown

import (
	"strings"
	"testing"

	"codex_go/utils"
)

func renderedPlainLines(t *testing.T, source string, width int) []string {
	t.Helper()
	rendered, err := RenderUniformListsWithThemeCwd(source, width, "", "")
	if err != nil {
		t.Fatalf("RenderUniformListsWithThemeCwd() error = %v", err)
	}
	lines := strings.Split(rendered, "\n")
	for index := range lines {
		lines[index] = strings.TrimRight(utils.StripANSI(lines[index]), " ")
	}
	return lines
}

func lineIndexContaining(t *testing.T, lines []string, want string) int {
	t.Helper()
	for index, line := range lines {
		if strings.Contains(line, want) {
			return index
		}
	}
	t.Fatalf("rendered output has no line containing %q:\n%s", want, strings.Join(lines, "\n"))
	return -1
}

// TestHeadingMarkersLikeRust covers Rust's markdown_render::start_heading,
// which paints the ATX markers as part of the heading instead of hiding them
// behind styling.
func TestHeadingMarkersLikeRust(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{name: "h1", source: "# One\n", want: "# One"},
		{name: "h2", source: "## Two\n", want: "## Two"},
		{name: "h3", source: "### Three\n", want: "### Three"},
		{name: "setext h1", source: "Title\n=====\n", want: "# Title"},
		{name: "setext h2", source: "Heading\n---\n", want: "## Heading"},
		{name: "inline markup", source: "## **Bold** heading\n", want: "## Bold heading"},
		{name: "blockquote", source: "> ## Quoted\n", want: "## Quoted"},
		{name: "list item", source: "- ## Item heading\n", want: "## Item heading"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lines := renderedPlainLines(t, test.source, 60)
			index := lineIndexContaining(t, lines, test.want)
			if got := strings.TrimSpace(lines[index]); !strings.Contains(got, test.want) {
				t.Fatalf("heading line = %q, want %q", got, test.want)
			}
			// The marker must survive exactly once; a duplicated marker means
			// the source was rewritten more than once.
			if count := strings.Count(lines[index], test.want); count != 1 {
				t.Fatalf("heading line = %q, want %q once", lines[index], test.want)
			}
		})
	}
}

func TestHeadingMarkersSkipCodeBlocksLikeRust(t *testing.T) {
	lines := renderedPlainLines(t, "```text\n## not a heading\n```\n", 60)
	index := lineIndexContaining(t, lines, "## not a heading")
	if strings.Count(lines[index], "## not a heading") != 1 {
		t.Fatalf("code block line = %q, want the source preserved once", lines[index])
	}
}

// TestBlockSeparatorsLikeRust covers two block-boundary differences with
// glamour: an extra blank row after a nested list, and a missing blank row
// between a fenced code block and a table. Rust renders one blank row in both
// places (markdown_render::start_list/start_table).
func TestBlockSeparatorsLikeRust(t *testing.T) {
	t.Run("sibling lists", func(t *testing.T) {
		lines := renderedPlainLines(t, "- first item\n- second item\n  - nested item\n\n1. one\n2. two\n", 60)
		nested := lineIndexContaining(t, lines, "nested item")
		ordered := lineIndexContaining(t, lines, "1. one")
		if ordered-nested != 2 {
			t.Fatalf("nested list to ordered list spacing = %d rows, want one blank row:\n%s", ordered-nested, strings.Join(lines, "\n"))
		}
	})

	t.Run("code block to table", func(t *testing.T) {
		lines := renderedPlainLines(t, "```go\nfmt.Println(\"hi\")\n```\n\n| A | B |\n|---|---|\n| 1 | 2 |\n", 60)
		code := lineIndexContaining(t, lines, `fmt.Println("hi")`)
		table := lineIndexContaining(t, lines, " A      B")
		if table-code != 2 {
			t.Fatalf("code block to table spacing = %d rows, want one blank row:\n%s", table-code, strings.Join(lines, "\n"))
		}
	})
}
