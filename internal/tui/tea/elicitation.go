package tea

import (
	"encoding/json"
	"fmt"
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	bottompane "codex_go/internal/tui/bottom_pane"
	chatwidget "codex_go/internal/tui/chatwidget"
)

type ElicitationRequestMsg struct {
	ID              string
	Title           string
	ServerName      string
	RequestID       string
	ThreadID        string
	TurnID          string
	Message         string
	URL             string
	RequestedSchema any
	Meta            map[string]any
}

func (m *Model) openElicitationModal(message ElicitationRequestMsg) bubbletea.Cmd {
	title := strings.TrimSpace(message.Title)
	if title == "" {
		title = "MCP request"
		if strings.TrimSpace(message.ServerName) != "" {
			title = "MCP request from " + strings.TrimSpace(message.ServerName)
		}
	}
	if strings.TrimSpace(message.URL) != "" {
		m.openModal(ModalRequestMsg{
			ID:    firstNonEmpty(message.ID, message.RequestID),
			Kind:  ModalKindElicitation,
			Title: title,
			Body:  elicitationURLBody(message),
			Options: []ModalOption{
				{ID: "accept", Label: "Allow", Shortcut: "y"},
				{ID: "decline", Label: "Decline", Shortcut: "n"},
				{ID: "cancel", Label: "Cancel", Shortcut: "c"},
			},
		})
		return m.queueNotification(chatwidget.ElicitationRequestedNotification(message.ServerName))
	}
	form, err := bottompane.NewElicitationFormRequest(message.ServerName, firstNonEmpty(message.RequestID, message.ID), message.Message, message.RequestedSchema, message.Meta)
	if err != nil {
		m.openModal(ModalRequestMsg{
			ID:      firstNonEmpty(message.ID, message.RequestID),
			Kind:    ModalKindElicitation,
			Title:   title,
			Body:    "Unable to render MCP request: " + err.Error(),
			Options: []ModalOption{{ID: "cancel", Label: "Cancel", Shortcut: "enter"}},
		})
		return m.queueNotification(chatwidget.ElicitationRequestedNotification(message.ServerName))
	}
	form.ThreadID = message.ThreadID
	form.TurnID = message.TurnID
	body := strings.Join(form.RenderLines(firstPositive(m.width-4, 76)), "\n")
	if body == "" {
		body = strings.TrimSpace(message.Message)
	}
	options := elicitationOptionsForForm(form)
	m.openModal(ModalRequestMsg{
		ID:      firstNonEmpty(message.ID, message.RequestID),
		Kind:    ModalKindElicitation,
		Title:   title,
		Body:    body,
		Options: options,
	})
	if m.modal != nil {
		m.modal.elicitation = form
	}
	return m.queueNotification(chatwidget.ElicitationRequestedNotification(message.ServerName))
}

func elicitationURLBody(message ElicitationRequestMsg) string {
	lines := []string{}
	if strings.TrimSpace(message.Message) != "" {
		lines = append(lines, strings.TrimSpace(message.Message))
	}
	lines = append(lines, "URL: "+strings.TrimSpace(message.URL))
	if len(message.Meta) > 0 {
		if data, err := json.Marshal(message.Meta); err == nil {
			lines = append(lines, "Metadata: "+string(data))
		}
	}
	return strings.Join(lines, "\n")
}

func elicitationOptionsForForm(form *bottompane.ElicitationFormRequest) []ModalOption {
	if form == nil {
		return []ModalOption{{ID: "cancel", Label: "Cancel", Shortcut: "enter"}}
	}
	if form.ResponseMode == bottompane.ElicitationApprovalAction && len(form.Fields) > 0 {
		options := make([]ModalOption, 0, len(form.Fields[0].Options))
		for _, option := range form.Fields[0].Options {
			options = append(options, ModalOption{
				ID:       option.Value,
				Label:    approvalOptionLabel(option),
				Shortcut: approvalOptionShortcut(option.Value),
			})
		}
		if len(options) > 0 {
			return options
		}
	}
	return []ModalOption{
		{ID: "accept", Label: "Submit", Shortcut: "y"},
		{ID: "decline", Label: "Decline", Shortcut: "n"},
		{ID: "cancel", Label: "Cancel", Shortcut: "c"},
	}
}

func approvalOptionLabel(option bottompane.ElicitationOption) string {
	label := strings.TrimSpace(option.Label)
	if label != "" {
		return label
	}
	switch option.Value {
	case bottompane.ApprovalAcceptOnceValue:
		return "Allow once"
	case bottompane.ApprovalAcceptSessionValue:
		return "Allow for this session"
	case bottompane.ApprovalAcceptAlwaysValue:
		return "Always allow"
	case bottompane.ApprovalDeclineValue:
		return "Decline"
	case bottompane.ApprovalCancelValue:
		return "Cancel"
	default:
		return fmt.Sprintf("%s", option.Value)
	}
}

func approvalOptionShortcut(value string) string {
	switch value {
	case bottompane.ApprovalAcceptOnceValue:
		return "y"
	case bottompane.ApprovalAcceptSessionValue:
		return "a"
	case bottompane.ApprovalAcceptAlwaysValue:
		return "l"
	case bottompane.ApprovalDeclineValue:
		return "n"
	case bottompane.ApprovalCancelValue:
		return "c"
	default:
		return ""
	}
}
