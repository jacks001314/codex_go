package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	tuiapp "codex_go/tui/app"
	idecontext "codex_go/tui/ide_context"
)

const threadTitleSuggestionModel = "gpt-5.6-luna"

// ThreadNameSuggestedMsg reports the result of a /rename title suggestion
// (Rust #40495).
type ThreadNameSuggestedMsg struct {
	ThreadID      string
	RequestID     uint64
	Suggestion    string
	OK            bool
	Automatic     bool
	ExpectedTitle string
	RenameFailed  bool
}

// nextThreadNameSuggestionRequest returns a stable correlation id for the
// pending rename prompt.
func (m *Model) nextThreadNameSuggestionRequest() uint64 {
	m.nextThreadNameSuggestionID++
	if m.nextThreadNameSuggestionID == 0 {
		m.nextThreadNameSuggestionID = 1
	}
	return m.nextThreadNameSuggestionID
}

// startThreadNameSuggestion runs the temporary structured title turn from the
// latest substantive conversation messages.
func (m *Model) startThreadNameSuggestion(threadID string, requestID uint64) bubbletea.Cmd {
	if m == nil || m.State == nil || m.onSuggestThreadName == nil {
		return nil
	}
	conversation, ok := tuiapp.RecentThreadTitleConversation(m.threadTitleMessages())
	if !ok {
		return nil
	}
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
	prompt := tuiapp.RecentConversationThreadTitlePrompt(conversation)
	schema := tuiapp.ThreadTitleOutputSchema()
	return func() bubbletea.Msg {
		response, err := generate(threadID, options, prompt, schema)
		message := ThreadNameSuggestedMsg{ThreadID: threadID, RequestID: requestID}
		if err == nil {
			message.Suggestion, message.OK = tuiapp.ParseGeneratedThreadTitle(response)
		}
		return message
	}
}

// threadTitleModel mirrors Rust's preference for the dedicated low-effort
// title model when ChatGPT auth and the model catalog make it available.
func (m *Model) threadTitleModel() string {
	if m == nil || m.State == nil {
		return ""
	}
	current := strings.TrimSpace(m.State.Model)
	provider := strings.TrimSpace(m.State.Provider)
	if provider != "" && !strings.EqualFold(provider, "openai") {
		return current
	}
	if !m.hasChatGPTAccount {
		return current
	}
	for _, option := range m.modelPickerOpts {
		if strings.TrimSpace(option.ID) == threadTitleSuggestionModel {
			return threadTitleSuggestionModel
		}
	}
	return current
}

// threadTitleMessages snapshots the substantive user and assistant messages
// from the live transcript.
func (m *Model) threadTitleMessages() []tuiapp.ThreadTitleMessage {
	if m == nil || m.State == nil {
		return nil
	}
	messages := make([]tuiapp.ThreadTitleMessage, 0, len(m.State.Messages))
	for _, message := range m.State.Messages {
		if message.TranscriptOnly || message.StartupTip {
			continue
		}
		switch message.Role {
		case codextui.RoleUser:
			text := strings.TrimSpace(message.UserPrompt)
			if text == "" {
				text = message.Text
			}
			// Rust omits IDE context and keeps only the user's request.
			text, _ = idecontext.ExtractPromptRequestWithOffset(text)
			messages = append(messages, tuiapp.ThreadTitleMessage{Role: "user", Text: text})
		case codextui.RoleAssistant:
			messages = append(messages, tuiapp.ThreadTitleMessage{
				Role:  "assistant",
				Text:  message.Text,
				Phase: message.Phase,
			})
		}
	}
	return messages
}

// applyThreadNameSuggested pre-fills the active rename prompt when the response
// still belongs to the same thread and request.
func (m *Model) applyThreadNameSuggested(message ThreadNameSuggestedMsg) bubbletea.Cmd {
	if m == nil || m.State == nil {
		return nil
	}
	if message.Automatic {
		return m.applyAutomaticThreadTitle(message)
	}
	if strings.TrimSpace(message.ThreadID) != strings.TrimSpace(m.State.ThreadID) {
		return nil
	}
	if m.modal == nil || m.modal.id != "rename-thread" || m.modal.customPrompt == nil {
		return nil
	}
	if m.modal.customPrompt.ApplyTextSuggestion(message.RequestID, message.Suggestion, message.OK) {
		m.refreshTranscript()
	}
	return nil
}
