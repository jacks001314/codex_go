package tea

import (
	"fmt"
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
	"codex_go/tui/history_cell"
)

// Turn-scoped safety recourse (Rust #57363/#57365). A moderation notification
// opens a pending notice above the composer; the terminal policy error for the
// same turn is deferred so the block can be rendered with its explanation and
// safer rewrites instead of the generic turn error.

const (
	safetyRecourseRewriteActionPrefix = "safety-recourse-rewrite:"
	safetyRecourseEnableDaybreakID    = "enable-daybreak"
	safetyRecoursePickerTitle         = "Here are a few ways I can help:"
	safetyRecoursePickerSubtitle      = "Choosing a rewrite sends it in a new chat."
	safetyRecourseFollowUpTrigger     = "safety_recourse_follow_up"
	// safetyRecourseCyberFallbackMessage keeps the failed tool-call text
	// non-empty when a cyber refusal arrives without its server message.
	safetyRecourseCyberFallbackMessage = "Some cybersecurity requests are still limited."
)

// TurnModerationMetadataMsg delivers a turn/moderationMetadata notification to
// the tea model (Rust safety_recourse::on_metadata).
type TurnModerationMetadataMsg struct {
	ThreadID string
	TurnID   string
	Metadata any
}

// SafetyRecourseRewriteReadyMsg reports a verified fork prepared for the chosen
// rewrite. The response carries the fork's prefix history for display.
type SafetyRecourseRewriteReadyMsg struct {
	SourceThreadID string
	Prompt         string
	Response       SessionResumeResponse
}

// SafetyRecourseRewriteFailedMsg reports that the fork or its first turn could
// not be prepared. The prompt returns to the composer on the source thread.
type SafetyRecourseRewriteFailedMsg struct {
	SourceThreadID string
	Prompt         string
	Err            error
}

// applyTurnModerationMetadata records a turn-scoped block notice and, once the
// server finishes preparing it, the validated explanation and rewrites.
func (m *Model) applyTurnModerationMetadata(msg TurnModerationMetadataMsg) bubbletea.Cmd {
	if m == nil || m.State == nil || msg.Metadata == nil || !m.isTaskRunning() {
		return nil
	}
	if m.activeSide != nil || m.readOnlyThread || m.threadUnavailable {
		return nil
	}
	if !m.safetyRecourseMatchesTurn(msg.ThreadID, msg.TurnID) {
		return nil
	}
	if m.pendingSafetyRecourse != nil && len(m.queued) > 0 {
		return nil
	}
	domain, result, ok := chatwidget.ParseSafetyRecourseMetadata(msg.Metadata)
	if !ok {
		return nil
	}
	if m.pendingSafetyRecourse == nil {
		pending := chatwidget.NewPendingSafetyRecourse(domain, m.cyberPolicyNotice())
		m.pendingSafetyRecourse = &pending
		m.safetyRecourseThreadID = firstNonEmpty(strings.TrimSpace(msg.ThreadID), m.currentThreadID())
		m.safetyRecourseTurnID = strings.TrimSpace(msg.TurnID)
	}
	pending := m.pendingSafetyRecourse
	if pending.Domain == domain && pending.Result == nil && result != nil {
		pending.Result = result
		m.clearSafetyBuffering()
		m.setWorkingStatusHeader(chatwidget.SafetyRecourseStatusHeader)
	}
	m.refreshTranscript()
	return nil
}

func (m *Model) safetyRecourseMatchesTurn(threadID, turnID string) bool {
	if m == nil {
		return false
	}
	if threadID = strings.TrimSpace(threadID); threadID != "" && threadID != m.currentThreadID() {
		return false
	}
	if turnID = strings.TrimSpace(turnID); turnID != "" {
		if current := strings.TrimSpace(m.currentTurnID); current != "" && current != turnID {
			return false
		}
	}
	return true
}

// safetyRecourseMatchesActiveTurn reports whether the pending notice still
// belongs to the running turn. Local exec events may omit the turn id, so an
// empty pending id matches the active turn.
func (m *Model) safetyRecourseMatchesActiveTurn() bool {
	if m == nil || m.pendingSafetyRecourse == nil {
		return false
	}
	if pendingThread := strings.TrimSpace(m.safetyRecourseThreadID); pendingThread != "" &&
		pendingThread != m.currentThreadID() {
		return false
	}
	pendingTurn := strings.TrimSpace(m.safetyRecourseTurnID)
	currentTurn := strings.TrimSpace(m.currentTurnID)
	return pendingTurn == "" || currentTurn == "" || pendingTurn == currentTurn
}

// deferSafetyRecourseError records a terminal error that belongs to the
// pending block so the completion path can render the explanation instead of
// the generic error (Rust safety_recourse::defer_error).
func (m *Model) deferSafetyRecourseError(message, kind string) bool {
	if !m.safetyRecourseMatchesActiveTurn() {
		return false
	}
	pending := m.pendingSafetyRecourse
	if !chatwidget.SafetyRecourseMatchesError(pending.Domain, message, kind) {
		return false
	}
	pending.DeferredError = strings.TrimSpace(message)
	pending.DeferredErrorKind = strings.TrimSpace(kind)
	return true
}

// applyCyberPolicyRecourse renders the pending cyber block, when there is one
// for the reported turn, and reports whether it took ownership of the error.
func (m *Model) applyCyberPolicyRecourse(msg CyberPolicyErrorMsg) bool {
	if m == nil || m.pendingSafetyRecourse == nil ||
		m.pendingSafetyRecourse.Domain != chatwidget.SafetyRecourseDomainCyber ||
		m.pendingSafetyRecourse.Result == nil {
		return false
	}
	if turnID := strings.TrimSpace(msg.TurnID); turnID != "" {
		if pendingTurn := strings.TrimSpace(m.safetyRecourseTurnID); pendingTurn != "" && pendingTurn != turnID {
			return false
		}
	}
	message := strings.TrimSpace(msg.Message)
	if message == "" {
		message = safetyRecourseCyberFallbackMessage
	}
	return m.renderSafetyRecourseFailure(message, "cyberPolicy")
}

// clearSafetyRecourse drops the pending notice without rendering it.
func (m *Model) clearSafetyRecourse() {
	if m == nil {
		return
	}
	if strings.TrimSpace(m.workingStatusHeader) == chatwidget.SafetyRecourseStatusHeader {
		m.setWorkingStatusHeader("")
	}
	m.pendingSafetyRecourse = nil
	m.safetyRecourseThreadID = ""
	m.safetyRecourseTurnID = ""
	m.safetyRecoursePrompts = nil
	if m.modal != nil && m.modal.id == chatwidget.SafetyRecourseViewID {
		m.modal = nil
	}
}

// renderDeferredSafetyRecourse renders a pending block whose terminal error was
// already matched and deferred. It reports whether the failure was rendered.
func (m *Model) renderDeferredSafetyRecourse() bool {
	if m == nil || m.pendingSafetyRecourse == nil {
		return false
	}
	deferred := strings.TrimSpace(m.pendingSafetyRecourse.DeferredError)
	if deferred == "" {
		return false
	}
	return m.renderSafetyRecourseFailure(deferred, m.pendingSafetyRecourse.DeferredErrorKind)
}

// takeDeferredSafetyRecourseError returns the terminal error deferred for the
// active block, if any (Rust PendingRecourse::deferred_error).
func (m *Model) takeDeferredSafetyRecourseError() (string, string) {
	if m == nil || m.pendingSafetyRecourse == nil {
		return "", ""
	}
	return strings.TrimSpace(m.pendingSafetyRecourse.DeferredError),
		strings.TrimSpace(m.pendingSafetyRecourse.DeferredErrorKind)
}

// applyNonRetrySafetyError renders a deferred policy error that no longer has a
// turn-scoped explanation, mirroring Rust handle_non_retry_error: cyber keeps
// the Daybreak-aware refusal cell, bio keeps the plain access block, and other
// failures use the generic turn error cell.
func (m *Model) applyNonRetrySafetyError(message, kind string) {
	if m == nil || m.State == nil {
		return
	}
	message = strings.TrimSpace(message)
	if message == "" {
		message = "Unknown error"
	}
	m.setStatus("error")
	m.markActiveToolCallsFailed(message)
	if strings.EqualFold(strings.TrimSpace(kind), "cyberPolicy") {
		m.applyCyberPolicyErrorMsg(CyberPolicyErrorMsg{ThreadID: m.currentThreadID()})
		m.notice = ""
		return
	}
	if chatwidget.IsBioPolicyError(message) {
		m.applyHistoryCell(historycell.NewSafetyAccessBlockEvent())
		m.notice = message
		return
	}
	m.clearCurrentThreadAfterFailure(message)
	m.addTurnErrorHistoryMessage(message)
	m.notice = message
}

// renderSafetyRecourseFailure renders the block notice with the turn-scoped
// explanation and opens the safer-rewrite picker. It returns false when the
// error must keep its normal handling (Rust render_failure).
func (m *Model) renderSafetyRecourseFailure(errorMessage, errorKind string) bool {
	if m == nil || m.State == nil || len(m.queued) > 0 || !m.safetyRecourseMatchesActiveTurn() {
		return false
	}
	pending := m.pendingSafetyRecourse
	if pending == nil || pending.Result == nil {
		return false
	}
	message := strings.TrimSpace(errorMessage)
	if !chatwidget.SafetyRecourseMatchesError(pending.Domain, message, errorKind) {
		return false
	}
	sourceThreadID := firstNonEmpty(strings.TrimSpace(m.safetyRecourseThreadID), m.currentThreadID())
	blockedTurnID := strings.TrimSpace(m.safetyRecourseTurnID)
	cell := chatwidget.SafetyRecourseNotice(pending.Domain, m.cyberPolicyNotice())
	cell.Explanation = pending.Result.Explanation
	m.clearSafetyBuffering()
	m.setWorkingStatusHeader("")
	m.markActiveToolCallsFailed(message)
	m.clearSafetyRecourse()
	m.applyHistoryCell(cell)
	items := pending.Result.Suggestions
	m.safetyRecoursePrompts = append([]chatwidget.SafetyRecourseSuggestion(nil), items...)
	m.safetyRecoursePickerThreadID = sourceThreadID
	m.safetyRecoursePickerTurnID = blockedTurnID
	picker := m.safetyRecourseSelectionView(pending.Domain, items)
	m.setStatus("idle")
	m.notice = ""
	m.refreshTranscript()
	if len(picker.Items) > 0 {
		m.openSelectionViewModal(ModalKindGeneric, picker)
	}
	return true
}

func (m *Model) safetyRecourseSelectionView(domain chatwidget.SafetyRecourseDomain, suggestions []chatwidget.SafetyRecourseSuggestion) chatwidget.SelectionView {
	view := chatwidget.SelectionView{
		ViewID:      chatwidget.SafetyRecourseViewID,
		Title:       safetyRecoursePickerTitle,
		AllowCancel: true,
	}
	if len(suggestions) > 0 {
		view.Subtitle = safetyRecoursePickerSubtitle
	}
	for index, suggestion := range suggestions {
		view.Items = append(view.Items, chatwidget.SelectionItem{
			ID:              fmt.Sprintf("%s%d", safetyRecourseRewriteActionPrefix, index),
			Name:            suggestion.Label,
			Action:          chatwidget.UsageMenuAction(fmt.Sprintf("%s%d", safetyRecourseRewriteActionPrefix, index)),
			DismissOnSelect: true,
		})
	}
	// Rust render_failure offers the toggle only for Notice::Disabled (the model
	// advertises Daybreak but the user has it off).
	if domain == chatwidget.SafetyRecourseDomainCyber && m.cyberPolicyNotice() == codextui.DaybreakNoticeDisabled {
		view.Items = append(view.Items, chatwidget.SelectionItem{
			ID:              safetyRecourseEnableDaybreakID,
			Name:            "Enable Daybreak",
			Action:          chatwidget.UsageMenuAction(safetyRecourseEnableDaybreakID),
			DismissOnSelect: true,
		})
	}
	return view
}

// applySafetyRecourseModalOption submits the selected rewrite or enables
// Daybreak. Rust reuses AppEvent::ContinueSafetyRecourse and
// PersistDaybreakSelection.
func (m *Model) applySafetyRecourseModalOption(optionID string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	optionID = strings.TrimSpace(optionID)
	if optionID == safetyRecourseEnableDaybreakID {
		m.notice = ""
		return m.setDaybreakPreference(true)
	}
	if !strings.HasPrefix(optionID, safetyRecourseRewriteActionPrefix) {
		return nil
	}
	var index int
	if _, err := fmt.Sscanf(optionID, safetyRecourseRewriteActionPrefix+"%d", &index); err != nil {
		return nil
	}
	if index < 0 || index >= len(m.safetyRecoursePrompts) {
		return nil
	}
	prompt := strings.TrimSpace(m.safetyRecoursePrompts[index].Prompt)
	sourceThreadID := strings.TrimSpace(m.currentThreadID())
	if pickerThreadID := strings.TrimSpace(m.safetyRecoursePickerThreadID); pickerThreadID != "" {
		sourceThreadID = pickerThreadID
	}
	turnID := strings.TrimSpace(m.safetyRecoursePickerTurnID)
	m.safetyRecoursePrompts = nil
	m.safetyRecoursePickerThreadID = ""
	m.safetyRecoursePickerTurnID = ""
	if prompt == "" || sourceThreadID == "" {
		return nil
	}
	if m.onContinueSafetyRecourse == nil {
		m.composer.SetValue(prompt)
		m.notice = "A safer rewrite needs a new chat, which is unavailable here."
		m.refreshTranscript()
		return nil
	}
	return m.onContinueSafetyRecourse(sourceThreadID, turnID, prompt)
}

// applySafetyRecourseRewriteReady switches to the verified fork and submits the
// selected rewrite with the safety-recourse trigger.
func (m *Model) applySafetyRecourseRewriteReady(msg SafetyRecourseRewriteReadyMsg) bubbletea.Cmd {
	if m == nil || m.State == nil || strings.TrimSpace(msg.Prompt) == "" {
		return nil
	}
	if source := strings.TrimSpace(msg.SourceThreadID); source != "" && source != m.currentThreadID() {
		return nil
	}
	forkID := strings.TrimSpace(msg.Response.SummaryThreadID())
	if forkID == "" {
		return m.applySafetyRecourseRewriteFailed(SafetyRecourseRewriteFailedMsg{
			SourceThreadID: msg.SourceThreadID,
			Prompt:         msg.Prompt,
			Err:            fmt.Errorf("the fork is unavailable"),
		})
	}
	cwd := ""
	if msg.Response.Summary != nil {
		cwd = strings.TrimSpace(msg.Response.Summary.CWD)
	}
	m.applyResumeResponseWithCWD(forkID, msg.Response, cwd)
	return m.submitRequest(SubmitRequest{
		Prompt:      strings.TrimSpace(msg.Prompt),
		TurnTrigger: safetyRecourseFollowUpTrigger,
	}, false)
}

// applySafetyRecourseRewriteFailed restores the rewrite so the user can retry
// the request manually (Rust continue_with_rewrite's rejection path).
func (m *Model) applySafetyRecourseRewriteFailed(msg SafetyRecourseRewriteFailedMsg) bubbletea.Cmd {
	if m == nil || m.State == nil {
		return nil
	}
	if source := strings.TrimSpace(msg.SourceThreadID); source != "" && source != m.currentThreadID() {
		return nil
	}
	prompt := strings.TrimSpace(msg.Prompt)
	detail := ""
	if msg.Err != nil {
		detail = strings.TrimSpace(msg.Err.Error())
	}
	message := "Couldn’t start a new chat for this rewrite."
	if detail != "" {
		message += " " + detail
	}
	if prompt != "" {
		m.composer.SetValue(prompt)
	}
	m.addTurnErrorHistoryMessage(message)
	m.notice = message
	m.setStatus("idle")
	m.refreshTranscript()
	return nil
}

// renderSafetyRecourseNotice draws the transient block notice above the
// composer while the explanation is pending (Rust rendering.rs).
func (m *Model) renderSafetyRecourseNotice() string {
	if m == nil || m.pendingSafetyRecourse == nil || m.modal != nil {
		return ""
	}
	width := m.width
	if width < 20 {
		width = 20
	}
	lines := m.pendingSafetyRecourse.Notice.DisplayLines(width)
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func (r SessionResumeResponse) SummaryThreadID() string {
	if r.Summary == nil {
		return ""
	}
	return strings.TrimSpace(r.Summary.ThreadID)
}
