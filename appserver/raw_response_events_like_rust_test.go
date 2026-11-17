package appserver

import (
	"strings"
	"testing"
	"time"

	"codex_go/session"
	"codex_go/turn"
)

// Rust's `send_raw_response_items` (`core/src/session/mod.rs`) announces every
// recorded conversation item as `RawResponseItem`, and the app server forwards
// those events only to threads that opted into experimental raw events
// (`thread_lifecycle.rs` drops them otherwise). Recording a user message or an
// accepted agent communication therefore reaches the thread's stream with the
// item's turn id, while a thread without the opt-in sees nothing.
func TestRuntimeAppendEmitsRecordedRawResponseItemsForOptInThreadsLikeRust(t *testing.T) {
	router, threadID := newRawResponseRecordingRouter(t, true)
	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)
	now := time.Date(2026, 11, 16, 14, 0, 0, 0, time.UTC)

	message := session.Item{
		ID:        "user-1",
		Type:      "message",
		Role:      "user",
		Text:      "hello",
		Content:   []session.ContentPart{{Type: "input_text", Text: "hello"}},
		CreatedAt: now,
		Metadata:  map[string]any{"turnId": "turn-raw"},
	}
	if _, err := router.runtimeAppendItems(threadID, []session.Item{message}); err != nil {
		t.Fatalf("runtimeAppendItems() error = %v", err)
	}
	recorded := rawResponseNotifications(sink)
	if len(recorded) != 1 {
		t.Fatalf("raw response notifications = %d, want the recorded user message", len(recorded))
	}
	if recorded[0].TurnID != "turn-raw" || recorded[0].ThreadID != string(threadID) {
		t.Fatalf("raw notification identity = %+v, want the recorded turn and thread", recorded[0])
	}
	assertRawMessageItem(t, recorded[0].Item, "message", "user", "hello")

	mail := runtimeAgentCommunicationInputItem("/root/worker", "/root", "recorded mail", false, true)
	if !router.persistRuntimeAgentCommunications(string(threadID), "turn-mail", &turn.TurnStartParams{
		ThreadID:             string(threadID),
		AdditionalInputItems: []any{mail},
	}, now.Add(time.Minute)) {
		t.Fatal("agent communication was not recorded")
	}
	recorded = rawResponseNotifications(sink)
	if len(recorded) != 2 {
		t.Fatalf("raw response notifications = %d, want the message and the communication", len(recorded))
	}
	if recorded[1].TurnID != "turn-mail" {
		t.Fatalf("communication raw notification turn = %q, want turn-mail", recorded[1].TurnID)
	}
	assertRawMessageItem(t, recorded[1].Item, "agent_message", "", "")
}

// Without the opt-in the same recording stays off the stream, as the app server
// drops `RawResponseItem` for those threads.
func TestRuntimeAppendSkipsRecordedRawResponseItemsWithoutOptInLikeRust(t *testing.T) {
	router, threadID := newRawResponseRecordingRouter(t, false)
	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)

	item := session.Item{
		ID:        "user-1",
		Type:      "message",
		Role:      "user",
		Text:      "hello",
		Content:   []session.ContentPart{{Type: "input_text", Text: "hello"}},
		CreatedAt: time.Now().UTC(),
		Metadata:  map[string]any{"turnId": "turn-plain"},
	}
	if _, err := router.runtimeAppendItems(threadID, []session.Item{item}); err != nil {
		t.Fatalf("runtimeAppendItems() error = %v", err)
	}
	if recorded := rawResponseNotifications(sink); len(recorded) != 0 {
		t.Fatalf("raw response notifications = %d, want none without the opt-in", len(recorded))
	}
}

func newRawResponseRecordingRouter(t *testing.T, rawEvents bool) (*RuntimeRouter, session.ThreadID) {
	t.Helper()
	store := session.NewStore(t.TempDir())
	threadID := session.ThreadID("thread-raw-events")
	extra := map[string]any{}
	if rawEvents {
		extra[experimentalRawEventsExtraKey] = true
	}
	if err := store.Create(&session.Record{ID: threadID, SessionID: string(threadID), Metadata: session.Metadata{Extra: extra}}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)}), threadID
}

func rawResponseNotifications(sink *NotificationBuffer) []*RawResponseItemCompletedNotification {
	if sink == nil {
		return nil
	}
	var out []*RawResponseItemCompletedNotification
	for _, notification := range sink.List() {
		if notification.Method != NotificationRawResponseItemCompleted {
			continue
		}
		if payload, ok := notification.Params.(*RawResponseItemCompletedNotification); ok && payload != nil {
			out = append(out, payload)
		}
	}
	return out
}

func assertRawMessageItem(t *testing.T, payload any, wantType string, wantRole string, wantText string) {
	t.Helper()
	item, ok := payload.(map[string]any)
	if !ok {
		t.Fatalf("raw item = %#v, want a response item map", payload)
	}
	if itemType, _ := item["type"].(string); itemType != wantType {
		t.Fatalf("raw item type = %q, want %q (%#v)", itemType, wantType, item)
	}
	if wantRole != "" {
		if role, _ := item["role"].(string); role != wantRole {
			t.Fatalf("raw item role = %q, want %q (%#v)", role, wantRole, item)
		}
	}
	if wantText != "" && !strings.Contains(inputItemText(item["content"]), wantText) {
		t.Fatalf("raw item content = %#v, want %q", item["content"], wantText)
	}
}
