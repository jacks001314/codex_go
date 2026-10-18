package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	tuiapp "codex_go/tui/app"
)

func (m *Model) resetAutomaticThreadTitle() {
	if m == nil {
		return
	}
	m.automaticThreadTitlePending = false
	m.automaticThreadTitleThreadID = ""
	m.automaticThreadTitleExpected = ""
}

// startAutomaticThreadTitle applies the provisional name and starts one bounded
// structured title request (Rust #40492).
func (m *Model) startAutomaticThreadTitle(text string) bubbletea.Cmd {
	if m == nil || m.State == nil || m.automaticThreadTitlePending {
		return nil
	}
	if m.readOnlyThread || m.threadUnavailable || m.inSideConversation() {
		return nil
	}
	if strings.TrimSpace(m.State.ThreadName) != "" {
		return nil
	}
	threadID := strings.TrimSpace(m.State.ThreadID)
	if threadID == "" {
		return nil
	}
	prompt := strings.TrimSpace(text)
	provisional := tuiapp.ProvisionalThreadTitle(prompt)
	if provisional == "" {
		return nil
	}
	m.automaticThreadTitlePending = true
	m.automaticThreadTitleThreadID = threadID
	m.automaticThreadTitleExpected = provisional
	m.State.SetThreadName(provisional)
	m.refreshTranscript()

	requestID := m.nextThreadNameSuggestionRequest()
	rename := m.onRenameThread
	generate := m.onSuggestThreadName
	options := RecapThreadOptions{
		Model:             m.threadTitleModel(),
		ModelProvider:     strings.TrimSpace(m.State.Provider),
		CWD:               strings.TrimSpace(m.State.CWD),
		PermissionProfile: strings.TrimSpace(m.State.Sandbox),
		ThreadSource:      "thread_title",
	}
	if options.Model == threadTitleSuggestionModel {
		options.Effort = "low"
	}
	titlePrompt := tuiapp.ThreadTitlePrompt(prompt)
	schema := tuiapp.ThreadTitleOutputSchema()
	return func() bubbletea.Msg {
		if rename != nil {
			if err := rename(threadID, provisional); err != nil {
				return ThreadNameSuggestedMsg{
					ThreadID:      threadID,
					RequestID:     requestID,
					ExpectedTitle: provisional,
					Automatic:     true,
					RenameFailed:  true,
				}
			}
		}
		if generate == nil {
			return ThreadNameSuggestedMsg{ThreadID: threadID, RequestID: requestID, ExpectedTitle: provisional, Automatic: true}
		}
		response, err := generate(threadID, options, titlePrompt, schema)
		message := ThreadNameSuggestedMsg{
			ThreadID:      threadID,
			RequestID:     requestID,
			ExpectedTitle: provisional,
			Automatic:     true,
		}
		if err == nil {
			message.Suggestion, message.OK = tuiapp.ParseGeneratedThreadTitle(response)
		}
		return message
	}
}

// applyAutomaticThreadTitle applies the generated name only while the
// provisional name is still current and the thread is still active.
func (m *Model) applyAutomaticThreadTitle(message ThreadNameSuggestedMsg) bubbletea.Cmd {
	if m == nil || m.State == nil {
		return nil
	}
	threadID := strings.TrimSpace(message.ThreadID)
	if threadID != m.automaticThreadTitleThreadID || threadID != strings.TrimSpace(m.State.ThreadID) {
		return nil
	}
	expected := strings.TrimSpace(message.ExpectedTitle)
	if expected == "" || strings.TrimSpace(m.State.ThreadName) != expected {
		m.automaticThreadTitlePending = false
		return nil
	}
	m.automaticThreadTitlePending = false
	if message.RenameFailed {
		m.State.SetThreadName("")
		m.refreshTranscript()
		return nil
	}
	if !message.OK || strings.TrimSpace(message.Suggestion) == "" {
		return nil
	}
	finalTitle := strings.TrimSpace(message.Suggestion)
	if m.onRenameThread != nil {
		if err := m.onRenameThread(threadID, finalTitle); err != nil {
			return nil
		}
	}
	m.State.SetThreadName(finalTitle)
	m.refreshTranscript()
	return nil
}
