package tea

import (
	"fmt"
	"strings"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/internal/tui"
)

type RequestUserInputMsg struct {
	ID               string
	Questions        []codextui.RequestUserInputQuestion
	AutoResolutionMS *int
}

type requestUserInputTimeoutMsg struct {
	ID string
}

func (m *Model) openRequestUserInputModal(message RequestUserInputMsg) bubbletea.Cmd {
	state, err := codextui.NewRequestUserInputState(message.Questions, message.AutoResolutionMS)
	if err != nil {
		m.openModal(ModalRequestMsg{
			ID:    message.ID,
			Kind:  ModalKindUserInput,
			Title: "Request user input",
			Body:  err.Error(),
			Options: []ModalOption{
				{ID: "cancel", Label: "Cancel", Shortcut: "enter"},
			},
		})
		return nil
	}
	m.modal = &modalState{
		id:        strings.TrimSpace(message.ID),
		kind:      ModalKindUserInput,
		title:     "Request user input",
		userInput: state,
	}
	m.refreshRequestUserInputModal()
	return requestUserInputTimeoutCommand(m.modal.id, state.AutoResolutionMS)
}

func requestUserInputTimeoutCommand(id string, autoResolutionMS *int) bubbletea.Cmd {
	if strings.TrimSpace(id) == "" || autoResolutionMS == nil || *autoResolutionMS <= 0 {
		return nil
	}
	duration := time.Duration(*autoResolutionMS) * time.Millisecond
	return bubbletea.Tick(duration, func(time.Time) bubbletea.Msg {
		return requestUserInputTimeoutMsg{ID: id}
	})
}

func (m *Model) applyRequestUserInputTimeout(message requestUserInputTimeoutMsg) bubbletea.Cmd {
	if m == nil || m.modal == nil || m.modal.kind != ModalKindUserInput || m.modal.userInput == nil {
		return nil
	}
	if strings.TrimSpace(message.ID) != "" && message.ID != m.modal.id {
		return nil
	}
	modal := m.modal
	response := ModalResponse{
		ID:        modal.id,
		Kind:      modal.kind,
		Cancelled: false,
		UserInput: &UserInputDecision{
			Answers:     map[string]string{},
			AnswerLists: map[string][]string{},
			TimedOut:    true,
		},
	}
	m.modal = nil
	m.notice = "Request user input timed out"
	if m.onModalResponse != nil {
		return m.onModalResponse(response)
	}
	return nil
}

func (m *Model) refreshRequestUserInputModal() {
	if m == nil || m.modal == nil || m.modal.userInput == nil {
		return
	}
	if m.modal.userInput.ConfirmUnanswered {
		unanswered := m.modal.userInput.UnansweredCount()
		suffix := "questions"
		if unanswered == 1 {
			suffix = "question"
		}
		m.modal.title = "Request user input"
		m.modal.body = m.modal.userInput.RenderBody(m.width - 4)
		m.modal.options = []ModalOption{
			{
				ID:          "proceed",
				Label:       "Proceed",
				Description: fmt.Sprintf("Submit with %d unanswered %s.", unanswered, suffix),
				Shortcut:    "1",
			},
			{
				ID:          "go_back",
				Label:       "Go back",
				Description: "Return to the first unanswered question.",
				Shortcut:    "2",
			},
		}
		m.modal.selected = 0
		return
	}
	question, ok := m.modal.userInput.CurrentQuestion()
	if !ok {
		m.modal.body = ""
		m.modal.options = []ModalOption{{ID: "submit", Label: "Submit", Shortcut: "enter"}}
		m.modal.selected = 0
		return
	}
	m.modal.body = m.modal.userInput.RenderBody(m.width - 4)
	if len(question.Options) == 0 {
		m.modal.options = []ModalOption{{ID: "submit", Label: "Submit answer", Shortcut: "enter"}}
		m.modal.selected = 0
		return
	}
	options := make([]ModalOption, 0, len(question.Options))
	for i, option := range question.Options {
		options = append(options, ModalOption{
			ID:          fmt.Sprintf("option_%d", i),
			Label:       option.Label,
			Description: option.Description,
			Shortcut:    requestUserInputShortcut(i),
		})
	}
	m.modal.options = normalizeModalOptions(options)
	if m.modal.selected < 0 || m.modal.selected >= len(m.modal.options) {
		m.modal.selected = 0
	}
}

func requestUserInputShortcut(index int) string {
	if index >= 0 && index < 9 {
		return string(rune('1' + index))
	}
	return ""
}
