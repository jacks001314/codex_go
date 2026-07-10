package markdown

import (
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
	"github.com/muesli/termenv"
)

const defaultWidth = 80

func Render(text string, width int) (string, error) {
	return RenderWithTheme(text, width, "")
}

func RenderWithTheme(text string, width int, themeID string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", nil
	}
	if width <= 0 {
		width = defaultWidth
	}
	style := styles.NoTTYStyleConfig
	style.CodeBlock.Theme = chromaThemeForCodexTheme(themeID)
	style.CodeBlock.Chroma = nil
	style.CodeBlock.StyleBlock = ansi.StyleBlock{}
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStyles(style),
		glamour.WithWordWrap(width),
		glamour.WithColorProfile(termenv.TrueColor),
		glamour.WithChromaFormatter("terminal16m"),
	)
	if err != nil {
		return "", err
	}
	out, err := renderer.Render(text)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(out, "\n"), nil
}

func chromaThemeForCodexTheme(themeID string) string {
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
