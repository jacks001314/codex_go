package appserver

import (
	"sync"
	"testing"
)

// readStateTargetedSink records notifications per connection so the broadcast
// scope of thread/readState/changed is observable.
type readStateTargetedSink struct {
	mu           sync.Mutex
	byConnection map[string][]*Notification
}

func (s *readStateTargetedSink) record(connectionID string, notification *Notification) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byConnection == nil {
		s.byConnection = map[string][]*Notification{}
	}
	s.byConnection[connectionID] = append(s.byConnection[connectionID], notification)
}

func (s *readStateTargetedSink) Notify(notification *Notification) {
	s.record("", notification)
}

func (s *readStateTargetedSink) NotifyToConnection(connectionID string, notification *Notification) {
	s.record(connectionID, notification)
}

func (s *readStateTargetedSink) list(connectionID string) []*Notification {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*Notification(nil), s.byConnection[connectionID]...)
}

// Mirrors Rust #61852: a durable receipt change is broadcast to unsubscribed
// experimental clients, while stable clients and per-connection opt-outs never
// see the notification.
func TestThreadReadStateChangedReachesUnsubscribedExperimentalClientsLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	sink := &readStateTargetedSink{}
	router.SetNotificationSink(sink)
	for _, connectionID := range []string{"conn-writer", "conn-observer", "conn-stable", "conn-opted-out"} {
		router.rememberConnectionClientInfo(connectionID, ClientInfo{Name: connectionID, Version: "1"})
	}
	router.rememberConnectionExperimentalAPI("conn-writer", &InitializeCapabilities{ExperimentalAPI: true})
	router.rememberConnectionExperimentalAPI("conn-observer", &InitializeCapabilities{ExperimentalAPI: true})
	router.rememberConnectionExperimentalAPI("conn-stable", &InitializeCapabilities{})
	optedOut := &InitializeCapabilities{
		ExperimentalAPI:           true,
		OptOutNotificationMethods: []string{string(NotificationThreadReadStateChanged)},
	}
	router.rememberConnectionExperimentalAPI("conn-opted-out", optedOut)
	router.rememberConnectionNotificationOptOut("conn-opted-out", optedOut)

	state := ThreadReadState{
		FirstUnread: &ThreadUnreadPosition{Type: "turn", TurnID: "turn-1"},
		Revision:    "revision-1",
	}
	router.notifyThreadReadStateChanged("thread-never-opened", state)

	for _, connectionID := range []string{"conn-writer", "conn-observer"} {
		notifications := sink.list(connectionID)
		if len(notifications) != 1 || notifications[0].Method != NotificationThreadReadStateChanged {
			t.Fatalf("%s notifications = %#v", connectionID, notifications)
		}
		params, ok := notifications[0].Params.(*ThreadReadStateChangedNotification)
		if !ok || params.ThreadID != "thread-never-opened" || params.ReadState.Revision != "revision-1" ||
			params.ReadState.FirstUnread == nil || params.ReadState.FirstUnread.TurnID != "turn-1" {
			t.Fatalf("%s notification params = %#v", connectionID, notifications[0].Params)
		}
	}
	for _, connectionID := range []string{"conn-stable", "conn-opted-out"} {
		if notifications := sink.list(connectionID); len(notifications) != 0 {
			t.Fatalf("%s received %#v, want nothing", connectionID, notifications)
		}
	}
	if notifications := sink.list(""); len(notifications) != 0 {
		t.Fatalf("notification fell back to broadcast: %#v", notifications)
	}
}
