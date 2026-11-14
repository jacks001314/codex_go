package tea

import (
	bubbletea "charm.land/bubbletea/v2"
	codextui "codex_go/tui"
	"testing"
)

func TestAgentSwitchDispatchesOnlyEligibleDestinationQueueLikeRust(t *testing.T) {
	for _, tc := range []struct {
		name, status      string
		readOnly, pending bool
		want              int
	}{
		{"idle", "idle", false, false, 1},
		{"running", "running", false, false, 0},
		{"read only", "idle", true, false, 0},
		{"missing receipt", "idle", false, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := newPendingSteerModel(t, "thread-a", nil)
			if tc.pending {
				submitPendingSteer(t, model, "uncertain")
			}
			model.queued = []queuedSubmission{{Request: SubmitRequest{Prompt: "destination prompt", ClientID: "a-input"}}}
			model.applyResumeResponse("thread-b", SessionResumeResponse{Status: "idle"})
			calls := 0
			model.onSubmitRequest = func(request SubmitRequest) bubbletea.Cmd {
				calls++
				if model.currentThreadID() != "thread-a" || request.ClientID != "a-input" {
					t.Fatal("wrong destination/input")
				}
				return func() bubbletea.Msg { return nil }
			}
			model.Update(AgentSwitchResultMsg{ThreadID: "thread-a", Response: AgentThreadSwitchResponse{Entry: codextui.AgentThreadEntry{ThreadID: "thread-a"}, Status: tc.status, ReadOnly: tc.readOnly}})
			if calls != tc.want {
				t.Fatalf("submissions=%d want=%d", calls, tc.want)
			}
			if tc.want == 0 && len(model.queued) != 1 {
				t.Fatal("ineligible queue consumed")
			}
		})
	}
}

func TestResumeDispatchIsBoundToRestoredThreadLikeRust(t *testing.T) {
	model := newPendingSteerModel(t, "thread-a", nil)
	model.queued = []queuedSubmission{{Request: SubmitRequest{Prompt: "a", ClientID: "a-input"}}}
	model.applyResumeResponse("thread-b", SessionResumeResponse{Status: "idle"})
	model.applyResumeResponse("thread-a", SessionResumeResponse{Status: "idle"})
	calls := 0
	model.onSubmitRequest = func(request SubmitRequest) bubbletea.Cmd { calls++; return nil }
	model.switchThreadID("thread-b")
	model.consumeResumedQueue()
	if calls != 0 {
		t.Fatal("stale resume submitted to another thread")
	}
	model.applyResumeResponse("thread-a", SessionResumeResponse{Status: "idle"})
	model.consumeResumedQueue()
	model.consumeResumedQueue()
	if calls != 1 {
		t.Fatalf("submissions=%d want=1", calls)
	}
}
