package appserver

// Mirrors Rust #62251's producer side: the live voice path stamps the ordinary
// model user message with realtime provenance, so the receipt is derived from
// the message instead of stored twice.

import (
	"encoding/json"
	"strings"
	"testing"

	"codex_go/realtime"
	"codex_go/session"
	"codex_go/turn"
)

func harnessMetadataOf(t *testing.T, item *session.Item) map[string]any {
	t.Helper()
	if item == nil {
		t.Fatal("missing session item")
	}
	if item.Data == nil {
		return nil
	}
	var raw json.RawMessage
	switch value := item.Data["harness_metadata"].(type) {
	case json.RawMessage:
		raw = value
	case map[string]any:
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		raw = encoded
	default:
		t.Fatalf("harness metadata = %#v", item.Data)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode harness metadata: %v", err)
	}
	return decoded
}

func projectReceiptOf(t *testing.T, item *session.Item) *realtime.RealtimeItem {
	t.Helper()
	payload, err := json.Marshal(session.InputItemFromItem(item, nil))
	if err != nil {
		t.Fatal(err)
	}
	metadata := harnessMetadataOf(t, item)
	encoded, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	receipt, ok := realtime.RealtimeItemFromResponseItem(payload, encoded)
	if !ok || receipt == nil {
		t.Fatalf("message carried no projectable receipt: payload=%s metadata=%s", payload, encoded)
	}
	return receipt
}

func TestRealtimeHandoffStampsProvenanceOnAcceptedInputLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	agent := newBlockingAgent()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
	})
	t.Cleanup(func() { _ = router.Close() })

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir()}))
	if threadStart.Error != nil {
		t.Fatalf("thread/start: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	router.handleRealtimeEvent(threadID, realtime.Event{
		Type:              "handoff.requested",
		HandoffID:         "delegation-start",
		ItemID:            "item-start",
		RealtimeSessionID: "voice-1",
		InputTranscript:   "start the long-running task",
	})
	waitForBlockingAgentStart(t, agent)

	record, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatalf("read thread: %v", err)
	}
	var accepted *session.Item
	for index := range record.Items {
		item := &record.Items[index]
		if item.Role == "user" && strings.Contains(item.Text, "start the long-running task") {
			accepted = item
			break
		}
	}
	if accepted == nil {
		t.Fatal("handoff input was not recorded as a user message")
	}
	provenance, _ := harnessMetadataOf(t, accepted)["realtime_handoff"].(map[string]any)
	if provenance == nil {
		t.Fatal("handoff user message carries no realtime_handoff provenance")
	}
	if provenance["handoff_id"] != "delegation-start" || provenance["mode"] != "start" || provenance["realtime_session_id"] != "voice-1" {
		t.Fatalf("handoff provenance = %#v", provenance)
	}
	receipt := projectReceiptOf(t, accepted)
	if receipt.Content.Kind != realtime.RealtimeItemKindTurnInputAccepted ||
		receipt.Content.HandoffID != "delegation-start" ||
		receipt.Content.HandoffMode != realtime.RealtimeHandoffModeStart ||
		!strings.Contains(receipt.Content.Input, "start the long-running task") ||
		receipt.ObservedAtMS == nil {
		t.Fatalf("projected receipt = %#v", receipt)
	}

	router.handleRealtimeEvent(threadID, realtime.Event{
		Type:              "handoff.requested",
		HandoffID:         "delegation-steer",
		ItemID:            "item-steer",
		RealtimeSessionID: "voice-1",
		InputTranscript:   "change direction now",
	})
	record, err = store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatalf("read steered thread: %v", err)
	}
	var steered *session.Item
	for index := range record.Items {
		item := &record.Items[index]
		if item.Metadata["steered"] == true && strings.Contains(item.Text, "change direction now") {
			steered = item
			break
		}
	}
	if steered == nil {
		t.Fatal("steer was not recorded")
	}
	steerReceipt := projectReceiptOf(t, steered)
	if steerReceipt.Content.HandoffID != "delegation-steer" || steerReceipt.Content.HandoffMode != realtime.RealtimeHandoffModeSteer {
		t.Fatalf("steer receipt = %#v", steerReceipt.Content)
	}
}

func TestRealtimeTranscriptChunkStampsSavedInputProvenanceLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		ThreadStatus: NewThreadStatusManager(),
	})
	t.Cleanup(func() { _ = router.Close() })

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir()}))
	if threadStart.Error != nil {
		t.Fatalf("thread/start: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	item, err := router.recordRealtimeTranscriptMessage(threadID, "checking staging", realtimeTranscriptChunkKind, "transcript_delta", "voice-2", []string{"frame-1"})
	if err != nil || item == nil {
		t.Fatalf("record chunk = %#v, %v", item, err)
	}
	provenance, _ := harnessMetadataOf(t, item)["realtime_session_input"].(map[string]any)
	if provenance == nil {
		t.Fatal("chunk user message carries no realtime_session_input provenance")
	}
	sources, _ := provenance["source_event_ids"].([]any)
	if provenance["realtime_session_id"] != "voice-2" || len(sources) != 1 || sources[0] != "frame-1" {
		t.Fatalf("chunk provenance = %#v", provenance)
	}
	receipt := projectReceiptOf(t, item)
	if receipt.Content.Kind != realtime.RealtimeItemKindSessionInput ||
		receipt.Content.InputReason != realtime.RealtimeSessionInputReasonChunk ||
		receipt.Content.Input != "checking staging" ||
		receipt.ObservedAtMS == nil {
		t.Fatalf("chunk receipt = %#v", receipt)
	}

	// A tail flush closes the session; it must not claim a chunk receipt.
	tail, err := router.recordRealtimeTranscriptMessage(threadID, "final words", realtimeTranscriptTailKind, "transcript_tail_flush", "voice-2", nil)
	if err != nil || tail == nil {
		t.Fatalf("record tail = %#v, %v", tail, err)
	}
	if _, present := harnessMetadataOf(t, tail)["realtime_session_input"]; present {
		t.Fatal("tail flush carried chunk provenance")
	}
}
