package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/internal/tui"
	chatwidget "codex_go/internal/tui/chatwidget"
)

func notificationSettingsOrDefault(settings *chatwidget.NotificationsSetting) chatwidget.NotificationsSetting {
	if settings == nil {
		return chatwidget.NotificationsSetting{Enabled: true}
	}
	return chatwidget.NotificationsSetting{
		Enabled:   settings.Enabled,
		Custom:    append([]string(nil), settings.Custom...),
		CustomSet: settings.CustomSet,
	}
}

func notificationMethodOrDefault(method codextui.NotificationMethod) codextui.NotificationMethod {
	switch method {
	case codextui.NotificationMethodOSC9, codextui.NotificationMethodBEL:
		return method
	default:
		return codextui.NotificationMethodAuto
	}
}

func notificationConditionOrDefault(condition codextui.NotificationCondition) codextui.NotificationCondition {
	if condition == "" {
		return codextui.NotificationConditionUnfocused
	}
	return condition
}

func (m *Model) queueNotification(notification chatwidget.Notification) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if !m.notifications.Notify(notification, m.notificationSettings) {
		return nil
	}
	return m.maybePostPendingNotification()
}

func (m *Model) maybePostPendingNotification() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	display, ok := m.notifications.TakePendingDisplay()
	if !ok {
		return nil
	}
	if !codextui.ShouldEmitNotification(m.notificationCondition, m.terminalFocused) {
		return nil
	}
	if m.notificationPost == nil {
		return nil
	}
	return m.notificationPost(display, m.notificationMethod)
}

func (m *Model) lastAssistantNotificationResponse() string {
	if m == nil || m.State == nil {
		return ""
	}
	for i := len(m.State.Messages) - 1; i >= 0; i-- {
		message := m.State.Messages[i]
		if message.Role == codextui.RoleAssistant {
			return strings.TrimSpace(message.Text)
		}
	}
	return ""
}
