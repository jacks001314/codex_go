package historycell

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
)

// Rust parity: codex-rs/tui/src/transcript_view_tests.rs
// `user_prompt_has_a_background_without_a_terminal_palette` (#57554). A
// terminal that never answers the default-color probe keeps the submitted
// prompt shaded with the ANSI DarkGray background and keeps the default text
// foreground for elements instead of painting an accent.
func TestUserPromptKeepsShadingWithoutTerminalPaletteLikeRust(t *testing.T) {
	restoreColors := codextui.SetDefaultTerminalColorsForTest(nil)
	defer restoreColors()
	restoreLevel := codextui.SetStdoutColorLevelForTest(codextui.ColorANSI256)
	defer restoreLevel()

	cell := NewUserPrompt("First $tool message", []TextElement{{Start: 0, End: 5}}, []string{"screenshot.png"}, nil)
	lines := cell.DisplayLines(32)
	if len(lines) == 0 {
		t.Fatal("user prompt rendered no lines")
	}
	for _, line := range lines {
		if !strings.Contains(line, "\x1b[100m") {
			t.Fatalf("prompt row lost the DarkGray fallback background: %q", line)
		}
	}
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "\x1b[36m") {
		t.Fatalf("element accent painted without a resolved palette:\n%q", joined)
	}
	if !strings.Contains(joined, "First $tool message") {
		t.Fatalf("prompt text was not preserved:\n%q", joined)
	}
}

// Rust parity: `vt100_spoken_prompt_keeps_its_marker_color_without_a_terminal_palette`
// (#57554): the red voice marker survives the DarkGray fallback surface.
func TestSpokenPromptMarkerStaysRedWithoutTerminalPaletteLikeRust(t *testing.T) {
	restoreColors := codextui.SetDefaultTerminalColorsForTest(nil)
	defer restoreColors()
	restoreLevel := codextui.SetStdoutColorLevelForTest(codextui.ColorANSI256)
	defer restoreLevel()

	lines := NewSpokenUserPrompt("hello from voice").DisplayLines(30)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, ansiUserMessageSpoken+"\u203a ") {
		t.Fatalf("spoken marker lost its red color:\n%q", joined)
	}
	if !strings.Contains(joined, "\x1b[100m") {
		t.Fatalf("spoken prompt lost the fallback background:\n%q", joined)
	}
}

// With a resolved terminal palette the prompt keeps the blended fill and the
// element accent, matching Rust's resolved-background branch.
func TestUserPromptUsesBlendedFillWithTerminalPaletteLikeRust(t *testing.T) {
	restoreColors := codextui.SetDefaultTerminalColorsForTest(&codextui.DefaultColors{
		FG: codextui.RGBColor{R: 229, G: 229, B: 229},
		BG: codextui.RGBColor{},
	})
	defer restoreColors()
	restoreLevel := codextui.SetStdoutColorLevelForTest(codextui.ColorANSI256)
	defer restoreLevel()

	lines := NewUserPrompt("use $tool", nil, nil, nil).DisplayLines(20)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "\x1b[48;5;235m") {
		t.Fatalf("resolved palette did not produce the blended fill:\n%q", joined)
	}
	if !strings.Contains(joined, ansiUserMessageMention+"$tool") {
		t.Fatalf("resolved palette lost the element accent:\n%q", joined)
	}
}
