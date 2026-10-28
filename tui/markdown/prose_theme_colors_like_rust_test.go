package markdown

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	"codex_go/utils"
)

// Rust #54217 (upstream e89b7c442c) "Apply syntax theme colors to Markdown prose
// in the TUI": MarkdownStyles::for_theme borrows the active syntax theme's
// markup-scope foregrounds for headings, emphasis, strong, list markers, links and
// blockquotes, keeping the existing modifiers and falling back when a scope
// provides no foreground.
//
// Go renders prose through glamour, so the same foregrounds are projected onto the
// glamour StyleConfig before the renderer is built (headings, emphasis, strong,
// links, blockquotes) and onto the list-marker repainters the Go pipeline owns.
// A scope the theme does not style leaves the previous fallback untouched, which is
// what a theme like `ada` exercises for the list scopes.

const proseThemeSource = "# Head\n\n*em* **strong**\n\n> quote\n\n- item\n\n1. first\n"

func proseLines(t *testing.T, themeID string) []string {
	t.Helper()
	rendered, err := RenderWithTheme(proseThemeSource, 80, themeID)
	if err != nil {
		t.Fatalf("RenderWithTheme(%q) error: %v", themeID, err)
	}
	return strings.Split(rendered, "\n")
}

func lineContaining(t *testing.T, lines []string, needle string) string {
	t.Helper()
	for _, line := range lines {
		if strings.Contains(utils.StripANSI(line), needle) {
			return line
		}
	}
	t.Fatalf("no rendered line contains %q in %q", needle, lines)
	return ""
}

// profileColorSequence is the SGR parameter list glamour emits for a theme colour
// at this process's colour depth: the Go renderer is profile-adaptive (Rust emits
// ratatui colours directly), so the expected bytes are derived the same way.
func profileColorSequence(value string) string {
	color := codextui.TermenvProfileForLevel(codextui.DetectStdoutColorLevel()).Color(value)
	if color == nil {
		return ""
	}
	return color.Sequence(false)
}

func TestMarkdownProseUsesThemeMarkupColorsLikeRust(t *testing.T) {
	const theme = "catppuccin-mocha"
	headingColor := codextui.ThemeScopeForegroundValue(theme, "markup.heading.markdown")
	if headingColor == "" {
		t.Fatalf("theme %q styles no heading scope in the Go theme stack", theme)
	}
	lines := proseLines(t, theme)
	heading := lineContaining(t, lines, "Head")
	if seq := profileColorSequence(headingColor); seq != "" && !strings.Contains(heading, "\x1b["+seq) {
		t.Fatalf("themed heading = %q, want the theme heading colour %q (%s)", heading, headingColor, seq)
	}
	// Rust: ordered_list_marker = light_blue().patch(theme markup.list.numbered).
	orderedMarkerSGR := codextui.ThemeScopeForegroundSGR(theme, "markup.list.numbered.markdown")
	if orderedMarkerSGR == "" {
		t.Fatalf("theme %q styles no numbered list scope", theme)
	}
	ordered := lineContaining(t, lines, "1. first")
	if want := orderedMarkerSGR + "1. \x1b[39mfirst"; !strings.Contains(ordered, want) {
		t.Fatalf("ordered marker line = %q, want %q", ordered, want)
	}
	// Rust: unordered_list_marker borrows markup.list.unnumbered/markup.list.bullet.
	bulletSGR := codextui.ThemeScopeForegroundSGR(theme, "markup.list.unnumbered.markdown", "markup.list.bullet.markdown")
	if bulletSGR == "" {
		t.Fatalf("theme %q styles no unordered list scope", theme)
	}
	bullet := lineContaining(t, lines, "item")
	if want := bulletSGR + "\u2022 \x1b[39mitem"; !strings.Contains(bullet, want) {
		t.Fatalf("unordered marker line = %q, want %q", bullet, want)
	}
}

func TestMarkdownProseKeepsFallbacksWithoutThemeColorsLikeRust(t *testing.T) {
	plain := proseLines(t, "")
	plainHeading := lineContaining(t, plain, "Head")
	// Rust keeps the modifier-only heading style when the syntax theme is empty.
	if !strings.HasPrefix(plainHeading, "\x1b[4;1m\x1b[0m\x1b[4;1m\x1b[0m\x1b[4;1m#\x1b[0m\x1b[4;1m Head\x1b[0m") {
		t.Fatalf("unthemed heading = %q, want the modifier-only heading", plainHeading)
	}
	if ordered := lineContaining(t, plain, "1. first"); !strings.Contains(ordered, "\x1b[38;5;12m1. \x1b[39mfirst") {
		t.Fatalf("unthemed ordered marker = %q, want the LightBlue palette marker", ordered)
	}
	if bullet := lineContaining(t, plain, "item"); strings.Contains(bullet, "\x1b[38;2;") || strings.Contains(bullet, "\x1b[38;5;") {
		t.Fatalf("unthemed unordered marker = %q, want no accent colour", bullet)
	}

	// A theme that styles headings but no list scope keeps the palette fallback for
	// the markers (Rust: the scope contributes no foreground).
	const adaTheme = "ada"
	adaHeadingColor := codextui.ThemeScopeForegroundValue(adaTheme, "markup.heading.markdown")
	if adaHeadingColor == "" {
		t.Fatalf("theme %q styles no heading scope in the Go theme stack", adaTheme)
	}
	if codextui.ThemeScopeForegroundSGR(adaTheme, "markup.list.numbered.markdown") != "" {
		t.Fatalf("theme %q unexpectedly styles the numbered list scope", adaTheme)
	}
	ada := proseLines(t, adaTheme)
	adaHeading := lineContaining(t, ada, "Head")
	if seq := profileColorSequence(adaHeadingColor); seq != "" && !strings.Contains(adaHeading, "\x1b["+seq) {
		t.Fatalf("ada heading = %q, want the theme heading colour %q", adaHeading, adaHeadingColor)
	}
	if ordered := lineContaining(t, ada, "1. first"); !strings.Contains(ordered, "\x1b[38;5;12m1. \x1b[39mfirst") {
		t.Fatalf("ada ordered marker = %q, want the LightBlue palette fallback", ordered)
	}
	if em := lineContaining(t, ada, "em"); em != lineContaining(t, plain, "em") {
		t.Fatalf("ada emphasis line = %q, want the unthemed emphasis line (no theme colour)", em)
	}
}

func TestApplyThemeMarkupColorsLeavesUnstyledScopesAlone(t *testing.T) {
	// Rust patches only the scopes the theme actually styles.
	base := codexMarkdownStyle()
	style := applyThemeMarkupColors(base, "ada", codextui.ColorTrue)
	if style.Emph.Color != base.Emph.Color || style.Strong.Color != base.Strong.Color ||
		style.Link.Color != base.Link.Color || style.BlockQuote.Color != base.BlockQuote.Color {
		t.Fatalf("unstyled prose scopes were patched")
	}
	if style.H1.Color == base.H1.Color || style.H1.Color == nil {
		t.Fatalf("the themed heading scope was not patched")
	}
	if got := codextui.ThemeScopeForegroundValue("ada", "markup.heading.markdown"); got == "" || *style.H1.Color != got {
		t.Fatalf("heading colour = %v, want the theme value %q", style.H1.Color, got)
	}
	empty := applyThemeMarkupColors(codexMarkdownStyle(), "", codextui.ColorTrue)
	if empty.H1.Color != base.H1.Color {
		t.Fatalf("an empty theme patched the heading colour: %q", *empty.H1.Color)
	}
}
