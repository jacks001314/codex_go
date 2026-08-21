package markdown

import (
	"testing"

	"codex_go/utils"
)

func TestUnwrapMarkdownFencesUnwrapsTableFence(t *testing.T) {
	source := "before\n```markdown\n| A | B |\n|---|---|\n| 1 | 2 |\n```\nafter"
	got := UnwrapMarkdownFences(source)
	clean := utils.StripANSI(got)
	if contains(clean, "```") {
		t.Fatalf("markdown fence around a table was not unwrapped:\n%s", clean)
	}
	for _, want := range []string{"| A | B |", "| 1 | 2 |", "before", "after"} {
		if !contains(clean, want) {
			t.Fatalf("missing %q after unwrap:\n%s", want, clean)
		}
	}
}

func TestUnwrapMarkdownFencesPreservesCodeFence(t *testing.T) {
	source := "```go\nfunc main() {}\n```\nafter"
	got := UnwrapMarkdownFences(source)
	clean := utils.StripANSI(got)
	if !contains(clean, "```go") || !contains(clean, "func main() {}") {
		t.Fatalf("non-markdown fence was altered:\n%s", clean)
	}
}

func TestRenderWithThemeUnwrapsTableFence(t *testing.T) {
	source := "```markdown\n| A | B |\n|---|---|\n| 1 | 2 |\n```"
	rendered, err := RenderWithThemeCwd(source, 60, "", "")
	if err != nil {
		t.Fatal(err)
	}
	clean := utils.StripANSI(rendered)
	if contains(clean, "```") {
		t.Fatalf("fence markers leaked into rendered table:\n%s", clean)
	}
	if !contains(clean, "\u2501") || !contains(clean, "A") {
		t.Fatalf("table inside markdown fence was not rendered as a table:\n%s", clean)
	}
}

func contains(text string, sub string) bool {
	for i := 0; i+len(sub) <= len(text); i++ {
		if text[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
