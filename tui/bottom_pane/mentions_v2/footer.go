package mentionsv2

import (
	"strings"

	codextui "codex_go/tui"
)

// Rust parity subset: codex-rs/tui/src/bottom_pane/mentions_v2/footer.rs.

type Footer struct {
	Text       string
	SearchMode SearchMode
}

func FooterHintLine() string {
	// Rust's mentions_v2 footer joins each key group with `/` and renders the
	// compact labels: "enter/tab insert · esc close · ↑/↓ select · ←/→ filter".
	return "enter/tab insert \u00b7 esc close \u00b7 \u2191/\u2193 select \u00b7 \u2190/\u2192 filter"
}

func SearchModeIndicatorLine(active SearchMode) string {
	modes := []SearchMode{SearchModeResults, SearchModeFilesystemOnly, SearchModeTools}
	parts := make([]string, 0, len(modes))
	for _, mode := range modes {
		label := mode.Label()
		if mode == active || (active == "" && mode == SearchModeResults) {
			parts = append(parts, "["+label+"]")
		} else {
			parts = append(parts, " "+label+" ")
		}
	}
	return strings.Join(parts, "  ")
}

func RenderFooter(width int, searchMode SearchMode) string {
	if width <= 0 {
		return ""
	}
	right := SearchModeIndicatorLine(searchMode)
	rightWidth := codextui.DisplayWidth(right)
	leftWidth := width - rightWidth
	if right != "" {
		leftWidth--
	}
	if leftWidth < 0 {
		leftWidth = 0
	}
	left := truncateRunes(FooterHintLine(), leftWidth)
	if right == "" || rightWidth > width {
		return truncateRunes(left, width)
	}
	gap := width - codextui.DisplayWidth(left) - rightWidth
	if gap < 0 {
		gap = 0
	}
	return truncateRunes(left+strings.Repeat(" ", gap)+right, width)
}
