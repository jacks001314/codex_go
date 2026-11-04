package historycell

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
)

// Rust parity: codex-rs/tui/src/style.rs `history_prompt_fallback` (#59257).
// A terminal that never answers the default-color probe keeps the submitted
// prompt readable with the xterm 238/255 grays and still paints element accents.
func TestUserPromptKeepsContrastWithoutTerminalPaletteLikeRust(t *testing.T) {
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
		if !strings.Contains(line, "\x1b[38;5;255;48;5;238m") {
			t.Fatalf("prompt row lost the fallback contrast: %q", line)
		}
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, ansiUserMessageMention+"$tool"+ansiUserMessageMentionEnd+codextui.HistoryPromptTextSGR(codextui.ColorANSI256)) {
		t.Fatalf("element accent did not restore the fallback foreground:\n%q", joined)
	}
	if !strings.Contains(joined, " message") {
		t.Fatalf("prompt text was not preserved:\n%q", joined)
	}
}

// Rust parity: `vt100_spoken_prompt_keeps_its_marker_color_without_a_terminal_palette`
// (#59257): the red voice marker survives and the following text restores the
// fallback foreground.
func TestSpokenPromptMarkerStaysRedWithoutTerminalPaletteLikeRust(t *testing.T) {
	restoreColors := codextui.SetDefaultTerminalColorsForTest(nil)
	defer restoreColors()
	restoreLevel := codextui.SetStdoutColorLevelForTest(codextui.ColorANSI256)
	defer restoreLevel()

	lines := NewSpokenUserPrompt("hello from voice").DisplayLines(30)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, ansiUserMessageSpoken+"\u203a "+ansiUserMessageSpokenEnd+codextui.HistoryPromptTextSGR(codextui.ColorANSI256)) {
		t.Fatalf("spoken marker did not restore the fallback foreground:\n%q", joined)
	}
	if !strings.Contains(joined, "\x1b[38;5;255;48;5;238m") {
		t.Fatalf("spoken prompt lost the fallback background:\n%q", joined)
	}
}

// ANSI-16 terminals keep their own colors: the fallback paints no surface and
// the element accent remains terminal-owned.
func TestUserPromptKeepsTerminalColorsWithoutPaletteOnANSI16LikeRust(t *testing.T) {
	restoreColors := codextui.SetDefaultTerminalColorsForTest(nil)
	defer restoreColors()
	restoreLevel := codextui.SetStdoutColorLevelForTest(codextui.ColorANSI16)
	defer restoreLevel()

	lines := NewUserPrompt("use $tool", nil, nil, nil).DisplayLines(20)
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "48;5;") || strings.Contains(joined, "48;2;") {
		t.Fatalf("ANSI-16 fallback painted a surface:\n%q", joined)
	}
	if !strings.Contains(joined, ansiUserMessageMention+"$tool") {
		t.Fatalf("ANSI-16 prompt lost the element accent:\n%q", joined)
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
