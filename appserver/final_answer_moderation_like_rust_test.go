package appserver

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"codex_go/model"
	"codex_go/session"
)

// Mirrors Rust #59842: the moderation verdict a turn's model stream reports is
// attached to the completed final answer item, so item/completed carries the
// result next to the answer it judged. The turn-scoped
// turn/moderationMetadata notification keeps firing unchanged.
func TestFinalAnswerModerationAttachedToCompletedAnswerLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	defer router.Close()
	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)

	metadata := map[string]any{
		"final_answer_moderation": map[string]any{"flagged": true, "category": "policy"},
	}
	state := newResponsesStreamNotificationState(false, "turn-1")
	router.notifyResponsesStreamEvent("thread-1", "turn-1", &model.ResponsesStreamEvent{
		Kind:               model.ResponsesStreamEventModeration,
		ModerationMetadata: metadata,
	}, state)

	// The turn-scoped notification is unchanged.
	var turnScoped *TurnModerationMetadataNotification
	for _, notification := range sink.List() {
		if notification.Method != NotificationTurnModerationMetadata {
			continue
		}
		if payload, ok := notification.Params.(*TurnModerationMetadataNotification); ok && payload.TurnID == "turn-1" {
			turnScoped = payload
		}
	}
	if turnScoped == nil || !reflect.DeepEqual(turnScoped.Metadata, metadata) {
		t.Fatalf("turn/moderationMetadata = %#v, want the recorded metadata", turnScoped)
	}

	items := []session.Item{
		{ID: "user-1", Type: "user_message", Role: "user", Text: "summarize the release notes", CreatedAt: time.Now()},
		{ID: "answer-1", Type: "agent_message", Role: "assistant", Text: "Here is the summary.", CreatedAt: time.Now()},
		{ID: "sub-agent-1", Type: "agent_message", Role: "assistant", Text: "child finished",
			Metadata: map[string]any{"delivery": "async"}, CreatedAt: time.Now()},
	}
	if finalAnswerID := finalAnswerThreadItemID(items); finalAnswerID != "answer-1" {
		t.Fatalf("finalAnswerThreadItemID() = %q, want the delivered answer", finalAnswerID)
	}

	finalAnswer := BuildThreadItem(items[1])
	applyFinalAnswerModeration(&finalAnswer, "answer-1", router.takeFinalAnswerModeration("thread-1", "turn-1"))
	if finalAnswer.Moderation == nil {
		t.Fatal("completed answer item lost the moderation result")
	}
	payload := threadItemPayload(finalAnswer)
	if payload["moderation"] == nil {
		t.Fatalf("item/completed payload = %#v, want the moderation field", payload)
	}
	// The payload survives a wire round trip, so thread/read replays the verdict.
	encoded, err := json.Marshal(&finalAnswer)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var decoded ThreadItem
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if !reflect.DeepEqual(decoded.Moderation, finalAnswer.Moderation) {
		t.Fatalf("decoded moderation = %#v, want %#v", decoded.Moderation, finalAnswer.Moderation)
	}

	// Nothing else carries the verdict, and the same value cannot leak into a
	// later turn.
	for _, index := range []int{0, 2} {
		other := BuildThreadItem(items[index])
		applyFinalAnswerModeration(&other, "answer-1", metadata)
		if other.Moderation != nil {
			t.Fatalf("item %q carried the verdict: %#v", other.ID, other.Moderation)
		}
	}
	if again := router.takeFinalAnswerModeration("thread-1", "turn-1"); again != nil {
		t.Fatalf("takeFinalAnswerModeration() = %#v, want the verdict consumed once", again)
	}

	// An answer with no verdict serializes without the field at all.
	plain := BuildThreadItem(items[1])
	plainJSON, err := json.Marshal(&plain)
	if err != nil {
		t.Fatalf("json.Marshal(plain) error = %v", err)
	}
	var plainWire map[string]any
	if err := json.Unmarshal(plainJSON, &plainWire); err != nil {
		t.Fatalf("json.Unmarshal(plain) error = %v", err)
	}
	if _, ok := plainWire["moderation"]; ok {
		t.Fatalf("plain answer wire = %s, want no moderation key", plainJSON)
	}
}
