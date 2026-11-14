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
	sideWorkspaceDefaultPercent = 33
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
// (right) transcripts using the default sidebar share (Rust #59527 uses one
// third of the terminal until the user saves a width).
func sideWorkspaceWidths(width int) (leftWidth int, rightWidth int, ok bool) {
	return sideWorkspaceWidthsForPercent(width, sideWorkspaceDefaultPercent, sideExactWidth{})
}

// sidePaneLayout reports whether both transcripts are drawn and which of them
// currently owns the composer.
func (m *Model) sidePaneLayout(width int) (leftWidth int, rightWidth int, sideFocused bool, ok bool) {
	if m == nil {
		return 0, 0, false, false
	}
	chooser := m.sidebarChooserVisible()
	if !m.sideWorkspaceEnabled() || (m.activeSide == nil && !chooser) {
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
	if chooser {
		return leftWidth, rightWidth, true, true
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
	parentCache := &transcriptMessageCache{}
	sideCache := &transcriptMessageCache{}
	left := ""
	right := ""
	if m.sidebarChooserVisible() {
		// The chooser belongs to the parent conversation, so the live
		// transcript stays on the left while the list paints on the right
		// (Rust #59739).
		left = m.renderSidePaneTranscript(parentCache, m.sidebarParentThreadID(), m.State.Messages, leftWidth, m.paneStatusForThread(m.sidebarParentThreadID()), paneStatusRowBudget(height))
		right = m.renderSidebarChooser(rightWidth, height)
	} else {
		parentMessages := side.ParentMessages
		sideMessages := side.SideMessages
		// The focused snapshot is the live transcript; the inactive copy is the
		// retained one captured when focus last changed.
		if sideFocused {
			sideMessages = m.State.Messages
		} else {
			parentMessages = m.State.Messages
		}
		budget := paneStatusRowBudget(height)
		left = m.renderSidePaneTranscript(parentCache, side.ParentThreadID, parentMessages, leftWidth, m.paneStatusWhenUnfocused(sideFocused, side.ParentThreadID), budget)
		right = m.renderSidePaneTranscript(sideCache, side.SideThreadID, sideMessages, rightWidth, m.paneStatusWhenUnfocused(!sideFocused, side.SideThreadID), budget)
		// Rust #59784 paints the sibling tabs above the side transcript.
		right = m.renderSidebarTabStrip(rightWidth) + "\n" + right
	}
	// Rust #59928 paints the strip on the pane's first row; record which screen
	// row that is so the pointer hit test follows the painted origin. An inset
	// render shifts it later through offsetConversationPointerRows.
	if m.sidebarTabStripVisible() {
		m.sidebarTabStripTop = 0
	}
	// Rust #59527 keeps a scroll position per pane: the focused pane follows
	// the transcript viewport, while the inactive pane keeps the offset parked
	// when focus left it (or when the wheel last moved over it). A pane whose
	// offset was never set stays pinned to its latest output.
	parentFirst, sideFirst := sidePaneTail, sidePaneTail
	if side != nil {
		if side.ParentInteractionSaved {
			parentFirst = side.ParentScroll
		}
		if side.SideInteractionSaved {
			sideFirst = side.SideScroll
		}
	}
	if sideFocused {
		sideFirst = m.transcript.YOffset
	} else {
		parentFirst = m.transcript.YOffset
	}
	rendered, parentFirst, sideFirst, parentMax, sideMax := joinSidePanes(left, right, leftWidth, height, parentFirst, sideFirst)
	if side != nil {
		side.ParentViewMax = parentMax
		side.SideViewMax = sideMax
		if sideFocused {
			side.ParentScroll = parentFirst
		} else {
			side.SideScroll = sideFirst
		}
	}
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

func (m *Model) renderSidePaneTranscript(cache *transcriptMessageCache, threadID string, messages []codextui.Message, width int, status string, maxStatusRows int) string {
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
	if status = strings.TrimSpace(status); status != "" {
		statusRows := m.renderDimmedPaneStatus(threadID, status, width, maxStatusRows)
		if statusRows != "" {
			if content != "" {
				content += "\n"
			}
			content += statusRows
		}
	}
	return content
}

// paneStatusWhenUnfocused returns a thread's status only when its pane is not
// focused; the focused status is already painted below the composer.
func (m *Model) paneStatusWhenUnfocused(unfocused bool, threadID string) string {
	if !unfocused {
		return ""
	}
	return m.paneStatusForThread(threadID)
}

func (m *Model) paneStatusForThread(threadID string) string {
	if m == nil {
		return ""
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return ""
	}
	return strings.TrimSpace(m.paneStatusText[threadID])
}

// renderDimmedPaneStatus wraps an inactive pane's status to the pane width and
// dims it, mirroring Rust #61141's unfocused treatment (no hyperlinks).
func (m *Model) renderDimmedPaneStatus(threadID string, status string, width int, maxRows int) string {
	status = strings.TrimSpace(status)
	if status == "" || width <= 0 {
		return ""
	}
	// A themed line that already fits keeps its colours; wider lines fall back
	// to the plain rewrap below (Rust #61141 keeps structured content).
	if m != nil {
		if styled := strings.TrimSpace(m.paneStatusStyled[strings.TrimSpace(threadID)]); styled != "" &&
			codextui.DisplayWidth(styled) <= width {
			return "\x1b[2m" + styled + "\x1b[0m"
		}
	}
	rows := codextui.WrapLine(status, codextui.WrapOptions{Width: width, BreakWords: true})
	if maxRows <= 0 {
		maxRows = 3
	}
	if len(rows) > maxRows {
		rows = rows[:maxRows]
	}
	for i, row := range rows {
		rows[i] = "\x1b[2m" + codextui.TruncateWithEllipsis(row, width) + "\x1b[0m"
	}
	return strings.Join(rows, "\n")
}

// paneStatusRowBudget mirrors Rust #61141's shared row allocation: a taller
// pane gives the status more rows, but the transcript always keeps at least one.
func paneStatusRowBudget(height int) int {
	if height <= 0 {
		return 1
	}
	budget := height / 3
	if budget < 1 {
		budget = 1
	}
	if budget > 4 {
		budget = 4
	}
	if limit := height - 1; budget > limit && limit >= 1 {
		budget = limit
	}
	return budget
}

// joinSidePanes draws each pane window, pads the left column to the divider,
// and renders exactly `height` rows. first is the index of the first painted
// row, or sidePaneTail to keep the pane pinned to its latest output. The
// returned maxes bound later wheel scrolling for that pane.
func joinSidePanes(left string, right string, leftWidth int, height int, leftFirst int, rightFirst int) (string, int, int, int, int) {
	if height <= 0 {
		height = 1
	}
	leftLines, leftClamped, leftMax := sidePaneWindow(left, height, leftFirst)
	rightLines, rightClamped, rightMax := sidePaneWindow(right, height, rightFirst)
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
	return strings.Join(rows, "\n"), leftClamped, rightClamped, leftMax, rightMax
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

// sidePaneTail keeps an untouched pane pinned to its latest output, matching
// the tail view the split layout used before per-pane scrolling (Rust #59527).
const sidePaneTail = -1

// sidePaneWindow returns the visible rows of one pane plus the clamped
// first-row index and the largest index this content allows. Wheel-up lowers
// the first row; sidePaneTail follows the newest rows.
func sidePaneWindow(content string, limit int, first int) ([]string, int, int) {
	if limit <= 0 || content == "" {
		return nil, 0, 0
	}
	lines := strings.Split(content, "\n")
	maxFirst := max(len(lines)-limit, 0)
	if first == sidePaneTail {
		first = maxFirst
	}
	first = min(max(first, 0), maxFirst)
	end := min(first+limit, len(lines))
	return lines[first:end], first, maxFirst
}
