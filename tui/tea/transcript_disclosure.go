package tea

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	"codex_go/utils"
)

// Rust parity: transcript_view/disclosure.rs and activity_layout. Identified
// activity cells expose one inline control that expands or hides their retained
// details. The control is a presentation row, not searchable source text.

const (
	transcriptShowDetails = "+ Show details"
	transcriptHideDetails = "\u2212 Hide details"
	transcriptShowLess    = "\u2212 Show less"
)

var transcriptOutputMarkerPattern = regexp.MustCompile(`^\s*(?:…\s*)?\+\s*(\d+)\s+lines?(?:\s.*)?$`)

type transcriptDisclosureHit struct {
	message int
	start   int
	end     int
}

// transcriptMessageDisclosure resolves the inline control for an identified
// activity. Cell metadata is authoritative; the compact marker and transcript
// variant remain fallbacks for states assembled without it.
func transcriptMessageDisclosure(message codextui.Message) (codextui.ActivityDisclosure, bool) {
	if len(normalizeActivityIDs(message.ActivityIDs)) == 0 {
		return codextui.ActivityDisclosure{}, false
	}
	if message.ActivityDisclosureKnown {
		disclosure := codextui.ActivityDisclosure{
			HiddenLines: message.ActivityHiddenLines,
			Generic:     message.ActivityGenericDetails,
		}
		return disclosure, disclosure.Visible()
	}
	if message.ActivityHiddenLines > 0 {
		return codextui.ActivityDisclosure{HiddenLines: message.ActivityHiddenLines}, true
	}
	if message.ActivityGenericDetails {
		return codextui.ActivityDisclosure{Generic: true}, true
	}
	compact := rawLinesTrimmed(message.Text)
	if hidden, _, found := stripTranscriptOutputMarker(compact); found {
		return codextui.ActivityDisclosure{HiddenLines: hidden}, true
	}
	if strings.TrimSpace(message.TranscriptText) != "" ||
		strings.TrimSpace(message.RawText) != strings.TrimSpace(message.Text) {
		return codextui.ActivityDisclosure{Generic: true}, true
	}
	return codextui.ActivityDisclosure{}, false
}

// transcriptMessageDisclosureFor resolves the control for a message in its
// current presentation. Rust's activity_layout keeps the control for every
// expanded activity, even when the cell reports no retained details, so the
// pointer can collapse it again.
func transcriptMessageDisclosureFor(message codextui.Message, expanded bool) (codextui.ActivityDisclosure, bool) {
	if disclosure, ok := transcriptMessageDisclosure(message); ok {
		return disclosure, true
	}
	if expanded && len(normalizeActivityIDs(message.ActivityIDs)) > 0 {
		return codextui.ActivityDisclosure{Generic: true}, true
	}
	return codextui.ActivityDisclosure{}, false
}

// stripTranscriptOutputMarker removes the legacy compact-preview marker row and
// reports its hidden line count. Rust's owned transcript replaces that row with a
// disclosure control.
func stripTranscriptOutputMarker(lines []string) (int, []string, bool) {
	for index := len(lines) - 1; index >= 0; index-- {
		if strings.TrimSpace(utils.StripANSI(lines[index])) == "" {
			continue
		}
		match := transcriptOutputMarkerPattern.FindStringSubmatch(utils.StripANSI(lines[index]))
		if match == nil {
			return 0, lines, false
		}
		hidden, err := strconv.Atoi(match[1])
		if err != nil || hidden <= 0 {
			return 0, lines, false
		}
		out := append([]string(nil), lines[:index]...)
		out = append(out, lines[index+1:]...)
		return hidden, out, true
	}
	return 0, lines, false
}

func transcriptDisclosureIndent(width int) int {
	if width <= 0 {
		return 0
	}
	return min(width/4, 4)
}

func transcriptDisclosureControlLine(disclosure codextui.ActivityDisclosure, expanded bool, width int) string {
	label := transcriptShowDetails
	switch {
	case expanded && disclosure.HiddenLines > 0:
		label = transcriptShowLess
	case expanded:
		label = transcriptHideDetails
	case disclosure.HiddenLines > 0:
		noun := "lines"
		if disclosure.HiddenLines == 1 {
			noun = "line"
		}
		label = fmt.Sprintf("+ %d %s", disclosure.HiddenLines, noun)
		if hint := codextui.TranscriptExpandHint(); hint != "" {
			withHint := label + " (" + hint + ")"
			if transcriptDisclosureIndent(width)+codextui.DisplayWidth(withHint) <= width {
				label = withHint
			}
		}
	}
	indent := transcriptDisclosureIndent(width)
	// Rust truncates the whole control row to the transcript width without an
	// ellipsis (`truncate_line_to_width`), so a narrow terminal never wraps the
	// control into a second, unhittable row.
	label = codextui.TruncateToWidth(label, max(width-indent, 0))
	return "\x1b[2m" + strings.Repeat(" ", indent) + label + "\x1b[0m"
}

func isTranscriptDisclosureControlLine(line string) bool {
	trimmed := strings.TrimSpace(utils.StripANSI(line))
	if trimmed == transcriptShowDetails || trimmed == transcriptHideDetails || trimmed == transcriptShowLess {
		return true
	}
	return transcriptOutputMarkerPattern.MatchString(trimmed) && strings.HasPrefix(trimmed, "+")
}

// recordTranscriptDisclosureHits maps painted content rows to their activity
// control. Ranges come from the same layout pass that produced content.
func (m *Model) recordTranscriptDisclosureHits(state *codextui.State, content string, ranges [][2]int) {
	if m == nil {
		return
	}
	m.transcriptMessageRanges = ranges
	m.transcriptDisclosureHits = nil
	if state == nil || !m.transcriptDisclosureControlsEnabled() || len(ranges) == 0 {
		return
	}
	lines := strings.Split(content, "\n")
	for index := range state.Messages {
		if index >= len(ranges) {
			break
		}
		if _, ok := transcriptMessageDisclosureFor(state.Messages[index], m.transcriptActivityExpanded(index)); !ok {
			continue
		}
		start, end := ranges[index][0], min(ranges[index][1], len(lines))
		if start < 0 || start >= end {
			continue
		}
		controlRow := -1
		for row := end - 1; row >= start; row-- {
			if strings.TrimSpace(utils.StripANSI(lines[row])) == "" {
				continue
			}
			if isTranscriptDisclosureControlLine(lines[row]) {
				controlRow = row
			}
			break
		}
		if controlRow < 0 {
			continue
		}
		plain := utils.StripANSI(lines[controlRow])
		first := len(plain) - len(strings.TrimLeft(plain, " "))
		width := codextui.DisplayWidth(plain)
		if width <= first {
			continue
		}
		if m.transcriptDisclosureHits == nil {
			m.transcriptDisclosureHits = map[int]transcriptDisclosureHit{}
		}
		m.transcriptDisclosureHits[controlRow] = transcriptDisclosureHit{
			message: index,
			start:   first,
			end:     width,
		}
	}
}

// transcriptDisclosureLayoutEnabled reports whether the transcript paints the
// inline activity controls. Rust renders them only in the owned Rich transcript,
// not in raw output or the detailed pager overlay.
func (m *Model) transcriptDisclosureLayoutEnabled() bool {
	return m != nil && m.ownedScreen && !m.rawOutput
}

// transcriptDisclosureControlsEnabled additionally requires that no competing
// transcript mode owns the pointer.
func (m *Model) transcriptDisclosureControlsEnabled() bool {
	return m.transcriptDisclosureLayoutEnabled() && !m.mainFind.active()
}

// transcriptMouseContentRow converts a terminal mouse row into the transcript
// content row painted there (Rust TranscriptView::area).
func (m *Model) transcriptMouseContentRow(msg bubbletea.MouseMsg) (int, bool) {
	if m == nil || m.transcriptContentTopRow < 0 || m.transcript.Height <= 0 {
		return 0, false
	}
	visibleRow := msg.Y - m.transcriptContentTopRow
	if visibleRow < 0 || visibleRow >= m.transcript.Height {
		return 0, false
	}
	return m.transcript.YOffset + visibleRow, true
}

// handleTranscriptDisclosureClick toggles the activity whose painted control
// row was clicked. Rust requires an unmodified left-button press.
func (m *Model) handleTranscriptDisclosureClick(msg bubbletea.MouseMsg) bool {
	if m == nil || msg.Action != bubbletea.MouseActionPress || msg.Button != bubbletea.MouseButtonLeft ||
		msg.Ctrl || msg.Alt || msg.Shift {
		return false
	}
	if m.overlay != nil || !m.transcriptDisclosureControlsEnabled() {
		return false
	}
	contentRow, ok := m.transcriptMouseContentRow(msg)
	if !ok {
		return false
	}
	hit, ok := m.transcriptDisclosureHits[contentRow]
	if !ok || msg.X < hit.start || msg.X >= hit.end {
		return false
	}
	// Rust #58281: expanding details during a patch approval anchors the entry
	// for inspection and resumes following the latest output once that prompt
	// closes or advances to another patch. Manual scrolling cancels the
	// pending resume instead.
	approvalID := m.currentPatchApprovalID()
	wasFollowing := m.activityFollow || m.transcript.AtBottom()
	if wasFollowing && approvalID != "" {
		m.activityFollow = false
		m.resumeFollowAfterApproval = approvalID
	}
	return m.toggleTranscriptActivityMessage(hit.message)
}

// currentPatchApprovalID identifies the open apply-patch approval prompt. Rust's
// sync_patch_approval compares this value across events.
func (m *Model) currentPatchApprovalID() string {
	if m == nil || m.modal == nil || m.modal.kind != ModalKindApproval {
		return ""
	}
	return strings.TrimSpace(m.modal.id)
}

// syncPatchApprovalFollow resumes the latest output when the approval that was
// open during an inline expansion ends or advances to another patch
// (Rust #58281 TranscriptView::sync_patch_approval).
func (m *Model) syncPatchApprovalFollow() {
	if m == nil || m.resumeFollowAfterApproval == "" {
		return
	}
	if m.currentPatchApprovalID() == m.resumeFollowAfterApproval {
		return
	}
	m.resumeFollowAfterApproval = ""
	m.renderedSelection.clear()
	m.clearTranscriptActivityFocus()
	m.activityFollow = true
	m.refreshTranscript()
	m.transcript.GotoBottom()
}

// dismissTranscriptActivityFocusOnClick mirrors Rust pointer_down: a left press
// that misses the disclosure clears focus before selection starts. The explicit
// expansion stays keyed by activity IDs, so details remain visible.
func (m *Model) dismissTranscriptActivityFocusOnClick(msg bubbletea.MouseMsg) bool {
	if m == nil || !m.activityFocus.active ||
		msg.Action != bubbletea.MouseActionPress || msg.Button != bubbletea.MouseButtonLeft {
		return false
	}
	if m.overlay != nil || !m.transcriptDisclosureLayoutEnabled() {
		return false
	}
	if _, ok := m.transcriptMouseContentRow(msg); !ok {
		return false
	}
	m.clearTranscriptActivityFocus()
	return true
}

// styleFocusedTranscriptActivity paints Rust's column-scoped disclosure styles.
// The control row uses the accent foreground, bold, and no dim; the activity's
// header row is underlined across the transcript width.
func styleFocusedTranscriptActivity(content string, ranges [][2]int, index int, width int) string {
	if index < 0 || index >= len(ranges) {
		return content
	}
	start, end := ranges[index][0], ranges[index][1]
	lines := strings.Split(content, "\n")
	if start < 0 || start >= end || start >= len(lines) {
		return content
	}
	end = min(end, len(lines))
	header := -1
	for row := start; row < end; row++ {
		if strings.TrimSpace(utils.StripANSI(lines[row])) != "" {
			header = row
			break
		}
	}
	if header >= 0 {
		lines[header] = underlineTranscriptLine(lines[header], width)
	}
	for row := start; row < end; row++ {
		if isTranscriptDisclosureControlLine(lines[row]) {
			lines[row] = styleTranscriptDisclosureControlLine(lines[row])
		}
	}
	return strings.Join(lines, "\n")
}

// styleTranscriptDisclosureControlLine removes the preview row's dim style and
// applies Rust render_disclosure's accent/bold foreground to the label columns.
func styleTranscriptDisclosureControlLine(line string) string {
	plain := utils.StripANSI(line)
	indent := len(plain) - len(strings.TrimLeft(plain, " "))
	if indent >= len(plain) {
		return line
	}
	return strings.Repeat(" ", indent) + transcriptDisclosureAccentSGR() + plain[indent:] + "\x1b[0m"
}

// underlineTranscriptLine applies Rust's full-row activity-header underline,
// preserving it across resets emitted by nested styled spans.
func underlineTranscriptLine(line string, width int) string {
	const (
		on  = "\x1b[4m"
		off = "\x1b[24m"
	)
	if plainWidth := codextui.DisplayWidth(utils.StripANSI(line)); width > plainWidth {
		line += strings.Repeat(" ", width-plainWidth)
	}
	line = strings.ReplaceAll(line, "\x1b[0m", "\x1b[0m"+on)
	line = strings.ReplaceAll(line, "\x1b[m", "\x1b[m"+on)
	return on + line + off
}

func transcriptDisclosureAccentSGR() string {
	level := codextui.DetectStdoutColorLevel()
	var background *codextui.RGB
	if rgb, ok := codextui.DefaultBackgroundRGB(); ok {
		background = &rgb
	}
	if sgr := codextui.SGRForStyle(codextui.AccentStyleFor(background, level), level); sgr != "" {
		return sgr
	}
	return "\x1b[1m"
}

// toggleTranscriptActivityMessage follows a click into activity focus: the
// clicked group becomes selected, its details toggle, and the previous top
// entry stays anchored (Rust toggle_activity).
func (m *Model) toggleTranscriptActivityMessage(index int) bool {
	if m == nil || !m.transcriptActivityExpandable(index) {
		return false
	}
	return m.setTranscriptActivityExpandedAt(index, !m.transcriptActivityExpanded(index))
}

// setTranscriptActivityExpandedAt applies one activity group's disclosure
// state. The entry that owned the previous top row stays anchored; toggling
// that entry itself returns to its header (Rust toggle_activity).
func (m *Model) setTranscriptActivityExpandedAt(index int, expanded bool) bool {
	if m == nil || !m.transcriptActivityExpandable(index) {
		return false
	}
	if m.mainFind.active() {
		m.cancelMainTranscriptFind()
	}
	m.renderedSelection.clear()
	if !m.activityFocus.active {
		m.activityFocus.active = true
		m.activityFocus.savedOffset = m.transcript.YOffset
		m.activityFocus.savedFollow = m.activityFollow
	}
	topIndex, topOffset, haveTop := transcriptEntryAtLine(m.transcriptMessageRanges, m.transcript.YOffset)
	if m.activityFocus.expanded == nil {
		m.activityFocus.expanded = map[string]bool{}
	}
	for _, key := range m.transcriptActivityKeys(index) {
		if expanded {
			m.activityFocus.expanded[key] = true
		} else {
			delete(m.activityFocus.expanded, key)
		}
	}
	m.setFocusedTranscriptActivity(index)
	m.refreshTranscript()
	m.restoreTranscriptEntryAnchor(topIndex, topOffset, haveTop, index)
	return true
}

// transcriptDisclosureRows paints the inline control below an activity group.
// Collapsed previews drop the legacy hidden-output marker because the control
// replaces it (Rust activity_layout). Expanded entries keep the full source and
// append the collapse control.
func transcriptDisclosureRows(message codextui.Message, lines []string, expanded bool, width int) ([]string, bool) {
	disclosure, ok := transcriptMessageDisclosureFor(message, expanded)
	if !ok || len(lines) == 0 {
		return lines, false
	}
	rows := lines
	if !expanded {
		if _, stripped, found := stripTranscriptOutputMarker(rows); found {
			rows = stripped
		}
	}
	out := make([]string, 0, len(rows)+1)
	out = append(out, rows...)
	out = append(out, transcriptDisclosureControlLine(disclosure, expanded, width))
	return out, true
}

func transcriptEntryAtLine(ranges [][2]int, line int) (int, int, bool) {
	for index, span := range ranges {
		if span[1] <= span[0] {
			continue
		}
		if line < span[0] {
			return index, 0, true
		}
		if line < span[1] {
			return index, line - span[0], true
		}
	}
	return -1, 0, false
}

// restoreTranscriptEntryAnchor reapplies the entry that owned the previous top
// row. Toggling the top entry itself returns to its header, matching Rust's
// anchor rule for an unchanged cell.
func (m *Model) restoreTranscriptEntryAnchor(topIndex, topOffset int, haveTop bool, toggled int) {
	if m == nil || !haveTop || topIndex < 0 || topIndex >= len(m.transcriptMessageRanges) {
		return
	}
	start, end := m.transcriptMessageRanges[topIndex][0], m.transcriptMessageRanges[topIndex][1]
	if start < 0 || end <= start {
		return
	}
	target := start
	if topIndex != toggled {
		target = start + max(min(topOffset, end-start-1), 0)
	}
	m.transcript.SetYOffset(target)
}
