package mentionsv2

import "strings"

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
	// Filter tabs now live above the results (#46694), so the footer carries
	// only the key hints.
	return truncateRunes(FooterHintLine(), width)
}
