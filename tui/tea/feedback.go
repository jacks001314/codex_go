package tea

import (
	"fmt"
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/appserver"
	historycell "codex_go/tui/history_cell"
)

type feedbackCategory string

const (
	feedbackCategoryBug         feedbackCategory = "bug"
	feedbackCategoryBadResult   feedbackCategory = "bad_result"
	feedbackCategoryGoodResult  feedbackCategory = "good_result"
	feedbackCategorySafetyCheck feedbackCategory = "safety_check"
	feedbackCategoryOther       feedbackCategory = "other"
)

type feedbackStage string

const (
	feedbackStageDisabled feedbackStage = "disabled"
	feedbackStageCategory feedbackStage = "category"
	feedbackStageConsent  feedbackStage = "consent"
	feedbackStageNote     feedbackStage = "note"
)

type feedbackModalState struct {
	stage       feedbackStage
	category    feedbackCategory
	includeLogs bool
	note        string
}

func (m *Model) openFeedbackFlow() {
	if m == nil {
		return
	}
	if !m.feedbackEnabled {
		m.openModal(ModalRequestMsg{
			ID:      "feedback-disabled",
			Kind:    ModalKindFeedback,
			Title:   "Sending feedback is disabled",
			Body:    "This action is disabled by configuration.",
			Options: []ModalOption{{ID: "close", Label: "Close"}},
		})
		m.modal.feedback = &feedbackModalState{stage: feedbackStageDisabled}
		return
	}
	m.openModal(ModalRequestMsg{
		ID:      "feedback-category",
		Kind:    ModalKindFeedback,
		Title:   "How was this?",
		Options: feedbackCategoryOptions(),
	})
	m.modal.feedback = &feedbackModalState{stage: feedbackStageCategory}
}

func feedbackCategoryOptions() []ModalOption {
	return []ModalOption{
		{ID: string(feedbackCategoryBug), Label: "bug", Description: "Crash, error message, hang, or broken UI/behavior."},
		{ID: string(feedbackCategoryBadResult), Label: "bad result", Description: "Output was off-target, incorrect, incomplete, or unhelpful."},
		{ID: string(feedbackCategoryGoodResult), Label: "good result", Description: "Helpful, correct, high-quality, or delightful result worth celebrating."},
		{ID: string(feedbackCategorySafetyCheck), Label: "safety check", Description: "Benign usage blocked due to safety checks or refusals."},
		{ID: string(feedbackCategoryOther), Label: "other", Description: "Slowness, feature suggestion, UX feedback, or anything else."},
	}
}

func (m *Model) updateFeedbackModal(message bubbletea.KeyMsg) bubbletea.Cmd {
	if m == nil || m.modal == nil || m.modal.feedback == nil {
		return nil
	}
	state := m.modal.feedback
	if state.stage == feedbackStageNote {
		return m.updateFeedbackNote(message)
	}
	switch message.Type {
	case bubbletea.KeyEsc, bubbletea.KeyCtrlC:
		m.modal = nil
		return nil
	case bubbletea.KeyUp:
		m.moveModalSelection(-1)
		return nil
	case bubbletea.KeyDown, bubbletea.KeyTab:
		m.moveModalSelection(1)
		return nil
	case bubbletea.KeyEnter:
		switch state.stage {
		case feedbackStageDisabled:
			m.modal = nil
		case feedbackStageCategory:
			state.category = feedbackCategory(m.modal.options[m.modal.selected].ID)
			m.openFeedbackConsent(state)
		case feedbackStageConsent:
			state.includeLogs = m.modal.selected == 0
			m.openFeedbackNote(state)
		}
	}
	return nil
}

func (m *Model) openFeedbackConsent(state *feedbackModalState) {
	m.modal.id = "feedback-consent"
	m.modal.title = "Upload logs?"
	m.modal.body = strings.Join([]string{
		"The following files will be sent:",
		"  - codex-logs.log",
		"  - codex-doctor-report.json",
		"  - codex-apps-tools-cache.json (if available)",
		"  - codex-app-directory-cache.json (if available)",
	}, "\n")
	m.modal.options = []ModalOption{
		{ID: "yes", Label: "Yes", Description: "Share the current Codex session logs and diagnostics with the team for troubleshooting."},
		{ID: "no", Label: "No"},
	}
	m.modal.selected = 0
	state.stage = feedbackStageConsent
}

func (m *Model) openFeedbackNote(state *feedbackModalState) {
	m.modal.id = "feedback-note"
	m.modal.title = feedbackNoteTitle(state.category)
	m.modal.body = ""
	m.modal.options = []ModalOption{{ID: "submit", Label: "Submit"}}
	m.modal.selected = 0
	state.stage = feedbackStageNote
}

func feedbackNoteTitle(category feedbackCategory) string {
	label := strings.ReplaceAll(string(category), "_", " ")
	return "Tell us more (" + label + ")"
}

func (m *Model) updateFeedbackNote(message bubbletea.KeyMsg) bubbletea.Cmd {
	state := m.modal.feedback
	switch message.Type {
	case bubbletea.KeyEsc, bubbletea.KeyCtrlC:
		m.modal = nil
	case bubbletea.KeyEnter:
		m.modal = nil
		return m.submitFeedback(state.category, state.note, state.includeLogs)
	case bubbletea.KeyCtrlJ:
		state.note += "\n"
	case bubbletea.KeyBackspace:
		runes := []rune(state.note)
		if len(runes) > 0 {
			state.note = string(runes[:len(runes)-1])
		}
	case bubbletea.KeyRunes:
		state.note += string(message.Runes)
	}
	return nil
}

func (m *Model) renderFeedbackNoteModal() string {
	state := m.modal.feedback
	placeholder := "(optional) Write a short description to help us further"
	if state.category == feedbackCategorySafetyCheck {
		placeholder = "(optional) Share what was refused and why it should have been allowed"
	}
	text := state.note
	if text == "" {
		text = placeholder
	}
	return strings.Join([]string{
		feedbackNoteTitle(state.category),
		"  " + text,
		"",
		"Enter submit  Ctrl+J newline  Esc cancel",
	}, "\n")
}

func (m *Model) submitFeedback(category feedbackCategory, note string, includeLogs bool) bubbletea.Cmd {
	params := appserver.FeedbackUploadParams{
		Classification: string(category),
		IncludeLogs:    includeLogs,
	}
	if reason := strings.TrimSpace(note); reason != "" {
		params.Reason = &reason
	}
	if m.State != nil {
		if threadID := strings.TrimSpace(m.State.ThreadID); threadID != "" {
			params.ThreadID = &threadID
		}
	}
	if m.onSubmitFeedback == nil {
		return func() bubbletea.Msg {
			return FeedbackSubmitResultMsg{Category: category, IncludeLogs: includeLogs, Err: fmt.Errorf("feedback/upload is unavailable in this runtime")}
		}
	}
	submit := m.onSubmitFeedback
	return func() bubbletea.Msg {
		response, err := submit(params)
		return FeedbackSubmitResultMsg{Category: category, IncludeLogs: includeLogs, Response: response, Err: err}
	}
}

func (m *Model) applyFeedbackSubmitResult(message FeedbackSubmitResultMsg) {
	if message.Err != nil {
		m.applyHistoryCell(historycell.NewErrorEvent("Failed to submit feedback: " + message.Err.Error()))
		return
	}
	threadID := strings.TrimSpace(message.Response.ThreadID)
	if threadID == "" && m.State != nil {
		threadID = strings.TrimSpace(m.State.ThreadID)
	}
	prefix := "Feedback uploaded."
	if !message.IncludeLogs {
		prefix = "Feedback recorded (no logs)."
	}
	lines := []string{prefix}
	if message.Category == feedbackCategoryGoodResult {
		lines[0] += " Thanks for the feedback!"
		if threadID != "" {
			lines = append(lines, "", "  Thread ID: "+threadID)
		}
	} else {
		url := "https://github.com/openai/codex/issues/new?template=3-cli.yml&steps=Uploaded%20thread:%20" + threadID
		lines[0] += " Please open an issue using the following URL:"
		lines = append(lines, "", "  "+url)
		if threadID != "" {
			lines = append(lines, "", "  Or mention your thread ID "+threadID+" in an existing issue.")
		}
	}
	m.applyHistoryCell(historycell.NewPlainHistoryCell(lines))
}
