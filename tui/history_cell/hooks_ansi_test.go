package historycell

import (
	"strings"
	"testing"
)

// Rust parity: hook_cell.rs
// `completed_hook_system_message_renders_ansi_styles` and the extended multiline
// coverage that adds a combining character and a trailing newline.
func TestHookSystemMessageRendersANSIStyles(t *testing.T) {
	cell := NewHookRun("postToolUse", "completed", "", []HookOutputEntry{
		{Kind: HookOutputWarning, Text: "\x1b[1;92mStyled output\nacross lines\x1b[0m plain"},
	})

	styled := cell.DisplayStyledLines(80)
	plain := cell.DisplayLines(80)
	if got, want := strings.Join(plain, "|"), "• PostToolUse hook (completed)|  warning: Styled output|           across lines plain"; got != want {
		t.Fatalf("plain display = %q, want %q", got, want)
	}
	if got, want := strings.Join(PlainLines(styled), "|"), strings.Join(plain, "|"); got != want {
		t.Fatalf("plain projection of styled lines = %q, want %q", got, want)
	}
	for _, line := range plain {
		if strings.ContainsRune(line, 0x1b) {
			t.Fatalf("raw escape leaked into display line %q", line)
		}
	}

	emphasis := CellStyle{Bold: true, Foreground: "12"}
	if len(styled) != 3 {
		t.Fatalf("styled line count = %d, want 3", len(styled))
	}
	if got := styled[1][1]; got != (StyledSpan{Text: "Styled output", Style: emphasis}) {
		t.Fatalf("styled[1][1] = %#v", got)
	}
	if got := styled[2]; len(got) != 3 ||
		got[1] != (StyledSpan{Text: "across lines ", Style: emphasis}) ||
		got[2] != (StyledSpan{Text: "plain"}) {
		t.Fatalf("styled[2] = %#v", got)
	}
	for _, line := range cell.RawLines() {
		if strings.ContainsRune(line, 0x1b) {
			t.Fatalf("raw escape leaked into raw line %q", line)
		}
	}
}

func TestHookSystemMessagePreservesBlankAndCombiningLines(t *testing.T) {
	cell := NewHookRun("userPromptSubmit", "completed", "", []HookOutputEntry{
		{Kind: HookOutputWarning, Text: "Heads up\n\u0301\nReview generated files\n"},
	})

	if got, want := strings.Join(cell.DisplayLines(80), "|"),
		"• UserPromptSubmit hook (completed)|  warning: Heads up|           \u0301|           Review generated files|"; got != want {
		t.Fatalf("display = %q, want %q", got, want)
	}
}
