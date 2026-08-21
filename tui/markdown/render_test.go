package markdown

import (
	"strings"
	"testing"

	"codex_go/utils"
)

func TestRenderWithThemeHighlightsCodeAndChangesTheme(t *testing.T) {
	source := "```go\npackage main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n```"
	dracula, err := RenderWithTheme(source, 80, "dracula")
	if err != nil {
		t.Fatal(err)
	}
	github, err := RenderWithTheme(source, 80, "github")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dracula, "\x1b[") || !strings.Contains(github, "\x1b[") {
		t.Fatalf("expected ANSI highlighted code dracula=%q github=%q", dracula, github)
	}
	if dracula == github {
		t.Fatalf("theme render did not change:\n%s", dracula)
	}
}

func TestRenderWithThemePreservesCodeLinesAtNarrowWidth(t *testing.T) {
	source := "```c\nvoid swap(int *a, int *b) {\n    int temp = *a;\n}\n```"
	rendered, err := RenderWithTheme(source, 12, "dracula")
	if err != nil {
		t.Fatal(err)
	}
	clean := utils.StripANSI(rendered)
	if !containsExactLine(clean, "void swap(int *a, int *b) {") || !containsExactLine(clean, "    int temp = *a;") {
		t.Fatalf("fenced code source lines were wrapped:\n%s", clean)
	}
	if strings.Contains(clean, codeBlockStartMarker) || strings.Contains(clean, codeBlockEndMarker) {
		t.Fatalf("internal code markers leaked into output:\n%s", clean)
	}
}

func TestRenderWithThemeRestoresMultipleCodeBlocks(t *testing.T) {
	source := "first\n\n```go\nfunc main() {}\n```\n\nsecond\n\n```unknown-language\na_very_long_identifier_without_spaces\n```"
	rendered, err := RenderWithTheme(source, 16, "github")
	if err != nil {
		t.Fatal(err)
	}
	clean := utils.StripANSI(rendered)
	for _, want := range []string{"func main() {}", "a_very_long_identifier_without_spaces"} {
		if !containsExactLine(clean, want) {
			t.Fatalf("code block line %q was not restored:\n%s", want, clean)
		}
	}
}

func TestRenderWithThemeStylesInlineMarkdown(t *testing.T) {
	source := "# Title\n\n**bold** and *italic* and `code`\n\n## Subheading"
	rendered, err := RenderWithTheme(source, 60, "")
	if err != nil {
		t.Fatal(err)
	}
	clean := utils.StripANSI(rendered)
	for _, literal := range []string{"# Title", "**bold**", "*italic*", "## Subheading"} {
		if strings.Contains(clean, literal) {
			t.Fatalf("markdown marker %q leaked into styled output:\n%s", literal, clean)
		}
	}
	for _, want := range []string{"Title", "bold", "italic", "Subheading"} {
		if !strings.Contains(clean, want) {
			t.Fatalf("styled content missing %q:\n%s", want, clean)
		}
	}
	if !strings.Contains(rendered, "\x1b[") {
		t.Fatalf("expected ANSI styling for inline markdown, got no escapes:\n%s", rendered)
	}
}

func TestRenderWithThemeAddsOSC8Hyperlinks(t *testing.T) {
	source := "Visit https://example.com/path for details."
	rendered, err := RenderWithTheme(source, 60, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, "\x1b]8;;https://example.com/path\x07") {
		t.Fatalf("expected OSC-8 hyperlink in rendered output:\n%s", rendered)
	}
	// The visible text still contains the URL.
	clean := utils.StripANSI(rendered)
	if !strings.Contains(clean, "https://example.com/path") {
		t.Fatalf("URL text missing after annotation:\n%s", clean)
	}
}

func TestRenderWithThemeMarksLinkLabel(t *testing.T) {
	source := "See [docs](https://example.com/docs) for details."
	rendered, err := RenderWithTheme(source, 60, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, "\x1b]8;;https://example.com/docs\x07docs\x1b]8;;\x07") {
		t.Fatalf("web link label not wrapped in an OSC-8 hyperlink:\n%q", rendered)
	}
}

func TestRenderWithThemeRendersBoxDrawingTable(t *testing.T) {
	source := "| Name | Value |\n|---|---|\n| alpha | 1 |\n| beta | 22 |"
	rendered, err := RenderWithTheme(source, 60, "")
	if err != nil {
		t.Fatal(err)
	}
	clean := utils.StripANSI(rendered)
	if strings.Contains(clean, "|") {
		t.Fatalf("table still uses ASCII pipe separator:\n%s", clean)
	}
	for _, want := range []string{"\u2501", "\u2500"} {
		if !strings.Contains(clean, want) {
			t.Fatalf("box-drawing table missing %q:\n%s", want, clean)
		}
	}
	for _, want := range []string{"Name", "Value", "alpha", "1", "beta", "22"} {
		if !strings.Contains(clean, want) {
			t.Fatalf("table content missing %q:\n%s", want, clean)
		}
	}
}

func containsExactLine(text string, want string) bool {
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if line == want {
			return true
		}
	}
	return false
}
