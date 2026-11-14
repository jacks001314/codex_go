package tea

import (
	bubbletea "charm.land/bubbletea/v2"
	"codex_go/protocol"
	"testing"
)

func TestLateReceiptReleasesResumedQueueExactlyOnceLikeRust(t *testing.T) {
	model := newPendingSteerModel(t, "thread-a", nil)
	first := submitPendingSteer(t, model, "first")
	second := submitPendingSteer(t, model, "second")
	model.queued = []queuedSubmission{{Request: SubmitRequest{Prompt: "next", ClientID: "next-id"}}}
	model.applyResumeResponse("thread-a", SessionResumeResponse{Status: "idle"})
	calls := 0
	model.onSubmitRequest = func(request SubmitRequest) bubbletea.Cmd {
		calls++
		if model.currentThreadID() != "thread-a" || request.ClientID != "next-id" {
			t.Fatal("wrong thread or input")
		}
		return func() bubbletea.Msg { return nil }
	}
	receipt := func(id string) protocol.ThreadEvent {
		return protocol.ItemCompleted(protocol.ThreadItem{Type: "user_message", ClientID: id, Text: id})
	}
	model.Update(ThreadScopedEventMsg{ThreadID: "thread-b", Event: receipt(first)})
	model.Update(ThreadEventMsg{Event: receipt("unknown")})
	if calls != 0 || len(model.pendingSteers) != 2 {
		t.Fatal("unrelated receipt changed pending input")
	}
	model.Update(ThreadEventMsg{Event: receipt(first)})
	if calls != 0 || len(model.pendingSteers) != 1 {
		t.Fatal("partial receipt released the queue")
	}
	model.Update(ThreadEventMsg{Event: receipt(second)})
	if calls != 1 || len(model.queued) != 0 {
		t.Fatalf("final receipt submissions=%d queued=%d", calls, len(model.queued))
	}
	model.Update(ThreadEventMsg{Event: receipt(second)})
	if calls != 1 {
		t.Fatal("duplicate receipt submitted again")
	}
}

func TestLateReceiptStillHonorsDestinationQueueGatesLikeRust(t *testing.T) {
	for _, readOnly := range []bool{false, true} {
		model := newPendingSteerModel(t, "thread-a", nil)
		id := submitPendingSteer(t, model, "uncertain")
		model.queued = []queuedSubmission{{Request: SubmitRequest{Prompt: "next", ClientID: "next-id"}}}
		status := "running"
		if readOnly {
			status = "idle"
		}
		model.applyResumeResponse("thread-a", SessionResumeResponse{Status: status, ReadOnly: readOnly})
		calls := 0
		model.onSubmitRequest = func(SubmitRequest) bubbletea.Cmd { calls++; return nil }
		model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(protocol.ThreadItem{Type: "user_message", ClientID: id})})
		if calls != 0 || len(model.queued) != 1 || len(model.pendingSteers) != 0 {
			t.Fatalf("readOnly=%v calls=%d queued=%d pending=%d", readOnly, calls, len(model.queued), len(model.pendingSteers))
		}
	}
}
