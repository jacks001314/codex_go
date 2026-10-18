package tea

import (
	"strings"

	codextui "codex_go/tui"
	"codex_go/tui/markdown"
	"codex_go/utils"
)

// renderStartupTooltipLines renders the fullscreen startup tip beneath the
// session header. Rust #47954 keeps the styled Markdown link and wraps on narrow
// terminals instead of rendering the tip as one composer-row hint.
func renderStartupTooltipLines(template string, width int, keymap *codextui.KeymapConfig, themeID string, cwd string) []string {
	if strings.TrimSpace(template) == "" || width <= 2 {
		return nil
	}
	tip, ok := codextui.RenderTooltip(template, keymap)
	if !ok {
		return nil
	}
	rendered, err := markdown.RenderWithThemeCwd("**Tip:** "+tip, width-2, themeID, cwd)
	if err != nil {
		return nil
	}
	rendered = strings.ReplaceAll(rendered, "\r\n", "\n")
	lines := make([]string, 0, 3)
	for _, line := range strings.Split(rendered, "\n") {
		line = strings.TrimRight(line, " ")
		if strings.TrimSpace(utils.StripANSI(line)) == "" {
			continue
		}
		lines = append(lines, "  "+line)
	}
	return lines
}
