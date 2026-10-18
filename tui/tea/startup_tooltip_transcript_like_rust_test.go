package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	"codex_go/utils"
)

// TestStartupTooltipLinesWrapLikeRust mirrors #47954: the startup tip belongs
// in the transcript beneath the session header and wraps on a narrow terminal.
func TestStartupTooltipLinesWrapLikeRust(t *testing.T) {
	template := "Use /mcp to list configured MCP tools, then open the MCP menu to inspect each server."
	lines := renderStartupTooltipLines(template, 32, nil, "", t.TempDir())
	if len(lines) < 2 {
		t.Fatalf("lines = %#v, want a wrapped transcript tip", lines)
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "  ") {
			t.Fatalf("line = %q, want the transcript two-column indent", line)
		}
		if width := codextui.DisplayWidth(utils.StripANSI(line)); width > 32 {
			t.Fatalf("line width = %d, want <= 32: %q", width, line)
		}
	}
}

// TestStartupTooltipLinesKeepLinksLikeRust checks the compact transcript tip
// keeps styled links instead of exposing Markdown source.
func TestStartupTooltipLinesKeepLinksLikeRust(t *testing.T) {
	const destination = "https://community.openai.com/c/codex/37"
	template := "Visit the [Codex community forum](https://community.openai.com/c/codex/37) to ask questions."
	lines := renderStartupTooltipLines(template, 85, nil, "", t.TempDir())
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "\x1b]8;;"+destination+"\x07") {
		t.Fatalf("tip lost its OSC 8 link:\n%q", joined)
	}
	if strings.Contains(joined, "](") {
		t.Fatalf("tip leaked Markdown link syntax:\n%q", joined)
	}
}
