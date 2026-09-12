package tea

import (
	"strconv"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/styles"

	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
)

// Rust parity: codex-rs/tui/src/bottom_pane/status_line_style.rs.
// Each accent borrows the active syntax theme's color for a set of highlight
// scopes, softened toward the color's luma (85% saturation), falling back to a
// fixed palette when the theme contributes nothing.

const (
	statusLineColorSaturationPercent = 85
)

// statusLineAccentTokenTypes maps a status-line accent to the theme tokens whose
// foreground it borrows (Rust StatusLineAccent::scopes).
func statusLineAccentTokenTypes(accent bottompane.StatusLineAccent) []chroma.TokenType {
	switch accent {
	case bottompane.StatusLineAccentModel:
		return []chroma.TokenType{chroma.KeywordType, chroma.NameClass, chroma.NameVariable}
	case bottompane.StatusLineAccentPath:
		return []chroma.TokenType{chroma.LiteralString}
	case bottompane.StatusLineAccentBranch:
		return []chroma.TokenType{chroma.NameFunction, chroma.NameTag}
	case bottompane.StatusLineAccentState:
		return []chroma.TokenType{chroma.KeywordDeclaration, chroma.Keyword, chroma.KeywordReserved}
	case bottompane.StatusLineAccentUsage:
		return []chroma.TokenType{chroma.LiteralNumber}
	case bottompane.StatusLineAccentLimit:
		return []chroma.TokenType{chroma.KeywordConstant, chroma.KeywordType}
	case bottompane.StatusLineAccentMetadata:
		return []chroma.TokenType{chroma.Comment}
	case bottompane.StatusLineAccentMode:
		return []chroma.TokenType{chroma.Operator, chroma.KeywordDeclaration}
	case bottompane.StatusLineAccentThread:
		return []chroma.TokenType{chroma.GenericHeading}
	case bottompane.StatusLineAccentProgress:
		return []chroma.TokenType{chroma.GenericInserted, chroma.LiteralNumber}
	default:
		return nil
	}
}

// statusLineAccentSGR returns the softened theme foreground SGR for an accent,
// or "" when the theme contributes no color (callers then use the fallback
// palette).
func statusLineAccentSGR(themeID string, accent bottompane.StatusLineAccent) string {
	style := styles.Get(codextui.ChromaThemeForCodexTheme(themeID))
	if style == nil {
		return ""
	}
	for _, tokenType := range statusLineAccentTokenTypes(accent) {
		entry := style.Get(tokenType)
		if !entry.Colour.IsSet() {
			continue
		}
		return softenStatusLineColorSGR(entry.Colour.String())
	}
	return ""
}

// softenStatusLineColorSGR applies Rust's soften_status_line_color to a
// "#rrggbb" color: each channel is mixed toward the weighted luma, then left at
// full brightness.
func softenStatusLineColorSGR(color string) string {
	r, g, b, ok := parseStatusLineHexColor(color)
	if !ok {
		return ""
	}
	luma := (77*int(r) + 150*int(g) + 29*int(b)) / 256
	soften := func(channel int) int {
		return (channel*statusLineColorSaturationPercent + luma*(100-statusLineColorSaturationPercent) + 50) / 100
	}
	return "\x1b[38;2;" + strconv.Itoa(soften(int(r))) + ";" + strconv.Itoa(soften(int(g))) + ";" + strconv.Itoa(soften(int(b))) + "m"
}

func parseStatusLineHexColor(color string) (int, int, int, bool) {
	color = strings.TrimSpace(color)
	if !strings.HasPrefix(color, "#") || len(color) != 7 {
		return 0, 0, 0, false
	}
	value, err := strconv.ParseUint(color[1:], 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return int(value>>16) & 0xff, int(value>>8) & 0xff, int(value) & 0xff, true
}
