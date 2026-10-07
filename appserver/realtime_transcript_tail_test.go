package appserver

import (
	"strings"
	"testing"

	"codex_go/realtime"
	"codex_go/session"
	"codex_go/turn"
)

// newRealtimeTailRouter builds the same fixture the neighboring realtime tests
// use: an isolated store, a notification buffer, and an agent that blocks for
// the whole turn so an active turn can be observed.
func newRealtimeTailRouter(t *testing.T) (*RuntimeRouter, *session.Store, *NotificationBuffer, *blockingAgent) {
	t.Helper()
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	agent := newBlockingAgent()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
	})
	t.Cleanup(func() { _ = router.Close() })
	router.SetNotificationSink(sink)
	return router, store, sink, agent
}

func startRealtimeTailThread(t *testing.T, router *RuntimeRouter) string {
	t.Helper()
	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir()}))
	if threadStart.Error != nil {
		t.Fatalf("thread/start: %+v", threadStart.Error)
	}
	return threadStart.Result.(*ThreadStartResponse).Thread.ID
}

// Rust #50531 (core/src/realtime_conversation/transcript_tail.rs): saving the
// terminal realtime transcript must land in thread history without triggering
// another model request. With no turn running the tail becomes its own
// completed history turn instead of starting a new one.
func TestRealtimeTranscriptTailFlushRecordsHistoryWithoutInferenceLikeRust(t *testing.T) {
	router, store, sink, agent := newRealtimeTailRouter(t)
	threadID := startRealtimeTailThread(t, router)

	router.handleRealtimeEvent(threadID, realtime.Event{
		Type:             "transcript_tail.flush",
		ActiveTranscript: []realtime.TranscriptEntry{{Role: "user", Text: "remaining tail"}},
	})

	record, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatalf("read realtime thread: %v", err)
	}
	var recorded *session.Item
	for index := range record.Items {
		item := &record.Items[index]
		if item.Metadata["kind"] == realtimeTranscriptTailKind {
			recorded = item
			break
		}
	}
	if recorded == nil {
		t.Fatalf("transcript tail was not persisted into thread history: %#v", record.Items)
	}
	if recorded.Role != "user" || recorded.Text != "remaining tail" {
		t.Fatalf("recorded tail = role %q text %q, want user/%q", recorded.Role, recorded.Text, "remaining tail")
	}
	if recorded.Metadata["source"] != "transcript_tail_flush" {
		t.Fatalf("recorded tail source = %#v", recorded.Metadata["source"])
	}
	if recorded.Metadata["turnId"] == nil || strings.TrimSpace(recorded.Metadata["turnId"].(string)) == "" {
		t.Fatalf("recorded tail turnId = %#v, want a completed history turn", recorded.Metadata["turnId"])
	}

	// The tail must never trigger inference.
	if active := router.activeRuntimeTurnSnapshot(threadID); active != nil {
		t.Fatalf("transcript tail started a running turn: %#v", active)
	}
	for _, notification := range sink.List() {
		if notification.Method == NotificationTurnStarted {
			t.Fatalf("transcript tail emitted turn/started: %#v", notification.Params)
		}
	}
	select {
	case <-agent.started:
		t.Fatal("transcript tail invoked the model agent")
	default:
	}

	completed, ok := lastRealtimeTailItemCompleted(sink, recorded.ID)
	if !ok {
		t.Fatal("transcript tail did not publish item/completed for the recorded message")
	}
	if completed.TurnID != recorded.Metadata["turnId"] {
		t.Fatalf("item/completed turnID = %q, want %v", completed.TurnID, recorded.Metadata["turnId"])
	}
}

// Rust #50531: while a model turn is already running the tail joins that turn's
// history instead of steering it, so the closure never adds a sampling step.
func TestRealtimeTranscriptTailFlushJoinsActiveTurnWithoutSteeringLikeRust(t *testing.T) {
	router, store, sink, agent := newRealtimeTailRouter(t)
	threadID := startRealtimeTailThread(t, router)

	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "long-running task",
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn/start: %+v", turnStart.Error)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	waitForBlockingAgentStart(t, agent)

	router.handleRealtimeEvent(threadID, realtime.Event{
		Type:             "transcript_tail.flush",
		ActiveTranscript: []realtime.TranscriptEntry{{Role: "user", Text: "wrap up now"}},
	})

	active := router.activeRuntimeTurnSnapshot(threadID)
	if active == nil || active.ID != turnID {
		t.Fatalf("active turn = %#v, want %q", active, turnID)
	}
	record, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatalf("read realtime thread: %v", err)
	}
	joined := false
	for index := range record.Items {
		item := &record.Items[index]
		if item.Metadata["steered"] == true {
			t.Fatalf("transcript tail steered the active turn: %#v", item)
		}
		if item.Metadata["kind"] == realtimeTranscriptTailKind && item.Text == "wrap up now" {
			if item.Metadata["turnId"] != turnID {
				t.Fatalf("recorded tail turnId = %#v, want %q", item.Metadata["turnId"], turnID)
			}
			joined = true
		}
	}
	if !joined {
		t.Fatalf("transcript tail did not join the active turn history: %#v", record.Items)
	}
	startedCount := 0
	for _, notification := range sink.List() {
		if notification.Method == NotificationTurnStarted {
			started, ok := notification.Params.(*TurnStartedNotification)
			if ok && started != nil && started.ThreadID == threadID {
				startedCount++
			}
		}
	}
	if startedCount != 1 {
		t.Fatalf("turn/started count = %d, want 1 (the tail must not add a turn)", startedCount)
	}
}

func lastRealtimeTailItemCompleted(sink *NotificationBuffer, itemID string) (*ItemCompletedNotification, bool) {
	var found *ItemCompletedNotification
	for _, notification := range sink.List() {
		if notification.Method != NotificationItemCompleted {
			continue
		}
		completed, ok := notification.Params.(*ItemCompletedNotification)
		if !ok || completed == nil {
			continue
		}
		if id, _ := completed.Item["id"].(string); id == itemID {
			found = completed
		}
	}
	return found, found != nil
}
