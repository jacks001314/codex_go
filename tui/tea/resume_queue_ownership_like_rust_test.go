package tea

import (
	codextui "codex_go/tui"
	"testing"
)

func TestResumeKeepsQueuedInputOnOriginalThreadLikeRust(t *testing.T) {
	model := newPendingSteerModel(t, "thread-a", nil)
	model.queued = []queuedSubmission{{Request: SubmitRequest{Prompt: "follow a", ClientID: "queue-a"}}}
	model.rejectedSteers = []queuedSubmission{{Request: SubmitRequest{Prompt: "retry a", ClientID: "retry-a"}}}
	model.applyResumeResponse("thread-b", SessionResumeResponse{Status: "idle"})
	if len(model.queued) != 0 || len(model.rejectedSteers) != 0 {
		t.Fatal("thread-a input leaked onto thread-b")
	}
	model.queued = []queuedSubmission{{Request: SubmitRequest{Prompt: "follow b", ClientID: "queue-b"}}}
	model.applyResumeResponse("thread-a", SessionResumeResponse{Status: "idle"})
	if len(model.queued) != 1 || model.queued[0].Request.ClientID != "queue-a" {
		t.Fatalf("restored queue=%#v", model.queued)
	}
	if len(model.rejectedSteers) != 1 || model.rejectedSteers[0].Request.ClientID != "retry-a" {
		t.Fatalf("restored retries=%#v", model.rejectedSteers)
	}
	model.applyResumeResponse("thread-b", SessionResumeResponse{Status: "idle", Messages: []codextui.Message{{Role: codextui.RoleUser, ClientID: "queue-b"}}})
	if len(model.queued) != 0 {
		t.Fatal("confirmed thread-b input remains queued")
	}
	model.applyResumeResponse("thread-a", SessionResumeResponse{Status: "idle"})
	if len(model.queued) != 1 || len(model.rejectedSteers) != 1 {
		t.Fatal("thread-b receipts changed thread-a input")
	}
}

func TestResumedQueueWaitsForReceiptAndWritableThreadLikeRust(t *testing.T) {
	for _, readOnly := range []bool{false, true} {
		model := newPendingSteerModel(t, "thread-a", nil)
		id := submitPendingSteer(t, model, "uncertain")
		model.queued = []queuedSubmission{{Request: SubmitRequest{Prompt: "follow", ClientID: "follow"}}}
		model.applyResumeResponse("thread-a", SessionResumeResponse{Status: "idle", ReadOnly: readOnly})
		model.submitNextQueued()
		if len(model.queued) != 1 {
			t.Fatal("missing receipt allowed queue to advance")
		}
		model.applyResumeResponse("thread-a", SessionResumeResponse{Status: "idle", ReadOnly: readOnly, Messages: []codextui.Message{{Role: codextui.RoleUser, ClientID: id}}})
		model.submitNextQueued()
		if readOnly && len(model.queued) != 1 {
			t.Fatal("read-only thread consumed queued input")
		}
		if !readOnly && len(model.queued) != 0 {
			t.Fatal("confirmed writable thread did not release queued input")
		}
	}
}
