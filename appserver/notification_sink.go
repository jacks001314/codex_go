package appserver

import "sync"

type NotificationSink interface {
	Notify(notification *Notification)
}

type TargetedNotificationSink interface {
	NotifyToConnection(connectionID string, notification *Notification)
}

type NotificationSinkFunc func(notification *Notification)

func (f NotificationSinkFunc) Notify(notification *Notification) {
	if f != nil {
		f(notification)
	}
}

type NotificationBuffer struct {
	mu            sync.Mutex
	notifications []*Notification
}

func NewNotificationBuffer() *NotificationBuffer {
	return &NotificationBuffer{}
}

func (b *NotificationBuffer) Notify(notification *Notification) {
	if b == nil || notification == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.notifications = append(b.notifications, notification)
}

func (b *NotificationBuffer) List() []*Notification {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]*Notification, 0, len(b.notifications))
	for _, notification := range b.notifications {
		out = append(out, notification)
	}
	return out
}
