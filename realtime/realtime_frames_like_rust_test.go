package realtime

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Mirrors Rust realtime_tests.rs: V1 frames round-trip and require their voice
// payload.
func TestRealtimeFrameRoundTripsAndRequiresVoicePayloadLikeRust(t *testing.T) {
	const wire = `{"id":"speech","realtimeSessionId":"voice","observedAtMs":1050,"type":"frame","channels":{"voice_transcript":{"role":"assistant","text":"hello"}}}`
	var item RealtimeItem
	if err := json.Unmarshal([]byte(wire), &item); err != nil {
		t.Fatalf("unmarshal frame: %v", err)
	}
	if item.ObservedAtMS == nil || *item.ObservedAtMS != 1050 || item.Content.Kind != RealtimeItemKindFrame {
		t.Fatalf("frame item = %#v", item)
	}
	encoded, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("marshal frame: %v", err)
	}
	if string(encoded) != wire {
		t.Fatalf("frame round trip = %s, want %s", encoded, wire)
	}

	const rollout = `{"id":"speech","realtime_session_id":"voice","observed_at_ms":1050,"type":"frame","channels":{"voice_transcript":{"role":"assistant","text":"hello"}}}`
	var persisted RealtimeItem
	if err := persisted.UnmarshalRolloutJSON([]byte(rollout)); err != nil {
		t.Fatalf("unmarshal rollout frame: %v", err)
	}
	persistedJSON, err := persisted.MarshalRolloutJSON()
	if err != nil || string(persistedJSON) != rollout {
		t.Fatalf("rollout frame round trip = %s, %v", persistedJSON, err)
	}

	withoutVoice := strings.Replace(wire, `"channels":{"voice_transcript":{"role":"assistant","text":"hello"}}`, `"channels":{}`, 1)
	if err := json.Unmarshal([]byte(withoutVoice), &RealtimeItem{}); err == nil {
		t.Fatal("frame without voice_transcript decoded")
	}
}

// Mirrors Rust TurnInputAccepted / SessionEndInputSaved wire support and their
// exclusion from live item notifications.
func TestRealtimeReceiptsRoundTripAndStayOutOfLiveNotificationsLikeRust(t *testing.T) {
	const wire = `{"id":"accept-1","realtimeSessionId":"voice","type":"turnInputAccepted","handoffId":"h1","turnId":"t1","mode":"steer","sourceEventIds":["a","b"],"inputItemId":"i1","input":"go"}`
	var item RealtimeItem
	if err := json.Unmarshal([]byte(wire), &item); err != nil {
		t.Fatalf("unmarshal turn input receipt: %v", err)
	}
	if item.Content.HandoffID != "h1" || item.Content.HandoffMode != RealtimeHandoffModeSteer ||
		len(item.Content.SourceEvents) != 2 || item.Content.InputItemID != "i1" {
		t.Fatalf("turn input receipt = %#v", item.Content)
	}
	encoded, err := json.Marshal(item)
	if err != nil || string(encoded) != wire {
		t.Fatalf("turn input receipt round trip = %s, %v", encoded, err)
	}

	const endWire = `{"id":"end-1","realtimeSessionId":"voice","type":"sessionEndInputSaved","sourceEventIds":[],"inputItemId":"i2","input":"bye"}`
	var endItem RealtimeItem
	if err := json.Unmarshal([]byte(endWire), &endItem); err != nil {
		t.Fatalf("unmarshal session end receipt: %v", err)
	}
	endEncoded, err := json.Marshal(endItem)
	if err != nil || string(endEncoded) != endWire {
		t.Fatalf("session end receipt round trip = %s, %v", endEncoded, err)
	}
	if notifications := historyLifecycleNotifications("thread-1", []RealtimeItem{item, endItem}); len(notifications) != 0 {
		t.Fatalf("receipt notifications = %#v, want none", notifications)
	}
}

// Mirrors Rust ObservationClock: clock rollback never moves a later
// observation backward and completion retains the first-observed timestamp.
func TestRealtimeObservationClockNeverMovesBackwardLikeRust(t *testing.T) {
	state := &RealtimeHistoryState{}
	state.SetFramesEnabled(true)
	now := int64(100)
	state.SetObservationClock(func() int64 { return now })
	state.SetIDGenerator(sequenceIDs("id"))

	start := state.StartSession("voice")
	if len(start.Items) != 1 || start.Items[0].ObservedAtMS == nil || *start.Items[0].ObservedAtMS != 100 {
		t.Fatalf("session start = %#v", start.Items)
	}
	now = 90
	done := state.TranscriptDone(RealtimeTranscriptRoleUser, "hello")
	if len(done.Items) != 1 || done.Items[0].ObservedAtMS == nil || *done.Items[0].ObservedAtMS != 100 {
		t.Fatalf("rolled-back transcript = %#v", done.Items)
	}
	now = 110
	closed := state.SessionClosed()
	if !closed.SealFrames || len(closed.Items) != 1 || closed.Items[0].ObservedAtMS == nil || *closed.Items[0].ObservedAtMS != 110 {
		t.Fatalf("session close = %#v seal=%v", closed.Items, closed.SealFrames)
	}
}

// Mirrors the enrolled-thread frame spelling: deterministic session-start IDs,
// voice frames, and observed timestamps. Unenrolled threads stay legacy.
func TestRealtimeFramesChangeCanonicalSpellingLikeRust(t *testing.T) {
	state := &RealtimeHistoryState{}
	state.SetFramesEnabled(true)
	state.SetObservationClock(func() int64 { return 1050 })
	state.SetIDGenerator(sequenceIDs("frame"))
	effects := state.StartSession("call-1")
	if got := effects.Items[0].ID; got != "realtime-session-start:call-1" {
		t.Fatalf("session start id = %q", got)
	}
	transcript := state.TranscriptDone(RealtimeTranscriptRoleAssistant, "hello")
	if len(transcript.Items) != 1 {
		t.Fatalf("transcript items = %#v", transcript.Items)
	}
	if transcript.Items[0].Content.Kind != RealtimeItemKindFrame || transcript.Items[0].Content.Text != "hello" {
		t.Fatalf("frame item = %#v", transcript.Items[0])
	}
	if transcript.Items[0].ObservedAtMS == nil || *transcript.Items[0].ObservedAtMS != 1050 {
		t.Fatalf("frame observed = %#v", transcript.Items[0])
	}
	closed := state.SessionClosed()
	if !closed.SealFrames || len(closed.Items) != 1 || closed.Items[0].Content.Kind != RealtimeItemKindSessionClosed {
		t.Fatalf("session close = %#v seal=%v", closed.Items, closed.SealFrames)
	}

	legacy := &RealtimeHistoryState{}
	legacy.SetFramesEnabled(false)
	legacyEffects := legacy.StartSession("legacy-call")
	if legacyEffects.Items[0].ObservedAtMS != nil || legacyEffects.Items[0].ID == "realtime-session-start:legacy-call" {
		t.Fatalf("legacy session start = %#v", legacyEffects.Items[0])
	}
}

func TestObservationClockFloorRestoresFromCheckpointLikeRust(t *testing.T) {
	state := &RealtimeHistoryState{}
	floor := int64(150)
	state.RestoreObservationClockFloor(&floor)
	now := int64(100)
	state.SetObservationClock(func() int64 { return now })
	if got := state.observeNowLockedForTest(); got != 150 {
		t.Fatalf("observation after restore = %d, want 150", got)
	}
	now = 200
	if got := state.observeNowLockedForTest(); got != 200 {
		t.Fatalf("observation after wall advance = %d, want 200", got)
	}
	if got := state.ObservationClockFloor(); got == nil || *got != 200 {
		t.Fatalf("floor = %v, want 200", got)
	}
}

func (s *RealtimeHistoryState) observeNowLockedForTest() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.observeNow()
}

func sequenceIDs(prefix string) func() string {
	index := 0
	return func() string {
		index++
		return prefix + "-" + string(rune('0'+index%10))
	}
}

// Mirrors Rust #61473: every new call gets its own default session ID, an
// existing call keeps the call ID, and an explicit override always wins.
func TestStartParamsAssignPerCallSessionIDsLikeRust(t *testing.T) {
	newCall := func() string {
		t.Helper()
		config, err := (&StartParams{ThreadID: "thread-1", OutputModality: OutputAudio}).Normalized("", "", "")
		if err != nil {
			t.Fatalf("Normalized() error = %v", err)
		}
		return config.RealtimeSessionID
	}
	first, second := newCall(), newCall()
	if first == "" || first == "thread-1" {
		t.Fatalf("default session id = %q", first)
	}
	if first == second {
		t.Fatalf("two calls shared session id %q", first)
	}
	if _, err := uuid.Parse(first); err != nil {
		t.Fatalf("default session id %q is not a UUID: %v", first, err)
	}

	noStartupContext := false
	existing, err := (&StartParams{
		ThreadID:              "thread-1",
		OutputModality:        OutputAudio,
		IncludeStartupContext: &noStartupContext,
		Transport:             ExistingCallTransport("call-9"),
	}).Normalized("", "", "")
	if err != nil {
		t.Fatalf("Normalized(existing call) error = %v", err)
	}
	if existing.RealtimeSessionID != "call-9" {
		t.Fatalf("existing-call session id = %q, want call-9", existing.RealtimeSessionID)
	}

	override := "explicit-session"
	pinned, err := (&StartParams{
		ThreadID:              "thread-1",
		OutputModality:        OutputAudio,
		IncludeStartupContext: &noStartupContext,
		RealtimeSessionID:     &override,
		Transport:             ExistingCallTransport("call-9"),
	}).Normalized("", "", "")
	if err != nil {
		t.Fatalf("Normalized(override) error = %v", err)
	}
	if pinned.RealtimeSessionID != override {
		t.Fatalf("override session id = %q, want %q", pinned.RealtimeSessionID, override)
	}
}

// Mirrors Rust #61473: enrolled frames ask the host for a flush barrier and a
// failed barrier suppresses the completion acknowledgement.
func TestFrameSealFlushFailureSuppressesCompletionLikeRust(t *testing.T) {
	manager := NewManager()
	manager.SetFrameEnrollment(func(string) bool { return true })
	seals := []bool{}
	manager.SetHistoryItemSink(func(_ string, _ []RealtimeItem, sealFrames bool) error {
		seals = append(seals, sealFrames)
		return errors.New("disk full")
	})
	history := manager.historyFor("thread-a")

	if notifications := manager.publishHistoryEffects("thread-a", history.StartSession("call-1")); len(notifications) != 0 {
		t.Fatalf("start notifications on failed flush = %#v", notifications)
	}
	if len(seals) != 1 || !seals[0] {
		t.Fatalf("seal flags = %v, want [true]", seals)
	}
	stream := manager.observeTimelineEvent("thread-a", Event{Type: "input_transcript.delta", Delta: "hi"})
	if len(stream) == 0 {
		t.Fatal("live transcript stream was suppressed with the completion")
	}
	for _, notification := range stream {
		if notification.Method == NotificationItemCompleted {
			t.Fatalf("completion survived a failed flush: %#v", notification)
		}
	}
	if len(seals) != 1 || !seals[0] {
		t.Fatalf("seal flags = %v, want [true]", seals)
	}

	flushed := NewManager()
	flushed.SetFrameEnrollment(func(string) bool { return true })
	sawSeal := false
	flushed.SetHistoryItemSink(func(_ string, _ []RealtimeItem, sealFrames bool) error {
		sawSeal = sawSeal || sealFrames
		return nil
	})
	flushedHistory := flushed.historyFor("thread-b")
	flushed.publishHistoryEffects("thread-b", flushedHistory.StartSession("call-2"))
	flushed.observeTimelineEvent("thread-b", Event{Type: "input_transcript.delta", Delta: "hi"})
	completed := flushed.observeTimelineEvent("thread-b", Event{Type: "input_transcript.done", Text: "hi"})
	if !sawSeal {
		t.Fatal("frame persistence never requested a flush barrier")
	}
	if !hasNotification(completed, NotificationItemCompleted) {
		t.Fatalf("acknowledged transcript = %#v", completed)
	}
}

func hasNotification(notifications []Notification, method NotificationMethod) bool {
	for _, notification := range notifications {
		if notification.Method == method {
			return true
		}
	}
	return false
}
