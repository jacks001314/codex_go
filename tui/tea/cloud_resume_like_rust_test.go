package tea

import (
	"errors"
	"testing"

	bubbletea "charm.land/bubbletea/v2"

	codextui "codex_go/tui"
)

// cloudResyncModel builds a Cloud conversation whose stream dropped and that
// has one held prompt (Rust #63211 CloudResume).
func cloudResyncModel(t *testing.T, resume SessionResumeFunc) (*Model, *[]SubmitRequest) {
	t.Helper()
	submitted := &[]SubmitRequest{}
	state := codextui.NewState(nil)
	state.SetThreadID("cloud-thread")
	state.CWD = "/workspace"
	model := NewModel(state, Options{
		Width:           100,
		Height:          30,
		OnResumeSession: resume,
		OnSubmitRequest: func(request SubmitRequest) bubbletea.Cmd {
			*submitted = append(*submitted, request)
			return nil
		},
	})
	model.Update(CloudStreamDisconnectedMsg{ThreadID: "cloud-thread"})
	model.queued = append(model.queued, queuedSubmission{Request: SubmitRequest{Prompt: "held prompt"}})
	return model, submitted
}

// TestCloudStreamReconnectReattachesHistoryLikeRust mirrors Rust #63211: the
// conversation is refetched on reconnect, replies saved while disconnected
// appear, and sending resumes.
func TestCloudStreamReconnectReattachesHistoryLikeRust(t *testing.T) {
	var selections []codextui.SessionSelection
	resume := func(selection codextui.SessionSelection) (SessionResumeResponse, error) {
		selections = append(selections, selection)
		return SessionResumeResponse{
			Messages: []codextui.Message{{Role: "assistant", Text: "reply saved while disconnected"}},
		}, nil
	}
	model, submitted := cloudResyncModel(t, resume)
	if cmd := model.applyCloudStreamReconnected(CloudStreamReconnectedMsg{ThreadID: "cloud-thread"}); cmd == nil {
		t.Fatal("reconnect did not start a background reattach")
	} else if len(*submitted) != 0 {
		t.Fatalf("sending resumed before the history was verified: %#v", *submitted)
	} else if msg := cmd(); msg != nil {
		if cmd := model.applyCloudConversationResync(msg.(CloudConversationResyncMsg)); cmd != nil {
			cmd()
		}
	}
	if len(selections) != 1 || selections[0].Target.ThreadID != "cloud-thread" || selections[0].Target.CWD != "/workspace" {
		t.Fatalf("resume selections = %#v", selections)
	}
	if !modelTranscriptHasText(model, "reply saved while disconnected") {
		t.Fatal("reattached history did not install the saved reply")
	}
	if model.cloudStreamDisconnectedThreadID != "" {
		t.Fatal("reattach kept the sending hold")
	}
	if len(*submitted) != 1 || (*submitted)[0].Prompt != "held prompt" {
		t.Fatalf("queued prompt after reattach = %#v", *submitted)
	}
}

// TestCloudStreamReconnectRejectsIncompleteHistoryLikeRust mirrors Rust
// #63211: history that is only a page keeps sending paused and preserves the
// draft/queue.
func TestCloudStreamReconnectRejectsIncompleteHistoryLikeRust(t *testing.T) {
	resume := func(codextui.SessionSelection) (SessionResumeResponse, error) {
		return SessionResumeResponse{HasOlderHistory: true}, nil
	}
	model, submitted := cloudResyncModel(t, resume)
	cmd := model.applyCloudStreamReconnected(CloudStreamReconnectedMsg{ThreadID: "cloud-thread"})
	if cmd == nil {
		t.Fatal("reconnect did not start a background reattach")
	}
	if out := model.applyCloudConversationResync(cmd().(CloudConversationResyncMsg)); out != nil {
		t.Fatal("incomplete history resumed sending")
	}
	if model.cloudStreamDisconnectedThreadID != "cloud-thread" {
		t.Fatal("incomplete history released the sending hold")
	}
	if model.notice != cloudHistoryUnavailableNotice {
		t.Fatalf("notice = %q, want the unavailable-history message", model.notice)
	}
	if len(*submitted) != 0 || len(model.queued) != 1 {
		t.Fatalf("incomplete history dropped the held prompt: submitted=%#v queued=%#v", *submitted, model.queued)
	}
}

// TestCloudStreamReconnectKeepsHoldOnHistoryErrorLikeRust mirrors Rust
// #63211: a failed history fetch keeps the conversation paused instead of
// accepting live events against a stale transcript.
func TestCloudStreamReconnectKeepsHoldOnHistoryErrorLikeRust(t *testing.T) {
	resume := func(codextui.SessionSelection) (SessionResumeResponse, error) {
		return SessionResumeResponse{}, errors.New("cloud history unavailable")
	}
	model, submitted := cloudResyncModel(t, resume)
	cmd := model.applyCloudStreamReconnected(CloudStreamReconnectedMsg{ThreadID: "cloud-thread"})
	if cmd == nil {
		t.Fatal("reconnect did not start a background reattach")
	}
	if out := model.applyCloudConversationResync(cmd().(CloudConversationResyncMsg)); out != nil {
		t.Fatal("failed history fetch resumed sending")
	}
	if model.cloudStreamDisconnectedThreadID != "cloud-thread" || len(*submitted) != 0 {
		t.Fatalf("failed history fetch released the hold: thread=%q submitted=%#v",
			model.cloudStreamDisconnectedThreadID, *submitted)
	}
	if model.composer.Value() != "" || len(model.queued) != 1 {
		t.Fatalf("failed history fetch lost the queued prompt: queued=%#v", model.queued)
	}
}

// TestCloudResyncRequiresShownConversationLikeRust mirrors Rust #63211: the
// refetched history must still contain the conversation already on screen.
func TestCloudResyncRequiresShownConversationLikeRust(t *testing.T) {
	resume := func(codextui.SessionSelection) (SessionResumeResponse, error) {
		return SessionResumeResponse{Messages: []codextui.Message{{Role: "assistant", Text: "unrelated page"}}}, nil
	}
	model, submitted := cloudResyncModel(t, resume)
	model.State.Messages = []codextui.Message{{Role: codextui.RoleAssistant, Text: "already shown", ItemID: "shown-item"}}
	cmd := model.applyCloudStreamReconnected(CloudStreamReconnectedMsg{ThreadID: "cloud-thread"})
	if cmd == nil {
		t.Fatal("reconnect did not start a background reattach")
	}
	if out := model.applyCloudConversationResync(cmd().(CloudConversationResyncMsg)); out != nil {
		t.Fatal("history without the shown conversation resumed sending")
	}
	if model.cloudStreamDisconnectedThreadID != "cloud-thread" || model.notice != cloudHistoryUnavailableNotice {
		t.Fatalf("hold=%q notice=%q, want the paused state", model.cloudStreamDisconnectedThreadID, model.notice)
	}
	if len(*submitted) != 0 {
		t.Fatalf("held prompt was submitted: %#v", *submitted)
	}
}
