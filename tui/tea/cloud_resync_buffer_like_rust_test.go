package tea

import (
	"testing"

	"codex_go/protocol"
	codextui "codex_go/tui"
)

// TestCloudResyncBuffersLiveEventsLikeRust mirrors Rust #63211: a reply that
// arrives on the live stream while the conversation is being refetched waits
// until that history installs, so the replacement page cannot swallow it.
func TestCloudResyncBuffersLiveEventsLikeRust(t *testing.T) {
	resume := func(codextui.SessionSelection) (SessionResumeResponse, error) {
		return SessionResumeResponse{
			Messages: []codextui.Message{{Role: "assistant", Text: "reply saved while disconnected"}},
		}, nil
	}
	model, _ := cloudResyncModel(t, resume)
	cmd := model.applyCloudStreamReconnected(CloudStreamReconnectedMsg{ThreadID: "cloud-thread"})
	if cmd == nil {
		t.Fatal("reconnect did not start a background reattach")
	}
	if model.cloudResyncThreadID != "cloud-thread" {
		t.Fatalf("cloudResyncThreadID = %q, want the fetched conversation", model.cloudResyncThreadID)
	}
	model.Update(ThreadEventMsg{Event: protocol.AgentMessageDelta("live-1", "live reply")})
	if modelTranscriptHasText(model, "live reply") {
		t.Fatal("live event repainted the transcript before the history installed")
	}
	if len(model.cloudResyncHeldEvents) != 1 {
		t.Fatalf("held events = %d, want the live reply buffered", len(model.cloudResyncHeldEvents))
	}

	if out := model.applyCloudConversationResync(cmd().(CloudConversationResyncMsg)); out != nil {
		out()
	}
	if !modelTranscriptHasText(model, "reply saved while disconnected") {
		t.Fatal("reattached history did not install the saved reply")
	}
	if !modelTranscriptHasText(model, "live reply") {
		t.Fatal("buffered live reply was never applied after the history installed")
	}
	if model.cloudResyncThreadID != "" || len(model.cloudResyncHeldEvents) != 0 {
		t.Fatalf("buffering survived the settle: thread %q, %d events", model.cloudResyncThreadID, len(model.cloudResyncHeldEvents))
	}
}

// TestCloudResyncKeepsBufferedEventsWhenHistoryUnavailableLikeRust keeps the
// live events when the refetch cannot be verified: sending stays paused, but a
// reply the user already saw on the stream is not dropped.
func TestCloudResyncKeepsBufferedEventsWhenHistoryUnavailableLikeRust(t *testing.T) {
	resume := func(codextui.SessionSelection) (SessionResumeResponse, error) {
		return SessionResumeResponse{HasOlderHistory: true}, nil
	}
	model, submitted := cloudResyncModel(t, resume)
	cmd := model.applyCloudStreamReconnected(CloudStreamReconnectedMsg{ThreadID: "cloud-thread"})
	if cmd == nil {
		t.Fatal("reconnect did not start a background reattach")
	}
	model.Update(ThreadEventMsg{Event: protocol.AgentMessageDelta("live-2", "live reply")})
	if out := model.applyCloudConversationResync(cmd().(CloudConversationResyncMsg)); out != nil {
		out()
	}
	if len(*submitted) != 0 || model.cloudStreamDisconnectedThreadID != "cloud-thread" {
		t.Fatal("unverified history released the sending hold")
	}
	if !modelTranscriptHasText(model, "live reply") {
		t.Fatal("buffered live reply was dropped when the history stayed unavailable")
	}
	if model.cloudResyncThreadID != "" {
		t.Fatalf("cloudResyncThreadID = %q, want the buffering ended", model.cloudResyncThreadID)
	}
}

// TestCloudResyncIgnoresOtherThreadsEventsLikeRust keeps another conversation's
// live events flowing while one thread's history is refetched.
func TestCloudResyncIgnoresOtherThreadsEventsLikeRust(t *testing.T) {
	resume := func(codextui.SessionSelection) (SessionResumeResponse, error) {
		return SessionResumeResponse{}, nil
	}
	model, _ := cloudResyncModel(t, resume)
	if cmd := model.applyCloudStreamReconnected(CloudStreamReconnectedMsg{ThreadID: "cloud-thread"}); cmd == nil {
		t.Fatal("reconnect did not start a background reattach")
	}
	model.Update(ThreadEventMsg{Event: protocol.ThreadEvent{Type: "thread/statusChanged", ThreadID: "other-thread"}})
	if len(model.cloudResyncHeldEvents) != 0 {
		t.Fatalf("held events = %d, want another thread's events applied", len(model.cloudResyncHeldEvents))
	}
}
