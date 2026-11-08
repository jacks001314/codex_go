package historycell

import (
	"strings"
	"testing"

	"codex_go/tui"
	"codex_go/utils"
)

func userPromptMarkdownTestColors(t *testing.T) func() {
	t.Helper()
	restoreColors := tui.SetDefaultTerminalColorsForTest(&tui.DefaultColors{
		FG: tui.RGBColor{R: 229, G: 229, B: 229},
		BG: tui.RGBColor{},
	})
	restoreLevel := tui.SetStdoutColorLevelForTest(tui.ColorANSI256)
	return func() {
		restoreLevel()
		restoreColors()
	}
}

// Mirrors Rust #59983: a typed prompt is Markdown, so its emphasis, inline code
// and list markers render instead of showing the Markdown source.
func TestTypedUserPromptRendersMarkdownLikeRust(t *testing.T) {
	defer userPromptMarkdownTestColors(t)()
	cell := NewUserPrompt("**bold** and `code`\n\n- item one", nil, nil, nil)
	joined := strings.Join(cell.DisplayLines(60), "\n")
	if strings.Contains(joined, "**bold**") || strings.Contains(joined, "`code`") {
		t.Fatalf("typed prompt kept its Markdown source:\n%q", joined)
	}
	if !strings.Contains(joined, "\x1b[1mbold") {
		t.Fatalf("typed prompt lost bold styling:\n%q", joined)
	}
	if !strings.Contains(joined, "\x1b[36mcode") {
		t.Fatalf("typed prompt lost inline-code styling:\n%q", joined)
	}
	if plain := utils.StripANSI(joined); !strings.Contains(plain, "\u2022 item one") {
		t.Fatalf("typed prompt lost its list marker:\n%q", plain)
	}
}

// Mirrors Rust #59983: the Markdown column keeps the prompt gutter on the first
// row, indents continuation rows the same way the plain path does, and keeps
// link/mention styling instead of flattening it to a bare string.
func TestUserPromptMarkdownKeepsGutterAndLinkColumnsLikeRust(t *testing.T) {
	defer userPromptMarkdownTestColors(t)()
	cell := NewUserPrompt("see [guide](https://example.com/docs) and $my-skill\n\nsecond paragraph", nil, nil, nil)
	lines := cell.DisplayLines(60)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "\x1b]8;;https://example.com/docs\x07") {
		t.Fatalf("typed prompt lost its link column:\n%q", joined)
	}
	if !strings.Contains(joined, "\x1b[36m$my-skill") {
		t.Fatalf("typed prompt lost its mention column:\n%q", joined)
	}
	var first string
	var continuation string
	for _, line := range lines {
		plain := utils.StripANSI(line)
		if strings.TrimSpace(plain) == "" {
			continue
		}
		if first == "" {
			first = plain
			continue
		}
		if strings.Contains(plain, "second paragraph") {
			continuation = plain
		}
	}
	if !strings.HasPrefix(first, "\u203a ") {
		t.Fatalf("first Markdown row = %q, want the prompt gutter", first)
	}
	if continuation == "" || !strings.HasPrefix(continuation, "  ") {
		t.Fatalf("continuation row = %q, want the two-column indent", continuation)
	}
}
