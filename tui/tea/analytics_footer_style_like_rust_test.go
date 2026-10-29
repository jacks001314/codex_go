package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	"codex_go/tui/styles"
)

// TestAnalyticsFooterHintKeysFollowTerminalBackgroundLikeRust pins Rust
// #45772's footer_hint_key_style: black shortcut keys on light terminal
// backgrounds and the terminal's default foreground otherwise.
func TestAnalyticsFooterHintKeysFollowTerminalBackgroundLikeRust(t *testing.T) {
	restoreLight := codextui.SetDefaultTerminalColorsForTest(&codextui.DefaultColors{
		FG: codextui.RGBColor{R: 30, G: 30, B: 30},
		BG: codextui.RGBColor{R: 255, G: 255, B: 255},
	})
	defer restoreLight()
	if got := analyticsFooterKeyStyle(); got != "\x1b[30m" {
		t.Fatalf("light footer key style = %q, want black", got)
	}
	if styled := analyticsStyledControls(styles.DefaultDark(), "q close"); !strings.Contains(styled, "\x1b[30m") {
		t.Fatalf("light footer key was not painted black: %q", styled)
	}

	restoreDark := codextui.SetDefaultTerminalColorsForTest(&codextui.DefaultColors{
		FG: codextui.RGBColor{R: 220, G: 220, B: 220},
		BG: codextui.RGBColor{R: 0, G: 0, B: 0},
	})
	defer restoreDark()
	if got := analyticsFooterKeyStyle(); got != "" {
		t.Fatalf("dark footer key style = %q, want the terminal default", got)
	}
}
