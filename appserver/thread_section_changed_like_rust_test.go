package appserver

import (
	"encoding/json"
	"testing"

	"codex_go/session"
)

// Rust #58380: a successful thread/section/move responds first and then
// broadcasts thread/section/changed to the connected clients. The notification
// carries the destination section and is emitted even for a move within the
// same section; removal carries null. A failed move emits nothing.
func TestThreadSectionMoveBroadcastsChangedNotificationLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	threadID := session.ThreadID("thread-section-changed")
	createRecord(t, store, threadID, fixedTime())
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	t.Cleanup(func() { _ = router.Close() })
	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)
	router.SetDeferredGoalNotifications(true)
	t.Cleanup(func() { router.SetDeferredGoalNotifications(false) })

	move := func(id int64, sectionID *string) *Response {
		return router.Handle(requestWithParams(t, IntID(id), MethodThreadSectionMove, ThreadSectionMoveParams{
			ThreadID:  string(threadID),
			SectionID: OptionalString{Set: true, Value: sectionID},
		}))
	}
	pinned := session.PinnedThreadSectionID
	if response := move(1, &pinned); response.Error != nil {
		t.Fatalf("thread/section/move error = %+v", response.Error)
	}
	if immediate := sink.List(); len(immediate) != 0 {
		t.Fatalf("notification was written before the response: %+v", immediate)
	}
	pending := router.FlushDeferredGoalNotifications(defaultRequestConnectionID)
	if len(pending) != 1 || pending[0].Method != NotificationThreadSectionChanged {
		t.Fatalf("pending notifications = %+v", pending)
	}
	params, ok := pending[0].Params.(*ThreadSectionChangedNotification)
	if !ok || params.ThreadID != string(threadID) || params.SectionID == nil || *params.SectionID != pinned {
		t.Fatalf("section/changed params = %#v", pending[0].Params)
	}

	// A move within the same section still refreshes other clients' ordering.
	if response := move(2, &pinned); response.Error != nil {
		t.Fatalf("same-section move error = %+v", response.Error)
	}
	if pending := router.FlushDeferredGoalNotifications(defaultRequestConnectionID); len(pending) != 1 {
		t.Fatalf("same-section move notifications = %+v", pending)
	}

	// Removal reports a null section.
	if response := move(3, nil); response.Error != nil {
		t.Fatalf("section removal error = %+v", response.Error)
	}
	pending = router.FlushDeferredGoalNotifications(defaultRequestConnectionID)
	if len(pending) != 1 {
		t.Fatalf("section removal notifications = %+v", pending)
	}
	data, err := json.Marshal(pending[0])
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	paramsMap, _ := decoded["params"].(map[string]any)
	if paramsMap["threadId"] != string(threadID) {
		t.Fatalf("notification threadId = %#v", paramsMap["threadId"])
	}
	if value, exists := paramsMap["sectionId"]; !exists || value != nil {
		t.Fatalf("notification sectionId = %#v (present %t), want explicit null", value, exists)
	}

	// A failed move must not emit the notification.
	unknown := "01984de2-8f74-7c91-a3b2-5c5e937cf319"
	if response := move(4, &unknown); response.Error == nil {
		t.Fatal("move into an unknown section unexpectedly succeeded")
	}
	if pending := router.FlushDeferredGoalNotifications(defaultRequestConnectionID); len(pending) != 0 {
		t.Fatalf("failed move notifications = %+v", pending)
	}
}
