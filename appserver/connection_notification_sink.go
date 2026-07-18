package appserver

type connectionNotificationSink struct {
	connectionID string
	send         func(*Notification)
}

func (s connectionNotificationSink) Notify(notification *Notification) {
	if s.send != nil {
		s.send(notification)
	}
}

func (s connectionNotificationSink) NotifyToConnection(connectionID string, notification *Notification) {
	if normalizeConnectionID(connectionID) != normalizeConnectionID(s.connectionID) {
		return
	}
	s.Notify(notification)
}
