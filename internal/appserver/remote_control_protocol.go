package appserver

import "codex_go/internal/remotecontrol"

const NotificationRemoteControlStatusChanged NotificationMethod = "remoteControl/status/changed"

type RemoteControlStatusChangedNotification struct {
	Status         remotecontrol.ConnectionStatus `json:"status"`
	ServerName     string                         `json:"serverName"`
	InstallationID string                         `json:"installationId"`
	EnvironmentID  *string                        `json:"environmentId"`
}

func RemoteControlStatusChangedFromManager(notification *remotecontrol.StatusChangedNotification) *RemoteControlStatusChangedNotification {
	if notification == nil {
		return nil
	}
	return &RemoteControlStatusChangedNotification{
		Status:         notification.Status,
		ServerName:     notification.ServerName,
		InstallationID: notification.InstallationID,
		EnvironmentID:  cloneString(notification.EnvironmentID),
	}
}

func NewRemoteControlStatusChangedNotification(params *remotecontrol.StatusChangedNotification) *Notification {
	return NewNotification(NotificationRemoteControlStatusChanged, RemoteControlStatusChangedFromManager(params))
}
