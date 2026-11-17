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
	segments := previewData.StatusLineSegmentsForItems(items)
	line, ok := bottompane.StatusLineFromSegments(segments, useThemeColors)
	if !ok {
		return ""
	}
	// Rust #60027: configured status lines and their live picker previews stay
	// on the native renderer; the extensions flag only enables experimental MCP
	// status line providers.
	hyperlink := ""
	if m.statusControls != nil {
		hyperlink = m.statusControls.StatusLineHyperlink
	}
	return line.RenderStyledWithHyperlinks(hyperlink, m.statusLineThreadColorSGR(), func(accent bottompane.StatusLineAccent) string {
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
