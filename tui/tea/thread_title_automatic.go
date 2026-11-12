package tea

import (
	"strings"

	bubbletea "charm.land/bubbletea/v2"

	tuiapp "codex_go/tui/app"
)

func (m *Model) resetAutomaticThreadTitle() {
	if m == nil {
		return
	}
	m.automaticThreadTitleEpoch.Add(1)
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
	// Rust #60842: desktop shows a single line of the first message until the
	// generated title arrives, and keeps it when generation fails.
	provisional := tuiapp.FallbackThreadTitle(prompt)
	if provisional == "" {
		return nil
	}
	m.automaticThreadTitleEpoch.Add(1)
	epoch := m.automaticThreadTitleEpoch.Load()
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
		if m.automaticThreadTitleEpoch.Load() != epoch {
			return nil
		}
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
		return m.refreshStatusControlsCmd()
	}
	if !message.OK || strings.TrimSpace(message.Suggestion) == "" {
		return nil
	}
	finalTitle := strings.TrimSpace(message.Suggestion)
	rename := m.onRenameThread
	epoch := m.automaticThreadTitleEpoch.Load()
	// Keep the event loop free while the final title is persisted. The epoch
	// prevents a slow save from overwriting a manual rename (Rust #58970).
	return func() bubbletea.Msg {
		if m.automaticThreadTitleEpoch.Load() != epoch {
			return nil
		}
		var err error
		if rename != nil {
			err = rename(threadID, finalTitle)
		}
		return AutomaticThreadNameSavedMsg{
			ThreadID: threadID,
			Title:    finalTitle,
			Expected: expected,
			Epoch:    epoch,
			Err:      err,
		}
	}
}

// AutomaticThreadNameSavedMsg reports the background save of a generated title.
type AutomaticThreadNameSavedMsg struct {
	ThreadID string
	Title    string
	Expected string
	Epoch    uint64
	Err      error
}

func (m *Model) applyAutomaticThreadNameSaved(message AutomaticThreadNameSavedMsg) bubbletea.Cmd {
	if m == nil || m.State == nil || message.Err != nil {
		return nil
	}
	if message.Epoch != m.automaticThreadTitleEpoch.Load() {
		return nil
	}
	if strings.TrimSpace(message.ThreadID) != strings.TrimSpace(m.State.ThreadID) {
		return nil
	}
	if strings.TrimSpace(m.State.ThreadName) != strings.TrimSpace(message.Expected) {
		return nil
	}
	m.State.SetThreadName(message.Title)
	m.refreshTranscript()
	return m.refreshStatusControlsCmd()
}
