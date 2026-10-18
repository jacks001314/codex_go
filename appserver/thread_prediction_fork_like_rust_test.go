package appserver

import (
	"encoding/json"
	"testing"
	"time"

	"codex_go/features"
	"codex_go/rollout"
	"codex_go/session"
	"codex_go/turn"
)

func newPredictionForkFixture(t *testing.T) (*RuntimeRouter, string, *session.Store) {
	t.Helper()
	store := session.NewStore(t.TempDir())
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		ThreadStatus: NewThreadStatusManager(),
	})
	t.Cleanup(func() { _ = router.Close() })
	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:   t.TempDir(),
		Model: "gpt-5.4",
		Config: map[string]any{
			"features": map[string]any{"deferred_tool_world_state": true},
		},
	}))
	if start.Error != nil {
		t.Fatalf("thread/start error = %+v", start.Error)
	}
	threadID := start.Result.(*ThreadStartResponse).Thread.ID
	if _, err := router.services.ThreadRouter.appendThreadItems(session.ThreadID(threadID), []session.Item{{
		ID: "history-marker", Type: "message", Role: "user", Text: "initial history", CreatedAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatalf("appendThreadItems() error = %v", err)
	}
	record, err := store.Load(session.ThreadID(threadID))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	record.Metadata.ServiceTier = "priority"
	record.Metadata.TurnContext = json.RawMessage(`{"turn_id":"turn-1","model":"gpt-5.4","effort":"high"}`)
	if err := store.Save(record); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	return router, threadID, store
}

// Rust #51884: a prediction fork inherits the loaded parent's effective
// context, reports its model/service tier/effort, and enables the
// reasoning-effort override without dropping inherited feature overrides.
func TestThreadPredictionForkInheritsParentContextLikeRust(t *testing.T) {
	router, threadID, store := newPredictionForkFixture(t)
	fork := router.Handle(requestWithParams(t, IntID(2), MethodThreadFork, ThreadForkParams{
		ThreadID:                   threadID,
		Ephemeral:                  true,
		ExperimentalPredictionMode: true,
	}))
	if fork.Error != nil {
		t.Fatalf("thread/fork error = %+v", fork.Error)
	}
	response := fork.Result.(*ThreadForkResponse)
	if response.Model != "gpt-5.4" || response.ServiceTier == nil || *response.ServiceTier != "priority" ||
		response.ReasoningEffort == nil || *response.ReasoningEffort != "high" {
		t.Fatalf("prediction fork response = %#v", response)
	}
	if response.Thread == nil {
		t.Fatal("prediction fork response is missing the thread")
	}
	forkRecord, err := store.Load(session.ThreadID(response.Thread.ID))
	if err == nil && forkRecord != nil {
		// Ephemeral forks live in the runtime, not the persistent store.
		t.Fatal("an ephemeral prediction fork was persisted")
	}
	live, ok := router.threads.EphemeralRecord(session.ThreadID(response.Thread.ID), true)
	if !ok || live == nil {
		t.Fatal("ephemeral prediction fork record is missing")
	}
	if live.Metadata.Model != "gpt-5.4" || live.Metadata.ServiceTier != "priority" || len(live.Items) == 0 {
		t.Fatalf("prediction fork record = %#v", live.Metadata)
	}
	overrides := threadRecordConfigOverrides(live)
	if overrides["features.deferred_tool_world_state"] != true || overrides["features.reasoning_effort_override"] != true {
		t.Fatalf("prediction fork config overrides = %#v", overrides)
	}
	cfg, err := router.effectiveConfigForTurn(&turn.TurnStartParams{ThreadID: response.Thread.ID, Config: overrides})
	if err != nil {
		t.Fatalf("effectiveConfigForTurn() error = %v", err)
	}
	if !features.Enabled(cfg.FeatureSettings(), "reasoning_effort_override") ||
		!features.Enabled(cfg.FeatureSettings(), "deferred_tool_world_state") {
		t.Fatalf("prediction fork features = %#v", cfg.FeatureSettings())
	}
}

// Rust #51884: prediction mode requires an ephemeral fork of a loaded parent
// and rejects configuration overrides and history cutoffs.
func TestThreadPredictionForkRejectsInvalidParametersLikeRust(t *testing.T) {
	model := "gpt-5.4"
	path := "rollout.jsonl"
	tests := []struct {
		name    string
		params  ThreadForkParams
		message string
	}{
		{
			name:    "configuration override",
			params:  ThreadForkParams{ThreadID: "thread-1", Ephemeral: true, ExperimentalPredictionMode: true, Model: &model},
			message: "`experimentalPredictionMode` cannot be combined with configuration overrides",
		},
		{
			name:    "not ephemeral",
			params:  ThreadForkParams{ThreadID: "thread-1", ExperimentalPredictionMode: true},
			message: "`experimentalPredictionMode` requires `ephemeral: true`",
		},
		{
			name:    "path",
			params:  ThreadForkParams{Path: &path, Ephemeral: true, ExperimentalPredictionMode: true},
			message: "`experimentalPredictionMode` requires a thread id without a path or turn cutoff",
		},
		{
			name:    "turn cutoff",
			params:  ThreadForkParams{ThreadID: "thread-1", Ephemeral: true, ExperimentalPredictionMode: true, LastTurnID: "turn-1"},
			message: "`experimentalPredictionMode` requires a thread id without a path or turn cutoff",
		},
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(session.NewStore(t.TempDir()))})
	t.Cleanup(func() { _ = router.Close() })
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := router.Handle(requestWithParams(t, IntID(1), MethodThreadFork, test.params))
			if response.Error == nil || response.Error.Message != test.message {
				t.Fatalf("thread/fork error = %+v, want %q", response.Error, test.message)
			}
		})
	}
}

// Rust #51884: the parent must be loaded in this runtime, not merely present
// in the persisted store.
func TestThreadPredictionForkRequiresLoadedParentLikeRust(t *testing.T) {
	_, threadID, store := newPredictionForkFixture(t)
	cold := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	t.Cleanup(func() { _ = cold.Close() })
	response := cold.Handle(requestWithParams(t, IntID(1), MethodThreadFork, ThreadForkParams{
		ThreadID:                   threadID,
		Ephemeral:                  true,
		ExperimentalPredictionMode: true,
	}))
	if response.Error == nil || response.Error.Message != "`experimentalPredictionMode` requires a loaded parent" {
		t.Fatalf("thread/fork error = %+v", response.Error)
	}
}

// Rust #51884: prediction forks inherit paginated history, so the ephemeral
// paginated-fork `excludeTurns` restriction does not apply to this mode.
func TestThreadPredictionForkInheritsPaginatedHistoryLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome:     store.Root(),
		SessionID:     "session-prediction-paginated",
		ThreadID:      "thread-prediction-paginated",
		Source:        "cli",
		ModelProvider: "openai",
		HistoryMode:   string(ThreadHistoryPaginated),
		Now:           time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	if err := recorder.AppendTurnStarted("turn-1", time.Now().UTC()); err != nil {
		t.Fatalf("AppendTurnStarted() error = %v", err)
	}
	if err := recorder.AppendItem(rollout.Item{ID: "user-1", Type: "message", Role: "user", Text: "paginated history", Metadata: map[string]any{"turnId": "turn-1"}}); err != nil {
		t.Fatalf("AppendItem() error = %v", err)
	}
	if err := recorder.AppendTurnComplete("turn-1", time.Now().UTC().Add(time.Second), 1_000); err != nil {
		t.Fatalf("AppendTurnComplete() error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		ThreadStatus: NewThreadStatusManager(),
	})
	t.Cleanup(func() { _ = router.Close() })
	resume := router.Handle(requestWithParams(t, IntID(1), MethodThreadResume, ThreadResumeParams{ThreadID: "thread-prediction-paginated"}))
	if resume.Error != nil {
		t.Fatalf("thread/resume error = %+v", resume.Error)
	}
	fork := router.Handle(requestWithParams(t, IntID(2), MethodThreadFork, ThreadForkParams{
		ThreadID:                   "thread-prediction-paginated",
		Ephemeral:                  true,
		ExperimentalPredictionMode: true,
	}))
	if fork.Error != nil {
		t.Fatalf("thread/fork error = %+v", fork.Error)
	}
	thread := fork.Result.(*ThreadForkResponse).Thread
	if thread == nil || thread.HistoryMode != ThreadHistoryPaginated || len(thread.Turns) != 1 ||
		len(thread.Turns[0].Items) != 1 || thread.Turns[0].Items[0].Text != "paginated history" {
		t.Fatalf("prediction fork thread = %#v", thread)
	}
}
