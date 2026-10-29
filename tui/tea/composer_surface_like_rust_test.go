package tea

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	codextui "codex_go/tui"
)

// Rust paints the composer block with `style::user_message_style()`
// (chat_composer.rs `Block::default().style(style).render(composer_rect, buf)`),
// which is `user_message_style_for(default_bg())`. With no palette reply that
// style is empty, so the block must stay unshaded instead of carrying a fixed
// ANSI fill.
func TestComposerSurfaceFollowsTerminalPaletteLikeRust(t *testing.T) {
	restoreLevel := codextui.SetStdoutColorLevelForTest(codextui.ColorTrue)
	defer restoreLevel()

	restoreUnknown := codextui.SetDefaultTerminalColorsForTest(nil)
	if surface, ok := composerSurfaceSGR(); ok {
		t.Fatalf("composer surface painted %q without a known terminal background", surface)
	}
	restoreUnknown()

	// A 12% white blend over #111111 is #2d2d2d, matching Rust's
	// `user_message_bg` for a dark terminal background.
	restoreKnown := codextui.SetDefaultTerminalColorsForTest(&codextui.DefaultColors{
		FG: codextui.RGBColor{R: 0xee, G: 0xee, B: 0xee},
		BG: codextui.RGBColor{R: 0x11, G: 0x11, B: 0x11},
	})
	defer restoreKnown()

	surface, ok := composerSurfaceSGR()
	if !ok {
		t.Fatal("composer surface missing with a known terminal background")
	}
	if got, want := surface, "\x1b[48;2;45;45;45m"; got != want {
		t.Fatalf("composer surface = %q, want %q", got, want)
	}

	// The fill has to span the whole composer block, including the single
	// padding row Rust paints above and below the prompt.
	model := NewModel(codextui.NewState(nil), Options{Width: 40, Height: 24})
	rendered := model.renderComposerRegion(">_ hello")
	if lines := len(strings.Split(rendered, "\n")); lines != 3 {
		t.Fatalf("composer block rows = %d, want 3 (blank, prompt, blank): %q", lines, rendered)
	}
}

// The composer draws its prompt and placeholder as spans that end with SGR
// resets. Rust fills the whole composer rectangle before drawing those spans,
// so the shade has to survive each reset; a line-based lipgloss background used
// to leave the prompt row unpainted while the padding rows stayed shaded.
func TestComposerSurfaceSurvivesContentResetsLikeRust(t *testing.T) {
	restoreLevel := codextui.SetStdoutColorLevelForTest(codextui.ColorTrue)
	defer restoreLevel()
	restoreColors := codextui.SetDefaultTerminalColorsForTest(&codextui.DefaultColors{
		FG: codextui.RGBColor{R: 0xee, G: 0xee, B: 0xee},
		BG: codextui.RGBColor{R: 0x11, G: 0x11, B: 0x11},
	})
	defer restoreColors()

	const fill = "\x1b[48;2;45;45;45m"
	model := NewModel(codextui.NewState(nil), Options{Width: 40, Height: 24})
	rendered := model.renderComposerRegion("\x1b[37m\u203a \x1b[0m\x1b[38;5;240mAsk Codex to do anything\x1b[0m")

	rows := strings.Split(rendered, "\n")
	if len(rows) != 3 {
		t.Fatalf("composer block rows = %d, want 3 (blank, prompt, blank): %q", len(rows), rendered)
	}
	for i, row := range rows {
		if !strings.HasPrefix(row, fill) {
			t.Fatalf("composer row %d does not start with the fill: %q", i, row)
		}
		if !strings.HasSuffix(row, "\x1b[0m") {
			t.Fatalf("composer row %d does not reset at the end: %q", i, row)
		}
		if got := ansi.StringWidth(row); got != 40 {
			t.Fatalf("composer row %d width = %d, want 40: %q", i, got, row)
		}
	}
	if !strings.Contains(rows[1], "\x1b[0m"+fill) {
		t.Fatalf("prompt row lost the fill after an inner reset: %q", rows[1])
	}
}
