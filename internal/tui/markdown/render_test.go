package markdown

import (
	"strings"
	"testing"
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
