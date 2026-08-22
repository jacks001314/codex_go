package tui

import (
	"bytes"
	"strings"

	"github.com/alecthomas/chroma/v2/quick"
)

func HighlightCodeANSI(code string, language string, themeID string) string {
	if code == "" {
		return ""
	}
	var out bytes.Buffer
	if err := quick.Highlight(&out, code, language, ChromaFormatterForLevel(diffColorLevel()), ChromaThemeForCodexTheme(themeID)); err != nil {
		return code
	}
	return strings.TrimRight(out.String(), "\r\n")
}

// ChromaFormatterForLevel maps the detected terminal color depth to the chroma
// terminal formatter so syntax highlighting never emits truecolor escapes on a
// terminal that cannot display them (Rust parity: terminal color level drives
// the highlighter's color format).
func ChromaFormatterForLevel(level StdoutColorLevel) string {
	switch level {
	case ColorTrue:
		return "terminal16m"
	case ColorANSI256:
		return "terminal256"
	case ColorANSI16:
		return "terminal16"
	default:
		return "terminal16m"
	}
}

func HighlightBashANSI(script string, themeID string) string {
	return HighlightCodeANSI(script, "bash", themeID)
}

func ChromaThemeForCodexTheme(themeID string) string {
	id := strings.ToLower(strings.TrimSpace(themeID))
	if style, ok := codexThemeToChromaStyle[id]; ok {
		return style
	}
	for prefix, style := range codexThemeToChromaPrefix {
		if strings.HasPrefix(id, prefix) {
			return style
		}
	}
	return "catppuccin-mocha"
}

var codexThemeToChromaStyle = map[string]string{
	"1337":                    "rrt",
	"ansi":                    "native",
	"base16":                  "base16-snazzy",
	"base16-256":              "base16-snazzy",
	"base16-eighties-dark":    "paraiso-dark",
	"base16-mocha-dark":       "paraiso-dark",
	"base16-ocean-dark":       "nord",
	"base16-ocean-light":      "xcode",
	"catppuccin-frappe":       "catppuccin-frappe",
	"catppuccin-latte":        "catppuccin-latte",
	"catppuccin-macchiato":    "catppuccin-macchiato",
	"catppuccin-mocha":        "catppuccin-mocha",
	"coldark-cold":            "github",
	"coldark-dark":            "github-dark",
	"dark-neon":               "native",
	"dracula":                 "dracula",
	"github":                  "github-dark",
	"gruvbox-dark":            "gruvbox",
	"gruvbox-light":           "gruvbox-light",
	"inspired-github":         "github",
	"monokai-extended":        "monokai",
	"monokai-extended-bright": "monokai",
	"monokai-extended-light":  "monokailight",
	"monokai-extended-origin": "monokai",
	"nord":                    "nord",
	"one-half-dark":           "onedark",
	"one-half-light":          "xcode",
	"solarized-dark":          "solarized-dark",
	"solarized-light":         "solarized-light",
	"sublime-snazzy":          "base16-snazzy",
	"two-dark":                "doom-one",
	"zenburn":                 "native",
}

var codexThemeToChromaPrefix = map[string]string{
	"base16":     "base16-snazzy",
	"catppuccin": "catppuccin-mocha",
	"coldark":    "github-dark",
	"gruvbox":    "gruvbox",
	"monokai":    "monokai",
	"one-half":   "onedark",
	"solarized":  "solarized-dark",
}
