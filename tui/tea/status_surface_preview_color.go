package tea

import (
	"strings"

	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
	"codex_go/tui/chatwidget"
)

// Rust parity: codex-rs/tui/src/bottom_pane/status_surface_preview.rs (#44857).
//
// The status line setup preview must render with the same spans and styling as
// the live footer, so the per-thread identity color on thread-name/thread-title
// matches the footer while a thread is renamed or auto-named.

// statusLinePreviewStyled renders the status line setup preview for the given
// items with the footer's accent resolution and thread identity color.
func (m *Model) statusLinePreviewStyled(previewData chatwidget.StatusSurfacePreviewData, items []bottompane.StatusLineItem, useThemeColors bool) string {
	line, ok := previewData.StatusLineForItems(items, useThemeColors)
	if !ok {
		return ""
	}
	return line.RenderStyled(m.statusLineThreadColorSGR(), func(accent bottompane.StatusLineAccent) string {
		return statusLineAccentSGR(m.tuiTheme, accent)
	}, false)
}

// statusLineThreadColorSGR returns the identity color SGR for the active
// thread, or "" when there is no thread, the theme is unknown, or the palette
// has no accents (Rust falls back to the terminal default).
func (m *Model) statusLineThreadColorSGR() string {
	if m == nil {
		return ""
	}
	threadID := strings.TrimSpace(m.currentThreadID())
	if threadID == "" {
		return ""
	}
	return codextui.ThreadColorSGR(codextui.ThreadColorForTheme(threadID, m.tuiTheme))
}
