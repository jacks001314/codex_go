package tea

// Rust #42671 (tui/src/app/new_session.rs + session_lifecycle.rs): starting a
// replacement thread can fail while reading the server's new-session defaults
// or calling thread/start. The active thread, its configuration, its
// subscriptions, and the composed message all survive that failure; only the
// error is rendered and the submission returns to the composer.

import (
	"strings"

	codextui "codex_go/tui"
)

// applyNewSessionStartFailed hands the failed submission back to the composer
// and renders Rust's error cell without touching the active thread.
func (m *Model) applyNewSessionStartFailed(message NewSessionStartFailedMsg) {
	if m == nil || m.State == nil {
		return
	}
	text := strings.TrimSpace(message.ErrorMessage)
	if text == "" {
		text = "Failed to start a fresh session through the app server."
	}
	m.removeOptimisticUserPrompt(message.Request)
	m.restoreSubmissionDraft(message.Request)
	m.programStatusAfterTurn = codextui.ProgramStatus{State: codextui.ProgramStatusError}
	m.setStatus("error")
	m.addErrorHistoryMessage(text)
	m.notice = text
	m.refreshTranscript()
}

// removeOptimisticUserPrompt drops the transcript entry the submit path added
// before the thread existed: Rust never renders the message when the
// replacement-thread start fails.
func (m *Model) removeOptimisticUserPrompt(request SubmitRequest) {
	if m == nil || m.State == nil {
		return
	}
	prompt := strings.TrimSpace(request.Prompt)
	if prompt == "" {
		return
	}
	for index := len(m.State.Messages) - 1; index >= 0; index-- {
		message := m.State.Messages[index]
		if message.Role != codextui.RoleUser || strings.TrimSpace(message.UserPrompt) != prompt {
			continue
		}
		m.State.Messages = append(m.State.Messages[:index], m.State.Messages[index+1:]...)
		m.State.BumpMessagesRevision()
		return
	}
}

// restoreSubmissionDraft puts a submitted message back into the composer
// (Rust ChatWidget::restore_user_message_to_composer): text, structured
// elements, attachments, and mention bindings.
func (m *Model) restoreSubmissionDraft(request SubmitRequest) {
	if m == nil {
		return
	}
	m.clearComposerPasteWindow()
	m.setComposerPrompt(request.Prompt)
	m.restoreComposerTextElements(request.Prompt, request.TextElements)
	m.composer.SetCursor(len(m.composer.Value()))
	m.attachments = cloneComposerAttachments(request.Attachments)
	m.composerMentionBindings = append([]string(nil), request.MentionBindings...)
	m.slashPopup = slashCommandPopup{}
	m.skillPopup = skillPopupState{}
	m.refreshSlashPopup()
}
