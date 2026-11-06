package tea

import (
	"strings"

	codextui "codex_go/tui"
)

// ConversationRect names a sub-rectangle of the viewport, mirroring ratatui's
// Rect. A host embedding the TUI passes it through Options.ConversationInset
// so the conversation paints inside the area it owns (Rust #58718).
type ConversationRect struct {
	X      int
	Y      int
	Width  int
	Height int
}

// conversationInsetApplies reports whether the conversation is the surface
// being painted. Pager overlays, dashboards, session pickers and centered
// dialogs own the whole viewport, exactly like Rust's centered-dialog path.
func (m *Model) conversationInsetApplies() bool {
	if m == nil {
		return false
	}
	if m.overlay != nil || m.agentsOverview != nil {
		return false
	}
	if m.analyticsDashboard != nil && m.analyticsDashboard.active {
		return false
	}
	if m.modal != nil && m.modal.sessionPicker != nil {
		return false
	}
	if m.modal != nil && !m.modal.inlinePicker && (modalCenteredOverlayKind(m.modal.kind) || m.modal.confirmationView) {
		return false
	}
	return true
}

// renderConversationInset paints the conversation inside rect and leaves the
// surrounding cells blank, so the host's own content stays visible around it.
// The conversation is laid out for the inset size, which reflows the transcript
// and composer to the supplied width just like Rust's prepare_conversation.
func (m *Model) renderConversationInset(rect ConversationRect) string {
	if m == nil {
		return ""
	}
	fullWidth := firstPositive(m.width, defaultWidth)
	fullHeight := firstPositive(m.height, defaultHeight)
	x := max(0, min(rect.X, fullWidth))
	y := max(0, min(rect.Y, fullHeight))
	width := max(0, min(rect.Width, fullWidth-x))
	height := max(0, min(rect.Height, fullHeight-y))
	if width <= 0 || height <= 0 {
		previous := m.conversationInset
		m.conversationInset = nil
		body := m.viewString()
		m.conversationInset = previous
		return body
	}
	previous := m.conversationInset
	m.conversationInset = nil
	m.applyLayoutSize(width, height)
	body := m.viewString()
	m.conversationInset = previous
	m.applyLayoutSize(fullWidth, fullHeight)

	lines := strings.Split(body, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	out := make([]string, 0, fullHeight)
	blank := strings.Repeat(" ", fullWidth)
	for i := 0; i < y; i++ {
		out = append(out, blank)
	}
	padding := strings.Repeat(" ", x)
	for _, line := range lines {
		line = padding + codextui.TruncateToWidth(line, width)
		if pad := fullWidth - codextui.DisplayWidth(line); pad > 0 {
			line += strings.Repeat(" ", pad)
		}
		out = append(out, line)
	}
	for len(out) < fullHeight {
		out = append(out, blank)
	}
	m.offsetConversationPointerRows(x, y)
	return strings.Join(out, "\n")
}

// offsetConversationPointerRows moves the recorded cursor and selectable rows
// into viewport coordinates; they were computed relative to the inset origin.
func (m *Model) offsetConversationPointerRows(x, y int) {
	if m == nil {
		return
	}
	for _, row := range []*int{&m.transcriptContentTopRow, &m.composerTopRow, &m.composerTextTopRow, &m.followControlRow} {
		if *row >= 0 {
			*row += y
		}
	}
	if m.followControlRow >= 0 {
		m.followControlColumn += x
	}
	for index := range m.renderedSelection.regions {
		region := &m.renderedSelection.regions[index]
		if region.h <= 0 {
			continue
		}
		region.x += x
		region.y += y
	}
	if m.renderedSelection.active {
		m.renderedSelection.anchorX += x
		m.renderedSelection.anchorY += y
		m.renderedSelection.endX += x
		m.renderedSelection.endY += y
	}
}
