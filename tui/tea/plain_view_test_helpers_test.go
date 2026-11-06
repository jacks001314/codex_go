package tea

import "codex_go/utils"

// plainView strips SGR/OSC escapes from a rendered frame. Bubble Tea v2 and
// lipgloss v2 style each span separately, so literal phrases such as
// "\u203a /help" no longer appear contiguously in the raw frame.
func plainView(view string) string {
	return utils.StripANSI(view)
}
