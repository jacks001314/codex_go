package realtime

// Mirrors Rust #62251: a persisted model user message plus its harness
// provenance is the only copy of an accepted handoff or saved chunk; the
// realtime receipt is projected from it, and its stored observation time
// restores the clock.

import (
	"encoding/json"
	"testing"
)

func handoffMessagePayload(t *testing.T, id string, turnID string, text string) json.RawMessage {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"type": "message",
		"id":   id,
		"role": "user",
		"content": []map[string]any{
			{"type": "input_text", "text": text},
		},
		"internal_chat_message_metadata_passthrough": map[string]any{"turn_id": turnID},
	})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestResponseItemProjectsTurnInputAcceptedLikeRust(t *testing.T) {
	payload := handoffMessagePayload(t, "item-1", "turn-1", "fix the flaky test")
	metadata := json.RawMessage(`{"realtime_handoff":{"event_id":"event-1","realtime_session_id":"voice","observed_at_ms":1500,"handoff_id":"handoff-1","mode":"steer","source_event_ids":["a","b"]}}`)
	item, ok := RealtimeItemFromResponseItem(payload, metadata)
	if !ok || item == nil {
		t.Fatal("handoff message projected no receipt")
	}
	if item.ID != "event-1" || item.RealtimeSessionID != "voice" || item.ObservedAtMS == nil || *item.ObservedAtMS != 1500 {
		t.Fatalf("receipt identity = %#v", item)
	}
	content := item.Content
	if content.Kind != RealtimeItemKindTurnInputAccepted || content.HandoffID != "handoff-1" ||
		content.TurnID != "turn-1" || content.HandoffMode != RealtimeHandoffModeSteer ||
		content.InputItemID != "item-1" || content.Input != "fix the flaky test" ||
		len(content.SourceEvents) != 2 || content.SourceEvents[1] != "b" {
		t.Fatalf("receipt content = %#v", content)
	}
}

func TestResponseItemProjectsSessionInputChunkLikeRust(t *testing.T) {
	payload := handoffMessagePayload(t, "item-2", "", "checking staging")
	metadata := json.RawMessage(`{"realtime_session_input":{"event_id":"event-2","realtime_session_id":"voice","observed_at_ms":2500,"source_event_ids":["c"]}}`)
	item, ok := RealtimeItemFromResponseItem(payload, metadata)
	if !ok || item == nil {
		t.Fatal("chunk message projected no receipt")
	}
	content := item.Content
	if content.Kind != RealtimeItemKindSessionInput || content.InputReason != RealtimeSessionInputReasonChunk ||
		content.InputItemID != "item-2" || content.Input != "checking staging" ||
		len(content.SourceEvents) != 1 || content.SourceEvents[0] != "c" {
		t.Fatalf("receipt content = %#v", content)
	}
}

func TestResponseItemReceiptRequiresProvenanceAndUserTextLikeRust(t *testing.T) {
	payload := handoffMessagePayload(t, "item-3", "turn-1", "hello")
	if _, ok := RealtimeItemFromResponseItem(payload, nil); ok {
		t.Fatal("message without provenance projected a receipt")
	}
	handoff := json.RawMessage(`{"realtime_handoff":{"event_id":"event-3","realtime_session_id":"voice","observed_at_ms":1,"handoff_id":"h","mode":"start","source_event_ids":[]}}`)
	// A handoff needs the turn it entered.
	if _, ok := RealtimeItemFromResponseItem(handoffMessagePayload(t, "item-4", "", "hello"), handoff); ok {
		t.Fatal("handoff without a turn projected a receipt")
	}
	// Unknown modes are not receipt modes.
	badMode := json.RawMessage(`{"realtime_handoff":{"event_id":"event-5","realtime_session_id":"voice","observed_at_ms":1,"handoff_id":"h","mode":"resume","source_event_ids":[]}}`)
	if _, ok := RealtimeItemFromResponseItem(payload, badMode); ok {
		t.Fatal("unknown handoff mode projected a receipt")
	}
	// Multi-part content has no single text the receipt could own.
	multi := json.RawMessage(`{"type":"message","id":"item-6","role":"user","content":[{"type":"input_text","text":"a"},{"type":"input_text","text":"b"}]}`)
	if _, ok := RealtimeItemFromResponseItem(multi, handoff); ok {
		t.Fatal("multi-part message projected a receipt")
	}
	// Only user messages carry accepted input.
	assistant := json.RawMessage(`{"type":"message","id":"item-7","role":"assistant","content":[{"type":"input_text","text":"a"}],"internal_chat_message_metadata_passthrough":{"turn_id":"turn-1"}}`)
	if _, ok := RealtimeItemFromResponseItem(assistant, handoff); ok {
		t.Fatal("assistant message projected a receipt")
	}
}

func TestResponseItemObservedAtRestoresClockLikeRust(t *testing.T) {
	metadata := json.RawMessage(`{"realtime_handoff":{"event_id":"e","realtime_session_id":"voice","observed_at_ms":4200,"handoff_id":"h","mode":"start","source_event_ids":[]},"history_sequence":3}`)
	if got := RealtimeObservedAtMS(metadata); got == nil || *got != 4200 {
		t.Fatalf("observed at = %v, want 4200", got)
	}
	chunk := json.RawMessage(`{"realtime_session_input":{"event_id":"e2","realtime_session_id":"voice","observed_at_ms":4300,"source_event_ids":[]}}`)
	if got := RealtimeObservedAtMS(chunk); got == nil || *got != 4300 {
		t.Fatalf("chunk observed at = %v, want 4300", got)
	}
	if got := RealtimeObservedAtMS(json.RawMessage(`{"history_sequence":1}`)); got != nil {
		t.Fatalf("plain metadata observed at = %v, want nil", *got)
	}
}
