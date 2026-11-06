package tea

import (
	keymsg "codex_go/tui/keymsg"
	mousemsg "codex_go/tui/mousemsg"
	"strings"

	bubbletea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	codextui "codex_go/tui"
)

// canReturnToLatest mirrors Rust's `TranscriptView::can_return_to_latest`: the
// reader paused away from the tail and the final transcript row is not painted.
func (m *Model) canReturnToLatest() bool {
	if m == nil {
		return false
	}
	return !m.activityFollow && !m.transcript.AtBottom()
}

// enterReturnsToLatest mirrors `App::enter_returns_to_latest`: a bare Enter on
// an empty composer, with no modal, popup, or primed backtrack, jumps back to
// the latest output instead of submitting an empty draft.
func (m *Model) enterReturnsToLatest() bool {
	if m == nil {
		return false
	}
	return m.composerIsEmpty() &&
		m.modal == nil &&
		!m.slashPopup.Active &&
		!m.skillPopup.Active &&
		m.mentionPopup == nil &&
		!m.backtrack.Primed &&
		!m.backtrack.OverlayPreviewActive
}

// jumpToTranscriptLatest mirrors Rust's `TranscriptView::jump_to_latest`: drop
// the reading pause, unseen activity, and any pending expansion follow, then
// pin the viewport to the live tail.
func (m *Model) jumpToTranscriptLatest() {
	if m == nil {
		return
	}
	m.resumeFollowAfterApproval = ""
	m.transcriptUnseenActivity = false
	m.activityFollow = true
	m.followControlHover = false
	m.followControlPressed = false
	m.transcript.GotoBottom()
}

// followControlLabel mirrors Rust's `follow_control_label`: a centered return
// affordance in the composer gap, shortening with the available width. The
// leading and trailing spaces are part of Rust's padded labels.
func (m *Model) followControlLabel(width int) string {
	if m == nil || width <= 0 || m.transcript.Height <= 0 || m.transcript.AtBottom() {
		return ""
	}
	if !m.ownedScreen || m.rawOutput || len(m.transcript.lines) == 0 {
		return ""
	}
	if m.modal != nil || m.overlay != nil || m.shortcutOverlay || m.warningsView != nil {
		return ""
	}
	if m.renderedSelection.active || m.mainFind.active() || m.vimSearchMode || m.activityFocus.active {
		return ""
	}
	labels := []string{
		" \u2193 Back to bottom \u00b7 esc ",
		" \u2193 Back to bottom ",
		" \u2193 Bottom \u00b7 esc ",
		" \u2193 Bottom ",
		" \u2193 ",
	}
	if m.transcriptUnseenActivity {
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

// composerTipLine returns the contextual discovery tip painted in an unused
// composer-gap row (Rust app/composer_hints.rs, #46739). It changes with the
// conversation state, never on a timer, and honors tui.show_tooltips.
func (m *Model) composerTipLine() string {
	if m == nil || !m.showTooltips || !m.composerIsEmpty() || len(m.attachments) > 0 || m.isTaskRunning() {
		return ""
	}
	if m.modal != nil || m.overlay != nil || m.shortcutOverlay || m.warningsView != nil ||
		m.renderedSelection.active || m.mainFind.active() || m.activityFocus.active || m.vimSearchMode {
		return ""
	}
	if m.backtrack.Primed || m.backtrack.OverlayPreviewActive || !m.transcript.AtBottom() {
		return ""
	}
	count := 0
	if m.State != nil {
		for _, message := range m.State.Messages {
			if message.Role == codextui.RoleUser {
				count++
			}
		}
	}
	if count == 0 {
		return "Tip: @ mentions files, skills, and plugins"
	}
	switch count % 3 {
	case 0:
		return "Tip: drag to select text · " + displayKeyBinding("ctrl-c") + " copies"
	case 1:
		bindings, _, _ := codextui.ResolvedKeymapBindings(m.keymapConfig, "global", "find_transcript")
		if len(bindings) == 0 {
			return ""
		}
		return "Tip: " + displayKeyBinding(bindings[0]) + " searches this conversation"
	default:
		return "Tip: /copy copies the last response"
	}
}

// renderComposerTip paints the tip right-aligned in the gap row above the
// composer. A tip that cannot fit is omitted, matching Rust's narrow snapshot.
func (m *Model) renderComposerTip() string {
	tip := m.composerTipLine()
	if tip == "" {
		return ""
	}
	width := codextui.DisplayWidth(tip)
	if width == 0 || width+1 > m.width {
		return ""
	}
	return strings.Repeat(" ", m.width-width-1) + m.footerStyle.Render(tip)
}

// renderFollowControlRow paints the centered control on the composer gap row.
// It records the hit rectangle for handleFollowControlMouse.
func (m *Model) renderFollowControlRow() string {
	m.followControlColumn = 0
	m.followControlWidth = 0
	label := m.followControlLabel(m.width)
	if label == "" {
		m.followControlHover = false
		return m.renderComposerTip()
	}
	width := codextui.DisplayWidth(label)
	left := max((m.width-width)/2, 0)
	m.followControlColumn = left
	m.followControlWidth = width
	line := strings.Repeat(" ", left) + m.followControlStyledLabel(label)
	if bgSGR, ok := composerSurfaceSGR(); ok {
		return paintComposerSurface(line, bgSGR, m.width)
	}
	return line
}

func (m *Model) followControlStyledLabel(label string) string {
	accent := codextui.ThemeAccentColor(m.activeTUITheme())
	if strings.TrimSpace(accent) == "" {
		accent = "12"
	}
	style := lipgloss.NewStyle().Foreground(lipgloss.Color(accent))
	if m.followControlHover {
		style = style.Bold(true).Reverse(true)
	}
	return style.Render(label)
}

// transcriptLatestNavigationLabel is the footer's return hint. Rust reserves
// Enter for the legacy pager but advertises it whenever an empty composer can
// jump back without consuming a draft.
func (m *Model) transcriptLatestNavigationLabel() string {
	if m != nil && m.enterReturnsToLatest() {
		return "enter/esc latest"
	}
	return "esc latest"
}

// transcriptFollowFooterLine mirrors Rust's transcript `status_line_with_navigation`:
// unseen activity and partial history lead the footer, followed by the
// return-to-latest hint and the short shortcuts hint.
func (m *Model) transcriptFollowFooterLine() string {
	if m == nil {
		return ""
	}
	unseen := m.transcriptUnseenActivity
	canReturn := m.canReturnToLatest()
	if !unseen && !canReturn {
		return ""
	}
	// Rust prints this once older history is paged in; Go keeps the whole
	// transcript resident, so the same footer state is reached whenever the
	// reader is paused above earlier messages.
	showEarlier := m.transcriptHistoryPartial || (canReturn && m.transcript.YOffset > 0)
	var line strings.Builder
	if unseen {
		if !canReturn && !showEarlier {
			line.WriteString("New activity")
		} else {
			line.WriteString("New activity \u00b7 ")
		}
	}
	if showEarlier {
		line.WriteString("Earlier messages available.  ")
	}
	if canReturn {
		line.WriteString(m.transcriptLatestNavigationLabel())
	}
	status := line.String()
	if status == "" {
		return ""
	}
	width := max(m.width-2, 1)
	if codextui.DisplayWidth(status) > width {
		navigation := ""
		if canReturn {
			navigation = m.transcriptLatestNavigationLabel()
		}
		switch {
		case !canReturn:
			status = "New activity"
		case unseen:
			status = "New \u00b7 " + navigation
		default:
			status = navigation
		}
	}
	if m.composerIsEmpty() {
		candidate := status + " \u00b7 " + CharKey('?').Label() + " shortcuts"
		if codextui.DisplayWidth(candidate) <= width {
			status = candidate
		}
	}
	return status
}

// handleReturnToLatestKey consumes Esc and a bare Enter while the transcript is
// paused away from the tail. Esc only reaches this point after backtrack,
// popups, find, and selection have declined it.
func (m *Model) handleReturnToLatestKey(msg keymsg.KeyMsg) bool {
	if m == nil || !m.canReturnToLatest() {
		return false
	}
	switch msg.Type {
	case keymsg.KeyEsc:
		m.jumpToTranscriptLatest()
		return true
	case keymsg.KeyEnter:
		if !m.enterReturnsToLatest() {
			return false
		}
		m.jumpToTranscriptLatest()
		return true
	default:
		return false
	}
}

// handleFollowControlMouse owns clicks on the painted return-to-latest control.
// A press inside the label arms the control; releasing inside jumps. A release
// outside cancels, matching Rust's drag-out behavior.
func (m *Model) handleFollowControlMouse(msg mousemsg.MouseMsg) (bool, bubbletea.Cmd) {
	if m == nil || m.followControlRow < 0 || m.followControlWidth <= 0 {
		return false, nil
	}
	inside := msg.Y == m.followControlRow &&
		msg.X >= m.followControlColumn &&
		msg.X < m.followControlColumn+m.followControlWidth
	switch msg.Action {
	case mousemsg.MouseActionPress:
		if msg.Button != mousemsg.MouseButtonLeft || !inside {
			return false, nil
		}
		m.followControlPressed = true
		return true, nil
	case mousemsg.MouseActionRelease:
		if msg.Button != mousemsg.MouseButtonLeft || !m.followControlPressed {
			return false, nil
		}
		m.followControlPressed = false
		if inside {
			m.jumpToTranscriptLatest()
		}
		return true, nil
	case mousemsg.MouseActionMotion:
		if inside == m.followControlHover {
			return false, nil
		}
		m.followControlHover = inside
		return inside, nil
	default:
		return false, nil
	}
}
