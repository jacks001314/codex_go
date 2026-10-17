package appserver

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/model"
	"codex_go/session"
	"codex_go/state"
	"codex_go/tool"
	"codex_go/turn"
)

// Mirrors Rust #41562: create_goal anchors the continuation lineage to the
// active turn's root, and an external objective edit or clear drops it.
func TestGoalTurnLineageAnchorsAndInvalidatesLikeRust(t *testing.T) {
	router, _, threadID := newGoalToolTestRouter(t)
	if err := router.threads.ReserveTurn(threadID); err != nil {
		t.Fatalf("ReserveTurn() error = %v", err)
	}
	if err := router.threads.RegisterTurn(threadID, "turn-1", nil, time.Now().UTC().UnixMilli(), &turn.TurnStartParams{
		ThreadID:   threadID,
		RootTurnID: "turn-root",
	}); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}
	executors := router.goalToolExecutorsForTurn(&config.Config{Values: map[string]any{
		"features": map[string]any{"goals": true},
	}}, threadID, "turn-1")
	create := goalToolExecutorByName(t, executors, tool.GoalCreateToolName)
	if _, err := create.Execute(context.Background(), goalToolInvocation(tool.GoalCreateToolName, `{"objective":"ship lineage"}`)); err != nil {
		t.Fatalf("create goal error = %v", err)
	}
	lineage, ok := router.goalTurnLineageFor(threadID)
	if !ok || lineage.RootTurnID != "turn-root" || lineage.ParentTurnID != "turn-1" {
		t.Fatalf("lineage after create = %#v, want root turn-root parent turn-1", lineage)
	}

	edited := "edited objective"
	if _, _, _, err := router.setStateThreadGoal(&GoalSetParams{ThreadID: threadID, Objective: &edited}); err != nil {
		t.Fatalf("set goal objective error = %v", err)
	}
	if lineage, ok := router.goalTurnLineageFor(threadID); ok {
		t.Fatalf("external objective edit kept stale lineage: %#v", lineage)
	}

	router.setGoalTurnLineage(threadID, "turn-root", "turn-1")
	if _, _, _, err := router.clearStateThreadGoal(&GoalClearParams{ThreadID: threadID}); err != nil {
		t.Fatalf("clear goal error = %v", err)
	}
	if lineage, ok := router.goalTurnLineageFor(threadID); ok {
		t.Fatalf("goal clear kept stale lineage: %#v", lineage)
	}
}

type blockingGoalLineageAgent struct {
	release  <-chan struct{}
	requests chan model.AgentRequest
}

func (a *blockingGoalLineageAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	if a.requests != nil {
		a.requests <- *request
	}
	select {
	case <-a.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &model.AgentResponse{Message: "still working"}, nil
}

// Mirrors Rust #41562: the automatic continuation starts with the trusted root
// and previous parent turn instead of fresh lineage.
func TestGoalContinuationCarriesTurnLineageLikeRust(t *testing.T) {
	home := t.TempDir()
	ctx := context.Background()
	sqliteConfig, err := state.NewSqliteConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	stateRuntime, err := state.InitStateRuntime(ctx, sqliteConfig, "openai")
	if err != nil {
		t.Fatal(err)
	}
	defer stateRuntime.Close()

	store := session.NewStore(home)
	threadRouter := NewRouter(store)
	threadRouter.SetStateRuntime(stateRuntime)
	now := time.Now().UTC()
	record := &session.Record{
		ID: "goal-lineage-thread", SessionID: "goal-lineage-session", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: home, ModelProvider: "openai", HistoryMode: "paginated"},
	}
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	if err := threadRouter.createThreadRollout(record, now); err != nil {
		t.Fatal(err)
	}
	budget := int64(1000)
	if _, err := stateRuntime.ReplaceThreadGoal(ctx, string(record.ID), "finish parity", state.ThreadGoalActive, &budget); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseTurn := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseTurn()
	agent := &blockingGoalLineageAgent{release: release, requests: make(chan model.AgentRequest, 1)}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: threadRouter,
		StateRuntime: stateRuntime,
		TurnRuntime:  turn.NewRuntime(&turn.RuntimeOptions{Agent: agent}),
	})
	router.requireThreadStatus().UpsertThread(string(record.ID), false)
	router.setGoalTurnLineage(string(record.ID), "turn-root", "turn-prev")
	router.continueThreadGoalIfIdle(string(record.ID))

	deadline := time.Now().Add(5 * time.Second)
	var active *activeRuntimeTurn
	for time.Now().Before(deadline) {
		active = router.threads.ActiveTurn(string(record.ID))
		if active != nil && active.Params != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if active == nil || active.Params == nil {
		t.Fatal("goal continuation turn did not start")
	}
	if active.Params.RootTurnID != "turn-root" {
		t.Fatalf("continuation root turn = %q, want turn-root", active.Params.RootTurnID)
	}
	if active.Params.ParentTurnID != "turn-prev" {
		t.Fatalf("continuation parent turn = %q, want turn-prev", active.Params.ParentTurnID)
	}

	// The continuation turn becomes the next parent while the root is kept.
	router.extendGoalTurnLineage(string(record.ID), active.Params.RootTurnID, active.TurnID)
	lineage, ok := router.goalTurnLineageFor(string(record.ID))
	if !ok || lineage.RootTurnID != "turn-root" || lineage.ParentTurnID != active.TurnID {
		t.Fatalf("lineage after continuation = %#v, want root turn-root parent %s", lineage, active.TurnID)
	}
	releaseTurn()
	_ = router.Close()
}

// Mirrors Rust #41562: external context reaching an active turn invalidates the
// stored lineage so a later continuation cannot reuse stale attribution.
func TestExternalInjectInvalidatesGoalTurnLineageLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	threadRouter := NewRouter(store)
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: threadRouter, SteerMailbox: turn.NewSteerMailbox(), DefaultCWD: t.TempDir()})
	router.rememberConnectionClientInfo("default", ClientInfo{Name: "test", Version: "1"})
	started := threadRouter.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir()}))
	if started.Error != nil {
		t.Fatal(started.Error)
	}
	threadID := started.Result.(*ThreadStartResponse).Thread.ID
	router.markResponseThreadLoaded(started.Result, "default")
	if err := router.registerActiveRuntimeTurn(threadID, "turn-active", func() {}, time.Now().UTC().UnixMilli(), &turn.TurnStartParams{ThreadID: threadID}); err != nil {
		t.Fatalf("registerActiveRuntimeTurn() error = %v", err)
	}
	router.setGoalTurnLineage(threadID, "turn-root", "turn-prev")
	response := router.Handle(requestWithParams(t, IntID(2), MethodThreadInjectItems, ThreadInjectItemsParams{
		ThreadID: threadID,
		Items: []json.RawMessage{
			json.RawMessage(`{"type":"message","role":"user","content":[{"type":"input_text","text":"external context"}]}`),
		},
	}))
	if response.Error != nil {
		t.Fatalf("inject items error = %+v", response.Error)
	}
	if lineage, ok := router.goalTurnLineageFor(threadID); ok {
		t.Fatalf("external inject kept stale lineage: %#v", lineage)
	}
}
