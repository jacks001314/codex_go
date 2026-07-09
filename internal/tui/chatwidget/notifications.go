package chatwidget

import (
	"path/filepath"
	"strings"
)

const AgentNotificationPreviewRunes = 200

type NotificationsSetting struct {
	Enabled   bool
	Custom    []string
	CustomSet bool
}

type NotificationKind string

const (
	NotificationAgentTurnComplete     NotificationKind = "agent-turn-complete"
	NotificationExecApprovalRequested NotificationKind = "exec-approval-requested"
	NotificationEditApprovalRequested NotificationKind = "edit-approval-requested"
	NotificationElicitationRequested  NotificationKind = "elicitation-requested"
	NotificationPlanModePrompt        NotificationKind = "plan-mode-prompt"
)

type Notification struct {
	Kind       NotificationKind
	Response   string
	Command    string
	CWD        string
	Changes    []string
	ServerName string
	Title      string
}

func AgentTurnCompleteNotification(response string) Notification {
	return Notification{Kind: NotificationAgentTurnComplete, Response: response}
}

func ExecApprovalRequestedNotification(command string) Notification {
	return Notification{Kind: NotificationExecApprovalRequested, Command: command}
}

func EditApprovalRequestedNotification(cwd string, changes []string) Notification {
	return Notification{Kind: NotificationEditApprovalRequested, CWD: cwd, Changes: append([]string(nil), changes...)}
}

func ElicitationRequestedNotification(serverName string) Notification {
	return Notification{Kind: NotificationElicitationRequested, ServerName: serverName}
}

func PlanModePromptNotification(title string) Notification {
	return Notification{Kind: NotificationPlanModePrompt, Title: title}
}

func (n Notification) Display() string {
	switch n.Kind {
	case NotificationAgentTurnComplete:
		if preview, ok := AgentTurnPreview(n.Response); ok {
			return preview
		}
		return "Agent turn complete"
	case NotificationExecApprovalRequested:
		return "Approval requested: " + TruncateRunes(n.Command, 30)
	case NotificationEditApprovalRequested:
		if len(n.Changes) == 1 {
			return "Codex wants to edit " + displayPathFor(n.Changes[0], n.CWD)
		}
		return "Codex wants to edit " + formatInt64(int64(len(n.Changes))) + " files"
	case NotificationElicitationRequested:
		return "Approval requested by " + n.ServerName
	case NotificationPlanModePrompt:
		return "Plan mode prompt: " + n.Title
	default:
		return ""
	}
}

func (n Notification) Priority() int {
	switch n.Kind {
	case NotificationAgentTurnComplete:
		return 0
	case NotificationExecApprovalRequested, NotificationEditApprovalRequested, NotificationElicitationRequested, NotificationPlanModePrompt:
		return 1
	default:
		return 0
	}
}

func (n Notification) TypeName() string {
	switch n.Kind {
	case NotificationExecApprovalRequested, NotificationEditApprovalRequested, NotificationElicitationRequested:
		return "approval-requested"
	default:
		return string(n.Kind)
	}
}

func (n Notification) AllowedFor(setting NotificationsSetting) bool {
	if setting.CustomSet || len(setting.Custom) > 0 {
		for _, allowed := range setting.Custom {
			if allowed == n.TypeName() {
				return true
			}
		}
		return false
	}
	return setting.Enabled
}

func (n Notification) AllowedForCustom(allowedTypes []string) bool {
	for _, allowed := range allowedTypes {
		if allowed == n.TypeName() {
			return true
		}
	}
	return false
}

type NotificationState struct {
	Pending *Notification
}

func (s *NotificationState) Notify(notification Notification, setting NotificationsSetting) bool {
	if s == nil || !notification.AllowedFor(setting) {
		return false
	}
	if s.Pending != nil && s.Pending.Priority() > notification.Priority() {
		return false
	}
	clone := notification
	s.Pending = &clone
	return true
}

func (s *NotificationState) TakePendingDisplay() (string, bool) {
	if s == nil || s.Pending == nil {
		return "", false
	}
	notification := *s.Pending
	s.Pending = nil
	display := notification.Display()
	return display, strings.TrimSpace(display) != ""
}

func AgentTurnPreview(response string) (string, bool) {
	normalized := strings.Join(strings.Fields(response), " ")
	if strings.TrimSpace(normalized) == "" {
		return "", false
	}
	return TruncateRunes(normalized, AgentNotificationPreviewRunes), true
}

func UserInputRequestSummary(header string, question string) (string, bool) {
	summary := strings.TrimSpace(header)
	if summary == "" {
		summary = strings.TrimSpace(question)
	}
	if summary == "" {
		return "", false
	}
	return TruncateRunes(summary, 30), true
}

func TruncateRunes(value string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	if maxRunes < 3 {
		return string(runes[:maxRunes])
	}
	return string(runes[:maxRunes-3]) + "..."
}

func displayPathFor(path string, cwd string) string {
	path = strings.TrimSpace(path)
	cwd = strings.TrimSpace(cwd)
	if path == "" {
		return "file"
	}
	if cwd != "" {
		if rel, err := filepath.Rel(cwd, path); err == nil && !strings.HasPrefix(rel, "..") && rel != "." {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(filepath.Base(path))
}
