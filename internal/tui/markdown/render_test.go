package markdown

import (
	"strings"
	"testing"

	"codex_go/internal/utils"
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

func containsExactLine(text string, want string) bool {
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if line == want {
			return true
		}
	}
	return false
}
