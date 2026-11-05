package tea

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"codex_go/features"
	codextui "codex_go/tui"
	"codex_go/tui/transcript_image"
)

// Rust parity: codex-rs/tui/src/app/owned_transcript.rs and app.rs (#58992).
//
// With the disabled-by-default `tui_workspace` feature, a side conversation is
// shown beside its parent instead of replacing it. Only the focused
// conversation owns the composer, so drawing both transcripts cannot change
// where input goes.

const (
	sideWorkspaceFeatureKey = "tui_workspace"
	// sideWorkspaceMinWidth mirrors Rust's 100-column threshold: narrower
	// terminals fall back to the single focused transcript.
	sideWorkspaceMinWidth = 100
	sideWorkspaceDivider  = " │ "
	// Rust #59595 keeps the sidebar between 36 columns and the remaining space
	// after reserving 64 columns for the parent transcript.
	sideWorkspaceMinSideWidth   = 36
	sideWorkspaceMinParentWidth = 64
	sideWorkspaceDefaultPercent = 50
)

// sideExactWidth pins the sidebar allocation at one terminal width so the
// percentage conversion cannot drift between frames (Rust #59595).
type sideExactWidth struct {
	total int
	width int
	valid bool
}

// sidePaneResize is the keyboard resize preview. previewWidth is the sidebar
// column count; the saved percent only changes when the preview is accepted.
type sidePaneResize struct {
	previewWidth int
}

func widthPercent(width int, total int) int {
	if total <= 0 {
		return sideWorkspaceDefaultPercent
	}
	percent := (width*100 + total/2) / total
	return clampModalInt(percent, 1, 99)
}

// sidebarColumns mirrors Rust sidebar_width: clamp the sidebar to at least 36
// columns and leave the parent at least 64 columns when the terminal allows it.
func sidebarColumns(width int, total int) int {
	available := total - ansi.StringWidth(sideWorkspaceDivider)
	maxSide := max(available-sideWorkspaceMinParentWidth, sideWorkspaceMinSideWidth)
	return clampModalInt(width, sideWorkspaceMinSideWidth, maxSide)
}

func (m *Model) sideWidthPercentValue() int {
	if m == nil || m.sideWidthPercent <= 0 {
		return sideWorkspaceDefaultPercent
	}
	return clampModalInt(m.sideWidthPercent, 1, 99)
}

// sideWorkspaceWidthsForPercent splits the terminal using the saved sidebar
// width, honouring an exact column allocation at the same terminal width.
func sideWorkspaceWidthsForPercent(width int, percent int, exact sideExactWidth) (leftWidth int, rightWidth int, ok bool) {
	if width < sideWorkspaceMinWidth {
		return 0, 0, false
	}
	available := width - ansi.StringWidth(sideWorkspaceDivider)
	if available < 2 {
		return 0, 0, false
	}
	sideWidth := sidebarColumns((available*clampModalInt(percent, 1, 99)+50)/100, width)
	if exact.valid && exact.total == width && widthPercent(exact.width, width) == clampModalInt(percent, 1, 99) {
		sideWidth = sidebarColumns(exact.width, width)
	}
	leftWidth = available - sideWidth
	if leftWidth < 1 || sideWidth < 1 {
		return 0, 0, false
	}
	return leftWidth, sideWidth, true
}

func (m *Model) sideWorkspaceEnabled() bool {
	if m == nil {
		return false
	}
	return features.Enabled(m.featureSettings, sideWorkspaceFeatureKey)
}

// sideWorkspaceWidths splits the terminal between the parent (left) and side
// (right) transcripts, reserving the divider columns.
func sideWorkspaceWidths(width int) (leftWidth int, rightWidth int, ok bool) {
	if width < sideWorkspaceMinWidth {
		return 0, 0, false
	}
	available := width - ansi.StringWidth(sideWorkspaceDivider)
	if available < 2 {
		return 0, 0, false
	}
	leftWidth = available / 2
	rightWidth = available - leftWidth
	if leftWidth < 1 || rightWidth < 1 {
		return 0, 0, false
	}
	return leftWidth, rightWidth, true
}

// sidePaneLayout reports whether both transcripts are drawn and which of them
// currently owns the composer.
func (m *Model) sidePaneLayout(width int) (leftWidth int, rightWidth int, sideFocused bool, ok bool) {
	if m == nil || m.activeSide == nil || !m.sideWorkspaceEnabled() {
		return 0, 0, false, false
	}
	if m.transcriptUnavailableForPanes() {
		return 0, 0, false, false
	}
	// Zoom shows the focused conversation full width, which is the ordinary
	// single-transcript path (Rust #59595 layout returns None when zoomed).
	if m.sideZoomed {
		return 0, 0, false, false
	}
	if m.sideResize != nil {
		leftWidth, rightWidth, ok = sideWorkspaceWidthsForPercent(width, widthPercent(m.sideResize.previewWidth, width), sideExactWidth{})
	} else {
		leftWidth, rightWidth, ok = sideWorkspaceWidthsForPercent(width, m.sideWidthPercentValue(), m.sideExactWidth)
	}
	if !ok {
		return 0, 0, false, false
	}
	return leftWidth, rightWidth, m.activeSide.ShowingSide, true
}

// transcriptUnavailableForPanes keeps full-screen surfaces (history search,
// activity focus, modals handled by the caller) on the single-pane path so a
// pane never replaces a focused view with a stale copy.
func (m *Model) transcriptUnavailableForPanes() bool {
	if m == nil {
		return true
	}
	return m.mainFind.active() || m.activityFocus.active
}

// renderSideWorkspace draws the parent and side transcripts side by side. It
// returns false when the workspace layout does not apply, leaving callers on
// the ordinary single-transcript path.
func (m *Model) renderSideWorkspace(width int, height int) (string, bool) {
	leftWidth, rightWidth, sideFocused, ok := m.sidePaneLayout(width)
	if !ok || m.State == nil {
		// Rust #59595 discards a resize preview when the split layout is no
		// longer available.
		m.sideResize = nil
		return "", false
	}
	if m.sideResize != nil {
		height--
		if height < 1 {
			height = 1
		}
	}
	side := m.activeSide
	parentMessages := side.ParentMessages
	sideMessages := side.SideMessages
	// The focused snapshot is the live transcript; the inactive copy is the
	// retained one captured when focus last changed.
	if sideFocused {
		sideMessages = m.State.Messages
	} else {
		parentMessages = m.State.Messages
	}
	parentCache := &transcriptMessageCache{}
	sideCache := &transcriptMessageCache{}
	left := m.renderSidePaneTranscript(parentCache, side.ParentThreadID, parentMessages, leftWidth)
	right := m.renderSidePaneTranscript(sideCache, side.SideThreadID, sideMessages, rightWidth)
	rendered := joinSidePanes(left, right, leftWidth, height)
	if m.sideResize != nil {
		rendered += "\n" + m.sideResizeHint(rightWidth)
	}
	return rendered, true
}

// sideResizeHint mirrors the resize-mode keyboard hints Rust shows while the
// divider preview is active.
func (m *Model) sideResizeHint(sideWidth int) string {
	percent := widthPercent(sideWidth, max(m.width, 1))
	hint := "Resize panes \u00b7 " + strconv.Itoa(sideWidth) + " cols (" + strconv.Itoa(percent) + "%) \u00b7 "
	hint += "\u2190/\u2192 or h/l move \u00b7 shift+\u2190/\u2192 10 cols \u00b7 enter/esc save"
	return codextui.TruncateWithEllipsis(hint, max(m.width, 1))
}

func (m *Model) renderSidePaneTranscript(cache *transcriptMessageCache, threadID string, messages []codextui.Message, width int) string {
	if m == nil || m.State == nil || width <= 0 {
		return ""
	}
	paneState := *m.State
	paneState.Messages = messages
	// Static image placements belong to the focused transcript; pane text keeps
	// the message content without publishing a second set of placements.
	noImages := func(codextui.Message, bool) *transcript_image.TranscriptImage { return nil }
	content, _, _, _ := renderTranscriptLayout(
		cache,
		&paneState,
		m.rawOutput,
		width,
		m.activeTUITheme(),
		false,
		nil,
		m.sessionCWD,
		m.showRawReasoning,
		noImages,
		m.transcriptDisclosureLayoutEnabled(),
		false,
		"sidepane|"+threadID,
	)
	return content
}

// joinSidePanes keeps the tail of each transcript, pads the left column to the
// divider, and renders exactly `height` rows.
func joinSidePanes(left string, right string, leftWidth int, height int) string {
	if height <= 0 {
		height = 1
	}
	leftLines := tailDisplayLines(left, height)
	rightLines := tailDisplayLines(right, height)
	rows := make([]string, 0, height)
	for index := 0; index < height; index++ {
		line := ""
		if index < len(leftLines) {
			line = padSidePaneLine(leftLines[index], leftWidth)
		} else {
			line = strings.Repeat(" ", leftWidth)
		}
		content := ""
		if index < len(rightLines) {
			content = rightLines[index]
		}
		rows = append(rows, line+sideWorkspaceDivider+content)
	}
	return strings.Join(rows, "\n")
}

// padSidePaneLine pads a styled transcript row to the pane width. Transcript
// rows carry SGR sequences, so the padding must measure the rendered width
// rather than the raw text.
func padSidePaneLine(line string, width int) string {
	if width <= 0 {
		return ""
	}
	padding := width - ansi.StringWidth(line)
	if padding <= 0 {
		return line
	}
	return line + strings.Repeat(" ", padding)
}

func tailDisplayLines(content string, limit int) []string {
	if limit <= 0 {
		return nil
	}
	if content == "" {
		return nil
	}
	lines := strings.Split(content, "\n")
	if len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	return lines
}
