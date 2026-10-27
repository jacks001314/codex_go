package app

import (
	"testing"

	"codex_go/appserver"
	"codex_go/cli"
	"codex_go/session"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
)

func writeSafetyRecourseThread(t *testing.T, status string) session.ThreadID {
	t.Helper()
	store := newSessionStore()
	id := session.ThreadID("019e72f4-e09a-70f2-b2c2-a153a57b8cc0")
	record := &session.Record{
		ID:        id,
		SessionID: "s-safety",
		Metadata: session.Metadata{
			CWD:         t.TempDir(),
			HistoryMode: "legacy",
			RolloutTurns: []session.TurnSnapshot{
				{ID: "turn-1", Status: "completed"},
				{ID: "turn-2", Status: status},
			},
		},
		Items: []session.Item{
			{ID: "i-1", Type: "message", Role: "user", Text: "first", Metadata: map[string]any{"turnId": "turn-1"}},
			{ID: "i-2", Type: "message", Role: "user", Text: "blocked", Metadata: map[string]any{"turnId": "turn-2"}},
		},
	}
	if err := store.Create(record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return id
}

func TestInteractiveContinueSafetyRecourseForksBeforeBlockedTurn(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	threadID := writeSafetyRecourseThread(t, "failed")
	msg := interactiveContinueSafetyRecourse(nil)(string(threadID), "turn-2", "Explain the general background.")()
	ready, ok := msg.(codextea.SafetyRecourseRewriteReadyMsg)
	if !ok {
		t.Fatalf("message = %#v", msg)
	}
	if ready.SourceThreadID != string(threadID) || ready.Prompt != "Explain the general background." {
		t.Fatalf("ready = %#v", ready)
	}
	if ready.Response.Summary == nil || ready.Response.Summary.ThreadID == "" ||
		ready.Response.Summary.ThreadID == string(threadID) {
		t.Fatalf("fork summary = %#v", ready.Response.Summary)
	}
	fork, err := newSessionStore().Read(session.ThreadID(ready.Response.Summary.ThreadID), true, true)
	if err != nil || fork == nil {
		t.Fatalf("Read(fork) = %#v, %v", fork, err)
	}
	if fork.ForkedFromID != threadID {
		t.Fatalf("ForkedFromID = %q", fork.ForkedFromID)
	}
	if len(fork.Metadata.RolloutTurns) != 1 || fork.Metadata.RolloutTurns[0].ID != "turn-1" {
		t.Fatalf("fork turns = %#v", fork.Metadata.RolloutTurns)
	}
}

func TestInteractiveContinueSafetyRecourseRejectsStaleBlockedTurn(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	threadID := writeSafetyRecourseThread(t, "completed")
	msg := interactiveContinueSafetyRecourse(nil)(string(threadID), "turn-2", "Explain.")()
	failed, ok := msg.(codextea.SafetyRecourseRewriteFailedMsg)
	if !ok {
		t.Fatalf("message = %#v", msg)
	}
	if failed.Err == nil || failed.Err.Error() != "the blocked turn is no longer the latest turn" {
		t.Fatalf("error = %v", failed.Err)
	}
}

func TestRemoteSafetyRecourseForkVerificationHelpers(t *testing.T) {
	blocked := appserver.TurnsPage{Data: []appserver.Turn{{ID: "turn-2", Status: appserver.TurnStatusFailed}}}
	if !remoteBlockedTurnIsLatest(blocked, "turn-2") {
		t.Fatalf("blocked latest not recognized")
	}
	if remoteBlockedTurnIsLatest(blocked, "turn-1") {
		t.Fatalf("stale turn matched")
	}
	if remoteBlockedTurnIsLatest(appserver.TurnsPage{Data: []appserver.Turn{{ID: "turn-2", Status: appserver.TurnStatusCompleted}}}, "turn-2") {
		t.Fatalf("completed turn matched")
	}

	source := appserver.TurnsPage{Data: []appserver.Turn{
		{ID: "turn-2", Status: appserver.TurnStatusFailed},
		{ID: "turn-1", Status: appserver.TurnStatusCompleted},
	}}
	if !remoteForkBoundaryExcludesTurn(appserver.TurnsPage{Data: []appserver.Turn{{ID: "turn-1"}}}, source, "turn-2") {
		t.Fatalf("valid fork boundary rejected")
	}
	if remoteForkBoundaryExcludesTurn(appserver.TurnsPage{Data: []appserver.Turn{{ID: "turn-2"}}}, source, "turn-2") {
		t.Fatalf("fork containing the blocked turn accepted")
	}
	if remoteForkBoundaryExcludesTurn(appserver.TurnsPage{}, source, "turn-2") {
		t.Fatalf("empty fork with source history accepted")
	}
}

func TestRemoteSafetyRecourseForkParamsMatchRustFlow(t *testing.T) {
	state := codextui.NewState(nil)
	state.Model = "gpt-test"
	state.Provider = "openai"
	state.EffectiveReasoningEffort()
	params, err := remoteSafetyRecourseForkParams(&cli.RootOptions{}, state, "thread-1", "turn-2")
	if err != nil {
		t.Fatalf("params error = %v", err)
	}
	if params.ThreadID != "thread-1" || params.BeforeTurnID != "turn-2" || params.HistoryMode != session.ForkAll {
		t.Fatalf("params = %#v", params)
	}
	if !params.DeferGoalContinuation {
		t.Fatalf("fork must defer goal continuation like ForkGoalContinuation::StartIfIdle")
	}
	if params.Ephemeral {
		t.Fatalf("safety recourse fork must be persistent")
	}
	if params.Model == nil || *params.Model != "gpt-test" {
		t.Fatalf("model = %#v", params.Model)
	}
}
