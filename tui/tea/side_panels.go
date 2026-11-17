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
// With the default-enabled `tui_workspace` feature, a side conversation is
// shown beside its parent instead of replacing it. Only the focused
// conversation owns the composer, so drawing both transcripts cannot change
// where input goes.

const (
	sideWorkspaceFeatureKey = "tui_workspace"
	// sideWorkspaceMinWidth mirrors Rust's 100-column threshold: narrower
	// terminals fall back to the single focused transcript.
	sideWorkspaceMinWidth = 100
	// Rust side_pane.rs GAP: the panes are separated by a single column and the
	// divider glyph is painted in that column (paint_divider), so the side pane
	// starts immediately after it instead of behind padding columns.
	sideWorkspaceDivider      = "│"
	sideWorkspaceDividerWidth = 1
	// Rust #59595 keeps the sidebar between 36 columns and the remaining space
	// after reserving 64 columns for the parent transcript plus the gap.
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
// columns and leave the parent at least 64 columns plus the one-column gap when
// the terminal allows it.
func sidebarColumns(width int, total int) int {
	maxSide := max(total-sideWorkspaceDividerWidth-sideWorkspaceMinParentWidth, sideWorkspaceMinSideWidth)
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
	available := width - sideWorkspaceDividerWidth
	if available < 2 {
		return 0, 0, false
	}
	percent = clampModalInt(percent, 1, 99)
	// Rust preferred_width takes the saved percentage of the whole terminal and
	// then clamps it (side_pane.rs preferred_width/sidebar_width).
	sideWidth := sidebarColumns((width*percent+50)/100, width)
	if exact.valid && exact.total == width && widthPercent(exact.width, width) == percent {
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
	document := m.sidebarDocumentVisible()
	if !m.sideWorkspaceEnabled() || (m.activeSide == nil && !chooser && !document) {
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
	if document {
		return leftWidth, rightWidth, m.sidebarChooserFocused, true
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

// renderSideWorkspace draws the parent and side conversations side by side.
// Each column owns its bottom pane, so the focused conversation keeps its
// composer, status line and footer inside its own width while the inactive
// column paints its retained transcript, status and draft preview (Rust
// #58992/#59527/#61141). It returns false when the split layout does not
// apply, leaving callers on the ordinary single-conversation path.
func (m *Model) renderSideWorkspace(width int, height int) (string, bool) {
	if m == nil || m.sidePaneRendering || m.State == nil {
		return "", false
	}
	leftWidth, rightWidth, sideFocused, ok := m.sidePaneLayout(width)
	if !ok {
		// Rust #59595 discards a resize preview when the split layout is no
		// longer available.
		m.sideResize = nil
		return "", false
	}
	height = max(height, 1)
	resizing := m.sideResize != nil
	if resizing && height > 1 {
		height--
	}
	if m.sidebarDocumentVisible() {
		// A preview owns the sidebar exactly like the chooser does: the parent
		// conversation keeps the left column and the preview paints on the
		// right (Rust sidebar::SidebarContent::Document).
		left := m.renderFocusedPane(leftWidth, height)
		right := strings.Split(m.renderSidebarDocument(rightWidth, height), "\n")
		for index := range right {
			right[index] = padSidePaneLine(codextui.TruncateToWidth(right[index], rightWidth), rightWidth)
		}
		m.sidebarTabStripTop = 0
		rendered := m.zipSidePanes(left, right, leftWidth, height)
		if resizing {
			rendered += "\n" + m.sideResizeHint(rightWidth)
		}
		return padSideWorkspaceRows(rendered, width), true
	}
	if m.sidebarChooserVisible() {
		// The chooser belongs to the parent conversation, so the live
		// conversation keeps the left column while the list paints on the
		// right (Rust #59739).
		left := m.renderFocusedPane(leftWidth, height)
		right := strings.Split(m.renderSidebarChooser(rightWidth, height), "\n")
		for index := range right {
			right[index] = padSidePaneLine(codextui.TruncateToWidth(right[index], rightWidth), rightWidth)
		}
		// Rust #59928 paints the strip on the pane's first row; record which
		// screen row that is so the pointer hit test follows the painted
		// origin.
		if m.sidebarTabStripVisible() {
			m.sidebarTabStripTop = 0
		}
		rendered := m.zipSidePanes(left, right, leftWidth, height)
		if resizing {
			rendered += "\n" + m.sideResizeHint(rightWidth)
		}
		return padSideWorkspaceRows(rendered, width), true
	}
	side := m.activeSide
	if side == nil {
		return "", false
	}
	sideFocused = side.ShowingSide
	// Rust #59784/#59928 paints the sibling tabs on the side pane's first row.
	// The strip costs the right column one row, whichever side is focused.
	strip := m.sidebarTabStripVisible()
	rightHeight := height
	if strip && rightHeight > 1 {
		rightHeight--
	}
	var leftLines, rightLines []string
	if sideFocused {
		// The focused pane owns the live transcript, so its parked copy never
		// carries unseen activity (Rust clears the flag once the tail shows).
		side.SideUnseen = false
		var parentFirst int
		leftLines, parentFirst, side.ParentViewMax = m.renderPassiveSidePane(side, false, leftWidth, height)
		rightLines = m.renderFocusedPane(rightWidth, rightHeight)
		// The live conversation is painted in the right column, below the
		// pane's tab strip when it owns one.
		m.offsetFocusedPanePointer(leftWidth+ansi.StringWidth(sideWorkspaceDivider), 0)
		if strip {
			m.offsetFocusedPanePointer(0, 1)
		}
		// The inactive pane stays parked at the rows it painted; the focused
		// pane follows its transcript viewport (Rust #59527).
		side.ParentScroll = parentFirst
		side.SideScroll = m.transcript.YOffset
	} else {
		side.ParentUnseen = false
		var sideFirst int
		leftLines = m.renderFocusedPane(leftWidth, height)
		rightLines, sideFirst, side.SideViewMax = m.renderPassiveSidePane(side, true, rightWidth, rightHeight)
		side.SideScroll = sideFirst
		side.ParentScroll = m.transcript.YOffset
	}
	if strip {
		stripRow := padSidePaneLine(m.renderSidebarTabStrip(rightWidth), rightWidth)
		rightLines = append([]string{stripRow}, rightLines...)
		m.sidebarTabStripTop = 0
	}
	rendered := m.zipSidePanes(leftLines, rightLines, leftWidth, height)
	if resizing {
		rendered += "\n" + m.sideResizeHint(rightWidth)
	}
	return padSideWorkspaceRows(rendered, width), true
}

// padSideWorkspaceRows pads every split row to the terminal width. The panes
// are returned as the whole frame, so nothing else pads their right edge.
func padSideWorkspaceRows(block string, width int) string {
	rows := strings.Split(block, "\n")
	for index := range rows {
		rows[index] = padSidePaneLine(rows[index], width)
	}
	return strings.Join(rows, "\n")
}

// sideWorkspaceHeight is the frame height the split columns may claim. Inline
// mode keeps one row free so the frame cannot scroll the terminal, matching
// transcriptHeightForLayout.
func (m *Model) sideWorkspaceHeight() int {
	height := firstPositive(m.height, defaultHeight)
	if !m.ownedScreen && height > 1 {
		height--
	}
	return height
}

// renderFocusedPane paints the live conversation inside one column. The frame
// is rebuilt at the pane size so its composer, status line, footer and any
// inline picker stay inside the pane like Rust's per-pane bottom pane.
func (m *Model) renderFocusedPane(width int, height int) (lines []string) {
	width = max(width, 1)
	height = max(height, 1)
	previousWidth := firstPositive(m.width, defaultWidth)
	previousHeight := firstPositive(m.height, defaultHeight)
	m.sidePaneRendering = true
	defer func() {
		m.sidePaneRendering = false
		m.applyLayoutSize(previousWidth, previousHeight)
	}()
	m.applyLayoutSize(width, height)
	body := m.viewString()
	// The focused transcript viewport is the pane's, not the full frame's, so
	// pointer bounds keep matching the painted rows after the size is restored.
	focusedTranscriptHeight := m.transcript.Height
	m.applyLayoutSize(previousWidth, previousHeight)
	m.transcript.Height = focusedTranscriptHeight
	lines = strings.Split(body, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for index := range lines {
		// JoinVertical pads every row to the widest section; a long status or
		// notice row must not widen the pane past its column (Rust clips each
		// pane to its rect).
		lines[index] = padSidePaneLine(codextui.TruncateToWidth(lines[index], width), width)
	}
	blank := strings.Repeat(" ", width)
	for len(lines) < height {
		lines = append(lines, blank)
	}
	return lines
}

// renderPassiveSidePane paints one column of a split where that conversation
// no longer owns the composer: the windowed transcript, the dimmed status, the
// parked draft preview, and the blank hint row (Rust #61141).
func (m *Model) renderPassiveSidePane(side *activeSideConversation, sidePassive bool, width int, height int) ([]string, int, int) {
	threadID := side.ParentThreadID
	messages := side.ParentMessages
	draft := side.ParentDraft
	first := sidePaneTail
	if side.ParentInteractionSaved {
		first = side.ParentScroll
	}
	if sidePassive {
		threadID = side.SideThreadID
		messages = side.SideMessages
		draft = side.SideDraft
		first = sidePaneTail
		if side.SideInteractionSaved {
			first = side.SideScroll
		}
	}
	cache := &transcriptMessageCache{}
	content := m.renderSidePaneContent(cache, threadID, messages, width)
	// Rust TranscriptView tracks unseen activity per pane: output that changes
	// the parked column's tail while the reader looks elsewhere raises the
	// "New activity" affordance, which clears once the tail paints again.
	unseen := &side.ParentUnseen
	tail := &side.ParentTail
	if sidePassive {
		unseen = &side.SideUnseen
		tail = &side.SideTail
	}
	signature := len(messages)*1_000_003 + len(content)
	if *tail != 0 && *tail != signature {
		*unseen = true
	}
	*tail = signature
	rows, clamped, maxFirst := m.composeSidePane(threadID, content, m.paneStatusForThread(threadID), draft, width, height, first, *unseen)
	if clamped == maxFirst {
		*unseen = false
	}
	for index := range rows {
		// A pane must never paint past the divider, even when a transcript row
		// (a table or an unbreakable token) is wider than its column.
		rows[index] = padSidePaneLine(codextui.TruncateToWidth(rows[index], width), width)
	}
	return rows, clamped, maxFirst
}

// offsetFocusedPanePointer moves the focused conversation's recorded pointer
// rows, selection regions and caret into split-frame coordinates.
func (m *Model) offsetFocusedPanePointer(x int, y int) {
	if m == nil || (x == 0 && y == 0) {
		return
	}
	m.offsetConversationPointerRows(x, y)
	if m.transcriptSelectionRegion.h > 0 {
		m.transcriptSelectionRegion.x += x
		m.transcriptSelectionRegion.y += y
	}
	if m.transcriptSelection.active {
		m.transcriptSelection.anchorX += x
		m.transcriptSelection.anchorY += y
		m.transcriptSelection.endX += x
		m.transcriptSelection.endY += y
	}
	for row, hit := range m.transcriptDisclosureHits {
		hit.start += x
		hit.end += x
		m.transcriptDisclosureHits[row] = hit
	}
}

// sideResizeHint mirrors the resize-mode keyboard hints Rust shows while the
// divider preview is active.
func (m *Model) sideResizeHint(sideWidth int) string {
	percent := widthPercent(sideWidth, max(m.width, 1))
	hint := "Resize panes \u00b7 " + strconv.Itoa(sideWidth) + " cols (" + strconv.Itoa(percent) + "%) \u00b7 "
	hint += "\u2190/\u2192 or h/l move \u00b7 shift+\u2190/\u2192 10 cols \u00b7 enter/esc save"
	return codextui.TruncateWithEllipsis(hint, max(m.width, 1))
}

// renderSidePaneContent renders one pane's transcript without its retained
// status or draft; the pane composition paints those as fixed bottom rows
// (Rust #61141 keeps them out of the scrolling transcript).
func (m *Model) renderSidePaneContent(cache *transcriptMessageCache, threadID string, messages []codextui.Message, width int) string {
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
	if status == "" || width <= 0 || maxRows <= 0 {
		return ""
	}
	// Structured status content reflows with its colours intact (Rust #61141).
	if m != nil {
		if spans := m.paneStatusSpans[strings.TrimSpace(threadID)]; len(spans) > 0 {
			rows := wrapPaneStatusSpans(spans, width)
			if len(rows) > maxRows {
				rows = rows[:maxRows]
			}
			if len(rows) > 0 {
				return strings.Join(rows, "\n")
			}
		}
		if styled := strings.TrimSpace(m.paneStatusStyled[strings.TrimSpace(threadID)]); styled != "" &&
			codextui.DisplayWidth(styled) <= width {
			return "\x1b[2m" + styled + "\x1b[0m"
		}
	}
	rows := codextui.WrapLine(status, codextui.WrapOptions{Width: width, BreakWords: true})
	if len(rows) > maxRows {
		rows = rows[:maxRows]
	}
	for i, row := range rows {
		rows[i] = "\x1b[2m" + codextui.TruncateWithEllipsis(row, width) + "\x1b[0m"
	}
	return strings.Join(rows, "\n")
}

// sidePaneComposerHeightLimit mirrors Rust #61141's composer_height_limit: the
// inactive composer may claim up to two thirds of the pane, or eight rows on
// short panes when they fit.
func sidePaneComposerHeightLimit(height int) int {
	if height <= 0 {
		return 0
	}
	limit := height * 2 / 3
	if limit < 8 {
		limit = 8
	}
	if limit > height {
		limit = height
	}
	return limit
}

// sidePaneFooterCap mirrors the footer cap Rust derives from the composer
// limit: clamp((limit+2)/4, 2, 4).
func sidePaneFooterCap(height int) int {
	cap := (sidePaneComposerHeightLimit(height) + 2) / 4
	if cap < 2 {
		cap = 2
	}
	if cap > 4 {
		cap = 4
	}
	return cap
}

// paneStatusRowBudget mirrors Rust #61141's FooterLayout allocation for an
// unfocused side pane. The composer keeps its rows and one hint row stays
// reserved, so the status may use whatever the footer cap leaves; a pane with
// no room reports zero rows instead of squeezing the transcript out.
func paneStatusRowBudget(height int) int {
	if height <= 0 {
		return 0
	}
	composer := min(max(height-1, 0), 3)
	available := max(height-composer, 0)
	cap := min(sidePaneFooterCap(height), available)
	status := cap - 1
	if status < 0 {
		status = 0
	}
	return status
}

// sidePaneFrame is the fixed row allocation of one side pane: the scrolling
// transcript keeps the rest, and the bottom rows hold the retained status, the
// inactive composer preview, and a blank hint row (Rust #61141).
type sidePaneFrame struct {
	transcript int
	status     int
	composer   int
	hint       int
}

func sidePaneFrameFor(height int) sidePaneFrame {
	if height <= 0 {
		return sidePaneFrame{transcript: 1}
	}
	status := paneStatusRowBudget(height)
	composer := min(max(height-1, 0), 3)
	hint := 0
	if height-status-composer >= 1 {
		hint = 1
	}
	// Rust leaves one separator row between the transcript and the footer.
	transcript := height - status - composer - hint - 1
	for transcript < 1 && composer > 0 {
		composer--
		transcript = height - status - composer - hint - 1
	}
	if transcript < 1 && hint > 0 {
		hint = 0
		transcript = height - status - composer - hint - 1
	}
	if transcript < 1 {
		transcript = 1
	}
	return sidePaneFrame{transcript: transcript, status: status, composer: composer, hint: hint}
}

// composeSidePane builds exactly `height` rows: the windowed transcript, the
// dimmed retained status, the inactive draft preview, and a blank hint row.
func (m *Model) composeSidePane(threadID string, content string, status string, draft string, width int, height int, first int, unseen bool) ([]string, int, int) {
	frame := sidePaneFrameFor(height)
	lines, clamped, maxFirst := sidePaneWindow(content, frame.transcript, first)
	rows := make([]string, 0, height)
	rows = append(rows, lines...)
	for len(rows) < frame.transcript {
		rows = append(rows, "")
	}
	// Rust paints the inactive column bottom-up like the live one: one blank
	// row, the composer (draft preview), the dimmed retained status, then the
	// blank hint row.
	// The row between transcript and composer carries the return-to-latest
	// affordance while the parked view is above its tail (Rust
	// transcript_view::follow_control, painted in the composer gap).
	follow := ""
	if clamped < maxFirst {
		follow = centerSidePaneFollow(sidePaneFollowLabel(unseen, width), width)
	}
	rows = append(rows, follow)
	rows = append(rows, m.renderDimmedPaneDraft(draft, width, frame.composer)...)
	for len(rows) < frame.transcript+1+frame.composer {
		rows = append(rows, "")
	}
	if frame.status > 0 {
		if statusRows := m.renderDimmedPaneStatus(threadID, status, width, frame.status); statusRows != "" {
			rows = append(rows, strings.Split(statusRows, "\n")...)
		}
	}
	for len(rows) < height {
		rows = append(rows, "")
	}
	if len(rows) > height {
		rows = rows[:height]
	}
	return rows, clamped, maxFirst
}

// sidePaneFollowLabel mirrors Rust's TranscriptView::follow_control_label for
// a parked pane: the same ladder, with the "New activity" prefix once output
// arrived while the reader was elsewhere.
func sidePaneFollowLabel(unseen bool, width int) string {
	if width <= 0 {
		return ""
	}
	labels := []string{
		" \u2193 Back to bottom \u00b7 esc ",
		" \u2193 Back to bottom ",
		" \u2193 Bottom \u00b7 esc ",
		" \u2193 Bottom ",
		" \u2193 ",
	}
	if unseen {
		labels = []string{
			" New activity \u00b7 \u2193 Back to bottom \u00b7 esc ",
			" New activity \u00b7 \u2193 Bottom ",
			" New \u00b7 \u2193 Bottom ",
			" \u2193 Bottom ",
			" \u2193 ",
		}
	}
	for _, label := range labels {
		if codextui.DisplayWidth(label) <= width {
			return label
		}
	}
	return ""
}

// centerSidePaneFollow centers one-row affordances the way Rust's
// render_follow_control places its target rect inside the gap row.
func centerSidePaneFollow(label string, width int) string {
	if label == "" || width <= 0 {
		return ""
	}
	room := width - codextui.DisplayWidth(label)
	if room < 0 {
		return ""
	}
	left := room / 2
	return strings.Repeat(" ", left) + label + strings.Repeat(" ", room-left)
}

// renderDimmedPaneDraft previews the inactive conversation's draft inside the
// rows reserved for its composer.
func (m *Model) renderDimmedPaneDraft(draft string, width int, maxRows int) []string {
	draft = strings.TrimRight(draft, "\r\n")
	if strings.TrimSpace(draft) == "" || width <= 2 || maxRows <= 0 {
		return nil
	}
	inner := max(width-2, 1)
	wrapped := make([]string, 0, maxRows)
	for _, line := range strings.Split(draft, "\n") {
		wrapped = append(wrapped, codextui.WrapLine(line, codextui.WrapOptions{Width: inner, BreakWords: true})...)
		if len(wrapped) >= maxRows {
			break
		}
	}
	if len(wrapped) > maxRows {
		wrapped = wrapped[:maxRows]
	}
	rows := make([]string, 0, len(wrapped))
	for index, row := range wrapped {
		prefix := "  "
		if index == 0 {
			prefix = "\u203a "
		}
		rows = append(rows, "\x1b[2m"+prefix+codextui.TruncateWithEllipsis(row, inner)+"\x1b[0m")
	}
	return rows
}

// zipSidePanes joins two already-composed panes row by row, padding the left
// column to the divider.
func (m *Model) zipSidePanes(left []string, right []string, leftWidth int, height int) string {
	if height <= 0 {
		return ""
	}
	divider := m.sideDividerCell()
	rows := make([]string, 0, height)
	for index := 0; index < height; index++ {
		line := ""
		if index < len(left) {
			line = left[index]
		}
		content := ""
		if index < len(right) {
			content = right[index]
		}
		rows = append(rows, padSidePaneLine(line, leftWidth)+divider+content)
	}
	return strings.Join(rows, "\n")
}

// sideDividerCell paints the divider the way Rust side_pane::paint_divider does:
// dim unless the pointer hovers it or a width preview (drag or keyboard resize)
// is active, which switches it to the theme accent.
func (m *Model) sideDividerCell() string {
	if m != nil && (m.sideDividerHover || m.sideResize != nil || m.sideDrag != nil) {
		return transcriptDisclosureAccentSGR() + sideWorkspaceDivider + "\x1b[0m"
	}
	return "\x1b[2m" + sideWorkspaceDivider + "\x1b[22m"
}

// prependSidePaneRow paints the sibling tab strip above the side transcript,
// matching Rust #59784's placement. The pane keeps a fixed height, so the
// added row drops the last one.
func prependSidePaneRow(lines []string, row string) []string {
	out := make([]string, 0, len(lines)+1)
	out = append(out, row)
	return append(out, lines...)
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
