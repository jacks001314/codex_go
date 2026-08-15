package appserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/config"
	"codex_go/model"
	"codex_go/rollout"
	"codex_go/session"
	"codex_go/state"
	"codex_go/turn"
)

func TestDefaultStdioLogDBLifecyclePersistsRuntimeLogs(t *testing.T) {
	home := t.TempDir()
	previous := slog.Default()
	server := NewDefaultStdioServer(&StdioOptions{CodexHome: home, RuntimeOptions: &RuntimeRouterOptions{EnableLogDB: true}})
	if err := server.router.StartupError(); err != nil {
		t.Fatal(err)
	}

	slog.Info("persisted app-server log", "target", "test", "thread_id", "stdio-log-thread")
	if err := server.Serve(strings.NewReader(""), io.Discard); err != nil {
		t.Fatal(err)
	}
	if slog.Default() != previous {
		t.Fatal("stdio shutdown did not restore the process logger")
	}
	config, err := state.NewSqliteConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := state.InitStateRuntime(context.Background(), config, "openai")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	rows, err := reopened.QueryLogs(context.Background(), state.LogQuery{ThreadIDs: []string{"stdio-log-thread"}})
	if err != nil || len(rows) != 1 || rows[0].Message == nil || !strings.Contains(*rows[0].Message, "persisted app-server log") {
		t.Fatalf("stdio log rows = %#v, %v", rows, err)
	}
}

func TestFeedbackUploadFlushesSQLiteLogsForAgentSubtreeAndAttachesRollouts(t *testing.T) {
	home := t.TempDir()
	ctx := context.Background()
	config, err := state.NewSqliteConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	stateRuntime, err := state.InitStateRuntime(ctx, config, "openai")
	if err != nil {
		t.Fatal(err)
	}
	defer stateRuntime.Close()
	store := session.NewStore(home)
	threadRouter := NewRouter(store)
	now := time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC)
	for _, record := range []*session.Record{
		{ID: "feedback-parent", SessionID: "feedback-parent", CreatedAt: now, UpdatedAt: now, Metadata: session.Metadata{CWD: home, ModelProvider: "openai"}},
		{ID: "feedback-child", SessionID: "feedback-child", ParentThreadID: "feedback-parent", CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second), Metadata: session.Metadata{CWD: home, ModelProvider: "openai"}},
	} {
		if err := store.Save(record); err != nil {
			t.Fatal(err)
		}
		if err := threadRouter.createThreadRollout(record, record.CreatedAt); err != nil {
			t.Fatal(err)
		}
	}
	if err := stateRuntime.UpsertThreadSpawnEdge(ctx, "feedback-parent", "feedback-child", "open"); err != nil {
		t.Fatal(err)
	}
	logHandler := state.NewLogDBHandlerWithConfig(stateRuntime, nil, state.LogSinkQueueConfig{QueueCapacity: 8, BatchSize: 8, FlushInterval: time.Hour})
	defer logHandler.Close(context.Background())
	logger := slog.New(logHandler)
	logger.Info("parent persisted log", "target", "test", "thread_id", "feedback-parent")
	logger.Warn("child persisted log", "target", "test", "thread_id", "feedback-child")
	snapshot := &FeedbackSnapshot{Diagnostics: NewFeedbackDiagnostics(nil)}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: threadRouter, StateRuntime: stateRuntime, LogDBHandler: logHandler, Feedback: snapshot})
	response := router.Handle(requestWithParams(t, IntID(1), MethodFeedbackUpload, FeedbackUploadParams{
		Classification: "bug", ThreadID: stringPointerAppserver("feedback-parent"), IncludeLogs: true,
	}))
	if response.Error != nil {
		t.Fatalf("feedback upload error = %+v", response.Error)
	}
	prepared := snapshot.LastPrepared
	if prepared == nil || len(prepared.AttachmentPaths) != 2 {
		t.Fatalf("feedback attachment paths = %#v", prepared)
	}
	logs := feedbackAttachmentByName(prepared.Attachments, "codex-logs.log")
	if logs == nil || !strings.Contains(string(logs.Buffer), "parent persisted log") || !strings.Contains(string(logs.Buffer), "child persisted log") {
		t.Fatalf("feedback sqlite logs = %q", feedbackAttachmentBuffer(logs))
	}
}

func stringPointerAppserver(value string) *string { return &value }

func feedbackAttachmentByName(attachments []FeedbackAttachment, name string) *FeedbackAttachment {
	for i := range attachments {
		if attachments[i].Filename == name {
			return &attachments[i]
		}
	}
	return nil
}

func feedbackAttachmentBuffer(attachment *FeedbackAttachment) []byte {
	if attachment == nil {
		return nil
	}
	return attachment.Buffer
}

func TestRuntimeRouterUsesPersistentSpawnGraphByDefault(t *testing.T) {
	home := t.TempDir()
	ctx := context.Background()
	config, err := state.NewSqliteConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	stateRuntime, err := state.InitStateRuntime(ctx, config, "openai")
	if err != nil {
		t.Fatal(err)
	}
	defer stateRuntime.Close()
	store := session.NewStore(home)
	parent := &session.Record{
		ID: "spawn-parent", SessionID: "spawn-parent", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		Metadata: session.Metadata{CWD: home, ModelProvider: "openai"},
	}
	if err := store.Save(parent); err != nil {
		t.Fatal(err)
	}

	first := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), StateRuntime: stateRuntime})
	controller := newRuntimeAgentController(first, string(parent.ID), home, 4)
	spawned, err := controller.SpawnAgent(ctx, &agent.SpawnAgentArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if first.services.SpawnGraph == nil {
		t.Fatal("state-backed spawn graph was not installed")
	}
	if _, err := controller.CloseAgent(ctx, &agent.CloseAgentArgs{Target: spawned.AgentID}); err != nil {
		t.Fatal(err)
	}

	second := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), StateRuntime: stateRuntime})
	children, err := second.services.SpawnGraph.ListThreadSpawnChildren(string(parent.ID), nil)
	if err != nil || !reflect.DeepEqual(children, []string{spawned.AgentID}) {
		t.Fatalf("children after router restart = %#v, %v", children, err)
	}
	closed := agent.ThreadSpawnEdgeClosed
	children, err = second.services.SpawnGraph.ListThreadSpawnChildren(string(parent.ID), &closed)
	if err != nil || !reflect.DeepEqual(children, []string{spawned.AgentID}) {
		t.Fatalf("closed children after router restart = %#v, %v", children, err)
	}
	if got := second.services.ThreadRouter.spawnGraph; got != second.services.SpawnGraph {
		t.Fatalf("thread router graph = %#v, want runtime graph %#v", got, second.services.SpawnGraph)
	}
}

func TestDefaultStdioOwnsRustStateRuntimeLifecycle(t *testing.T) {
	home := t.TempDir()
	server := NewDefaultStdioServer(&StdioOptions{CodexHome: home})
	if server.router.StartupError() != nil {
		t.Fatalf("startup error = %v", server.router.StartupError())
	}
	runtime := server.router.services.StateRuntime
	if runtime == nil || !server.router.services.CloseStateRuntime {
		t.Fatalf("stdio router does not own its state runtime: %#v", server.router.services)
	}
	config := runtime.SQLite()
	for _, path := range []string{config.StateDBPath(), config.LogsDBPath(), config.GoalsDBPath(), config.MemoriesDBPath()} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("runtime database %s missing: %v", path, err)
		}
	}
	if _, err := os.Stat(config.ThreadHistoryDBPath()); !os.IsNotExist(err) {
		t.Fatalf("thread history must be lazy, stat err=%v", err)
	}
	if err := server.Serve(strings.NewReader(""), io.Discard); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if err := runtime.StateDB().Ping(); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("owned state runtime remained open: %v", err)
	}
}

func TestDefaultStdioBackfillsExistingRolloutsBeforeServing(t *testing.T) {
	home := t.TempDir()
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome: home, ThreadID: "startup-thread", Source: "cli", CWD: home,
		ModelProvider: "openai", HistoryMode: "paginated", Now: time.Date(2026, 7, 31, 1, 2, 3, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}

	server := NewDefaultStdioServer(&StdioOptions{CodexHome: home})
	if err := server.router.StartupError(); err != nil {
		t.Fatalf("startup error = %v", err)
	}
	runtime := server.router.services.StateRuntime
	defer runtime.Close()
	var path, status string
	if err := runtime.StateDB().QueryRow(`SELECT rollout_path FROM threads WHERE id = 'startup-thread'`).Scan(&path); err != nil {
		t.Fatalf("startup rollout was not backfilled: %v", err)
	}
	if path != recorder.Path() {
		t.Fatalf("backfilled rollout path = %q, want %q", path, recorder.Path())
	}
	if err := runtime.StateDB().QueryRow(`SELECT status FROM backfill_state WHERE id = 1`).Scan(&status); err != nil || status != "complete" {
		t.Fatalf("startup backfill status = %q, %v", status, err)
	}
}

func TestConnectionRoutersDoNotCloseInjectedSharedStateRuntime(t *testing.T) {
	ctx := context.Background()
	config, err := state.NewSqliteConfig(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := state.InitStateRuntime(ctx, config, "openai")
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	options := &RuntimeRouterOptions{StateRuntime: runtime}
	first := NewDefaultRuntimeRouterWithOptions(session.NewStore(filepath.Join(config.Home(), "sessions")), config.Home(), options)
	second := NewDefaultRuntimeRouterWithOptions(session.NewStore(filepath.Join(config.Home(), "sessions")), config.Home(), options)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.StateDB().PingContext(ctx); err != nil {
		t.Fatalf("first connection closed shared runtime: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.StateDB().PingContext(ctx); err != nil {
		t.Fatalf("second connection closed shared runtime: %v", err)
	}
}

func TestRuntimeRouterPersistsThreadGoalInRustGoalsDBAndRollout(t *testing.T) {
	home := t.TempDir()
	ctx := context.Background()
	config, err := state.NewSqliteConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	stateRuntime, err := state.InitStateRuntime(ctx, config, "openai")
	if err != nil {
		t.Fatal(err)
	}
	defer stateRuntime.Close()
	store := session.NewStore(home)
	threadRouter := NewRouter(store)
	threadRouter.SetStateRuntime(stateRuntime)
	now := time.Date(2026, 7, 31, 8, 0, 0, 0, time.UTC)
	record := &session.Record{
		ID: "goal-sqlite-thread", SessionID: "goal-sqlite-session", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: home, ModelProvider: "openai", HistoryMode: "paginated"},
		Items:    []session.Item{{ID: "u1", Type: "message", Role: "user", Text: "start persisted goal", Metadata: map[string]any{"turnId": "turn-1"}}},
	}
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	if err := threadRouter.createThreadRollout(record, now); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: threadRouter, StateRuntime: stateRuntime})

	objective := "persist only in the Rust-compatible goal database"
	budget := int64(500)
	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadGoalSet, GoalSetParams{
		ThreadID: string(record.ID), Objective: &objective, TokenBudget: &budget,
	}))
	if response.Error != nil {
		t.Fatalf("thread/goal/set error: %+v", response.Error)
	}
	created := response.Result.(*GoalSetResponse).Goal
	if created.GoalID == "" || created.Objective != objective || created.TokenBudget == nil || *created.TokenBudget != budget {
		t.Fatalf("created goal = %#v", created)
	}
	persisted, err := stateRuntime.GetThreadGoal(ctx, string(record.ID))
	if err != nil || persisted == nil || persisted.GoalID != created.GoalID {
		t.Fatalf("persisted goal = %#v, %v", persisted, err)
	}
	compatibilityRecord, err := store.Read(record.ID, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := compatibilityRecord.Metadata.Extra[threadGoalExtraKey]; exists {
		t.Fatalf("goal leaked into session metadata: %#v", compatibilityRecord.Metadata.Extra)
	}

	path := threadRouter.threadRolloutPath(record)
	lines, _, err := rollout.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	foundGoalEvent := false
	for _, line := range lines {
		if line.Type != "event_msg" || len(line.Payload) == 0 {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(line.Payload, &payload); err != nil || payload["type"] != "thread_goal_updated" {
			continue
		}
		foundGoalEvent = true
		if payload["threadId"] != string(record.ID) || payload["turnId"] != nil {
			t.Fatalf("goal event envelope = %#v", payload)
		}
		goalPayload, ok := payload["goal"].(map[string]any)
		if !ok || goalPayload["threadId"] != string(record.ID) || goalPayload["objective"] != objective || goalPayload["status"] != "active" {
			t.Fatalf("goal event payload = %#v", payload["goal"])
		}
	}
	if !foundGoalEvent {
		t.Fatalf("thread_goal_updated event missing from %s", path)
	}

	reloaded := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), StateRuntime: stateRuntime})
	get := reloaded.Handle(requestWithParams(t, IntID(2), MethodThreadGoalGet, GoalGetParams{ThreadID: string(record.ID)}))
	if get.Error != nil || get.Result.(*GoalGetResponse).Goal == nil || get.Result.(*GoalGetResponse).Goal.GoalID != created.GoalID {
		t.Fatalf("goal after router restart = %+v", get)
	}
	clear := reloaded.Handle(requestWithParams(t, IntID(3), MethodThreadGoalClear, GoalClearParams{ThreadID: string(record.ID)}))
	if clear.Error != nil || !clear.Result.(*GoalClearResponse).Cleared {
		t.Fatalf("thread/goal/clear = %+v", clear)
	}
	if got, err := stateRuntime.GetThreadGoal(ctx, string(record.ID)); err != nil || got != nil {
		t.Fatalf("goal after clear = %#v, %v", got, err)
	}
}

type goalContinuationTestAgent struct {
	threadID     string
	stateRuntime *state.StateRuntime
	requests     chan model.AgentRequest
}

func (a *goalContinuationTestAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.requests <- *request
	complete := state.ThreadGoalComplete
	if _, err := a.stateRuntime.UpdateThreadGoal(ctx, a.threadID, state.GoalUpdate{Status: &complete}); err != nil {
		return nil, err
	}
	return &model.AgentResponse{
		ResponseID: "goal-continuation-response",
		Message:    "goal complete",
		Items:      []model.AgentItem{{ID: "goal-continuation-message", Type: "agent_message", Text: "goal complete"}},
	}, nil
}

func TestRuntimeRouterActiveGoalContinuationStartsTurnWhenIdle(t *testing.T) {
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
	now := time.Date(2026, 7, 31, 8, 0, 0, 0, time.UTC)
	record := &session.Record{
		ID: "goal-continuation-thread", SessionID: "goal-continuation-session", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: home, ModelProvider: "openai", HistoryMode: "paginated"},
		Items:    []session.Item{{ID: "u1", Type: "message", Role: "user", Text: "start goal", Metadata: map[string]any{"turnId": "turn-1"}}},
	}
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	if err := threadRouter.createThreadRollout(record, now); err != nil {
		t.Fatal(err)
	}

	objective := "finish parity"
	budget := int64(1000)
	active := state.ThreadGoalActive
	if _, err := stateRuntime.ReplaceThreadGoal(ctx, string(record.ID), objective, active, &budget); err != nil {
		t.Fatal(err)
	}

	requests := make(chan model.AgentRequest, 1)
	agent := &goalContinuationTestAgent{threadID: string(record.ID), stateRuntime: stateRuntime, requests: requests}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: threadRouter,
		StateRuntime: stateRuntime,
		TurnRuntime:  turn.NewRuntime(&turn.RuntimeOptions{Agent: agent}),
	})
	router.requireThreadStatus().UpsertThread(string(record.ID), false)

	router.continueThreadGoalIfIdle(string(record.ID))

	select {
	case request := <-requests:
		if !strings.Contains(request.Instructions, objective) {
			t.Fatalf("continuation instructions = %q, want objective %q", request.Instructions, objective)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for goal continuation turn")
	}

	deadline := time.Now().Add(5 * time.Second)
	for router.threads.ActiveTurn(string(record.ID)) != nil {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for goal continuation turn to complete")
		}
		time.Sleep(10 * time.Millisecond)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := router.threads.WaitForTurnWorkers(waitCtx); err != nil {
		t.Fatalf("wait for goal continuation turn worker: %v", err)
	}

	goal, err := stateRuntime.GetThreadGoal(ctx, string(record.ID))
	if err != nil || goal == nil || goal.Status != state.ThreadGoalComplete {
		t.Fatalf("goal after continuation = %#v, %v", goal, err)
	}
}

func TestPaginatedForkCanDeferInheritedGoalContinuation(t *testing.T) {
	home := t.TempDir()
	ctx := context.Background()
	config, err := state.NewSqliteConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	stateRuntime, err := state.InitStateRuntime(ctx, config, "openai")
	if err != nil {
		t.Fatal(err)
	}
	defer stateRuntime.Close()
	store := session.NewStore(home)
	router := NewRouter(store)
	router.SetStateRuntime(stateRuntime)
	defer router.Close()
	now := time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)
	source := &session.Record{
		ID: "goal-fork-source", SessionID: "goal-fork-session", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: home, ModelProvider: "openai", HistoryMode: "paginated"},
		Items: []session.Item{
			{ID: "u1", Type: "message", Role: "user", Text: "source work", Metadata: map[string]any{"turnId": "turn-1"}},
			{ID: "a1", Type: "agent_message", Role: "assistant", Text: "source result", Metadata: map[string]any{"turnId": "turn-1"}},
		},
	}
	if err := store.Save(source); err != nil {
		t.Fatal(err)
	}
	if err := router.createThreadRollout(source, now); err != nil {
		t.Fatal(err)
	}
	budget := int64(1_000)
	sourceGoal, err := stateRuntime.ReplaceThreadGoal(ctx, string(source.ID), "finish the inherited objective", state.ThreadGoalBlocked, &budget)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stateRuntime.AccountThreadGoalUsage(ctx, string(source.ID), 12, 345, state.GoalAccountingActiveOrStopped, nil); err != nil {
		t.Fatal(err)
	}
	sourceGoal, err = stateRuntime.GetThreadGoal(ctx, string(source.ID))
	if err != nil {
		t.Fatal(err)
	}

	fork := router.Handle(requestWithParams(t, IntID(10), MethodThreadFork, ThreadForkParams{
		ThreadID: string(source.ID), ExcludeTurns: true, DeferGoalContinuation: true,
	}))
	if fork.Error != nil {
		t.Fatalf("thread/fork error: %+v", fork.Error)
	}
	targetID := fork.Result.(*ThreadForkResponse).Thread.ID
	inherited, err := stateRuntime.GetThreadGoal(ctx, targetID)
	if err != nil || inherited == nil {
		t.Fatalf("inherited goal = %#v, %v", inherited, err)
	}
	if inherited.GoalID != sourceGoal.GoalID || inherited.Objective != sourceGoal.Objective || inherited.Status != sourceGoal.Status || inherited.TokensUsed != 345 || inherited.TimeUsedSeconds != 12 {
		t.Fatalf("inherited goal mismatch: source=%#v target=%#v", sourceGoal, inherited)
	}
	deferred, err := stateRuntime.HasThreadGoalContinuationDeferral(ctx, targetID)
	if err != nil || !deferred {
		t.Fatalf("inherited goal deferral = %v, %v", deferred, err)
	}

	plainFork := router.Handle(requestWithParams(t, IntID(11), MethodThreadFork, ThreadForkParams{
		ThreadID: string(source.ID), ExcludeTurns: true,
	}))
	if plainFork.Error != nil {
		t.Fatalf("plain thread/fork error: %+v", plainFork.Error)
	}
	plainTargetID := plainFork.Result.(*ThreadForkResponse).Thread.ID
	if goal, err := stateRuntime.GetThreadGoal(ctx, plainTargetID); err != nil || goal != nil {
		t.Fatalf("plain fork inherited goal = %#v, %v", goal, err)
	}
}

func TestRuntimeGoalTurnLifecycleAccountsAndStopsLikeRust(t *testing.T) {
	home := t.TempDir()
	ctx := context.Background()
	config, err := state.NewSqliteConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	stateRuntime, err := state.InitStateRuntime(ctx, config, "openai")
	if err != nil {
		t.Fatal(err)
	}
	defer stateRuntime.Close()
	store := session.NewStore(home)
	threadRouter := NewRouter(store)
	threadRouter.SetStateRuntime(stateRuntime)
	threadID := "goal-accounting-thread"
	base := time.Date(2026, 8, 1, 1, 0, 0, 0, time.UTC)
	record := &session.Record{
		ID: session.ThreadID(threadID), SessionID: "goal-accounting-session",
		CreatedAt: base, UpdatedAt: base, RecencyAt: base,
		Metadata: session.Metadata{CWD: home, ModelProvider: "openai", HistoryMode: "paginated"},
	}
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	if err := threadRouter.createThreadRollout(record, base); err != nil {
		t.Fatal(err)
	}
	budget := int64(1_000)
	goal := &state.ThreadGoal{
		ThreadID: threadID, GoalID: "goal-accounting-id", Objective: "account exact turn progress",
		Status: state.ThreadGoalActive, TokenBudget: &budget, CreatedAt: base, UpdatedAt: base,
	}
	if err := stateRuntime.ReplaceThreadGoalSnapshot(ctx, goal); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: threadRouter, StateRuntime: stateRuntime})
	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)

	router.beginStateThreadGoalTurn(threadID, "plan-turn", base.UnixMilli(), true, "")
	deferred, err := stateRuntime.HasThreadGoalContinuationDeferral(ctx, threadID)
	if err != nil || deferred {
		t.Fatalf("deferral after explicit plan turn = %v, %v", deferred, err)
	}
	router.finishStateThreadGoalTurn(threadID, "plan-turn", base.Add(10*time.Second), 500, nil)
	unchanged, err := stateRuntime.GetThreadGoal(ctx, threadID)
	if err != nil || unchanged.TokensUsed != 0 || unchanged.TimeUsedSeconds != 0 {
		t.Fatalf("plan turn accounted goal = %#v, %v", unchanged, err)
	}

	router.beginStateThreadGoalTurn(threadID, "regular-turn", base.UnixMilli(), false, "")
	router.finishStateThreadGoalTurn(threadID, "regular-turn", base.Add(2500*time.Millisecond), 45, nil)
	accounted, err := stateRuntime.GetThreadGoal(ctx, threadID)
	if err != nil || accounted.TokensUsed != 45 || accounted.TimeUsedSeconds != 2 || accounted.Status != state.ThreadGoalActive {
		t.Fatalf("regular turn accounting = %#v, %v", accounted, err)
	}
	updatedNotification := false
	for _, notification := range sink.List() {
		payload, ok := notification.Params.(*GoalUpdatedNotification)
		if notification.Method == NotificationThreadGoalUpdated && ok && payload.TurnID != nil && *payload.TurnID == "regular-turn" && payload.Goal.TokensUsed == 45 {
			updatedNotification = true
		}
	}
	if !updatedNotification {
		t.Fatalf("turn-scoped goal update notification missing: %#v", sink.List())
	}

	router.beginStateThreadGoalTurn(threadID, "limited-turn", base.Add(3*time.Second).UnixMilli(), false, "")
	router.finishStateThreadGoalTurn(threadID, "limited-turn", base.Add(4*time.Second), 5, CodexErrorInfo("usageLimitExceeded"))
	limited, err := stateRuntime.GetThreadGoal(ctx, threadID)
	if err != nil || limited.TokensUsed != 50 || limited.TimeUsedSeconds != 3 || limited.Status != state.ThreadGoalUsageLimited {
		t.Fatalf("usage-limited turn goal = %#v, %v", limited, err)
	}

	path := threadRouter.threadRolloutPath(record)
	lines, _, err := rollout.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	foundTurnEvent := false
	for _, line := range lines {
		var payload map[string]any
		if line.Type != "event_msg" || json.Unmarshal(line.Payload, &payload) != nil || payload["type"] != "thread_goal_updated" {
			continue
		}
		if payload["turnId"] == "limited-turn" {
			foundTurnEvent = true
		}
	}
	if !foundTurnEvent {
		t.Fatalf("turn-scoped thread_goal_updated event missing from %s", path)
	}
}

func TestRouterRolloutFlushReconcilesStateAndLazilyProjectsHistory(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	config, err := state.NewSqliteConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := state.InitStateRuntime(ctx, config, "openai")
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	store := session.NewStore(home)
	router := NewRouter(store)
	t.Cleanup(func() { _ = router.Close() })
	router.SetStateRuntime(runtime)

	createRecord(t, store, "legacy-flush", fixedTime())
	legacy, err := store.Read("legacy-flush", true, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := router.createThreadRollout(legacy, fixedTime()); err != nil {
		t.Fatal(err)
	}
	var historyMode string
	if err := runtime.StateDB().QueryRowContext(ctx, `SELECT history_mode FROM threads WHERE id = 'legacy-flush'`).Scan(&historyMode); err != nil || historyMode != "legacy" {
		t.Fatalf("legacy state row history mode = %q, %v", historyMode, err)
	}
	if _, err := os.Stat(config.ThreadHistoryDBPath()); !os.IsNotExist(err) {
		t.Fatalf("legacy flush created lazy history DB: %v", err)
	}

	createPaginatedRecord(t, store, "paginated-flush", fixedTime().Add(time.Second))
	paginated, err := store.Read("paginated-flush", true, true)
	if err != nil {
		t.Fatal(err)
	}
	paginated.Metadata.Git = map[string]string{"branch": "rollout-branch"}
	if err := router.createThreadRollout(paginated, fixedTime().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var rolloutPath string
	if err := runtime.StateDB().QueryRowContext(ctx, `SELECT history_mode, rollout_path FROM threads WHERE id = 'paginated-flush'`).Scan(&historyMode, &rolloutPath); err != nil || historyMode != "paginated" {
		t.Fatalf("paginated state row = history:%q path:%q error:%v", historyMode, rolloutPath, err)
	}
	if _, err := os.Stat(config.ThreadHistoryDBPath()); err != nil {
		t.Fatalf("paginated flush did not create history DB: %v", err)
	}
	historyDB, err := runtime.ThreadHistoryDB(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var projections int
	if err := historyDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM thread_history_projection_state WHERE thread_id = 'paginated-flush'`).Scan(&projections); err != nil || projections != 1 {
		t.Fatalf("paginated projection rows = %d, %v", projections, err)
	}

	if _, err := runtime.StateDB().ExecContext(ctx, `UPDATE threads SET title = 'explicit title', first_user_message = 'hello', git_branch = NULL WHERE id = 'paginated-flush'`); err != nil {
		t.Fatal(err)
	}
	paginated.Metadata.Git["branch"] = "new-rollout-branch"
	if err := router.appendThreadMetadataRollout(paginated, fixedTime().Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	var title string
	var branch sql.NullString
	if err := runtime.StateDB().QueryRowContext(ctx, `SELECT title, git_branch FROM threads WHERE id = 'paginated-flush'`).Scan(&title, &branch); err != nil {
		t.Fatal(err)
	}
	if title != "explicit title" || branch.Valid {
		t.Fatalf("reconciled SQLite-owned fields = title:%q branch:%v", title, branch)
	}
}

func TestRouterPaginatedHistoryRPCsUseSQLiteWithoutGoSnapshot(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	config, err := state.NewSqliteConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := state.InitStateRuntime(ctx, config, "openai")
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	store := session.NewStore(home)
	router := NewRouter(store)
	t.Cleanup(func() { _ = router.Close() })
	router.SetStateRuntime(runtime)

	now := fixedTime()
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome: home, SessionID: "sqlite-history", ThreadID: "sqlite-history", Source: "cli", CWD: home,
		ModelProvider: "openai", HistoryMode: "paginated", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.AppendTurnStarted("turn-1", now); err != nil {
		t.Fatal(err)
	}
	for index, item := range []map[string]any{
		{"type": "userMessage", "id": "user-1", "content": []map[string]any{{"type": "text", "text": "hello"}}},
		{"type": "reasoning", "id": "reasoning-1", "summary": []string{"thinking"}, "content": []string{}},
		{"type": "agentMessage", "id": "agent-1", "text": "done", "phase": "final_answer"},
	} {
		payload, marshalErr := json.Marshal(map[string]any{
			"type": "item_completed", "thread_id": "sqlite-history", "turn_id": "turn-1",
			"started_at_ms":   now.Add(time.Duration(index) * time.Second).UnixMilli(),
			"completed_at_ms": now.Add(time.Duration(index+1) * time.Second).UnixMilli(), "item": item,
		})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if err := recorder.AppendLine(rollout.Line{Type: "event_msg", Timestamp: now.Add(time.Duration(index+1) * time.Second).Format(time.RFC3339Nano), Payload: payload}); err != nil {
			t.Fatal(err)
		}
	}
	if err := recorder.AppendTurnComplete("turn-1", now.Add(5*time.Second), 5000); err != nil {
		t.Fatal(err)
	}
	path := recorder.Path()
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ReconcileRollout(ctx, path, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(store.Root(), "sqlite-history.json")); !os.IsNotExist(err) {
		t.Fatalf("unexpected Go snapshot: %v", err)
	}

	one := 1
	itemsResponse := router.Handle(requestWithParams(t, IntID(1), MethodThreadItemsList, ThreadItemsListParams{
		ThreadID: "sqlite-history", Limit: &one, SortDirection: SortAsc,
	}))
	if itemsResponse.Error != nil {
		t.Fatalf("items response error = %+v", itemsResponse.Error)
	}
	items := itemsResponse.Result.(*ThreadItemsListResponse)
	if len(items.Data) != 1 || items.Data[0].TurnID != "turn-1" || items.Data[0].Item.ID != "user-1" || items.NextCursor == nil {
		t.Fatalf("items page = %#v", items)
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Data []struct {
			TurnID string          `json:"turnId"`
			Item   json.RawMessage `json:"item"`
		} `json:"data"`
	}
	if err := json.Unmarshal(encoded, &wire); err != nil || len(wire.Data) != 1 || wire.Data[0].TurnID != "turn-1" || len(wire.Data[0].Item) == 0 {
		t.Fatalf("items wire = %s, err %v", encoded, err)
	}

	turnsResponse := router.Handle(requestWithParams(t, IntID(2), MethodThreadTurnsList, ThreadTurnsListParams{
		ThreadID: "sqlite-history", Limit: &one, SortDirection: SortDesc, ItemsView: TurnItemsSummary,
	}))
	if turnsResponse.Error != nil {
		t.Fatalf("turns response error = %+v", turnsResponse.Error)
	}
	turns := turnsResponse.Result.(*TurnsPage)
	if len(turns.Data) != 1 || turns.Data[0].ID != "turn-1" || turns.Data[0].Status != TurnStatusCompleted ||
		len(turns.Data[0].Items) != 2 || turns.Data[0].Items[0].ID != "user-1" || turns.Data[0].Items[1].ID != "agent-1" {
		t.Fatalf("turns page = %#v", turns)
	}
	if turns.BackwardsCursor == nil || !strings.Contains(*turns.BackwardsCursor, `"scope":{"kind":"turns"}`) {
		t.Fatalf("turn backwards cursor = %v", turns.BackwardsCursor)
	}

	zero := uint32(0)
	searchResponse := router.Handle(requestWithParams(t, IntID(21), MethodThreadSearchOccurrences, ThreadSearchOccurrencesParams{
		ThreadID: "sqlite-history", SearchTerm: "o", Limit: &zero,
	}))
	if searchResponse.Error != nil {
		t.Fatalf("search response error = %+v", searchResponse.Error)
	}
	search := searchResponse.Result.(*ThreadSearchOccurrencesResponse)
	if len(search.Data) != 1 || search.Data[0].ItemID != "user-1" || search.NextCursor == nil ||
		!strings.Contains(search.Data[0].TurnCursor, `"scope":{"kind":"turns"}`) {
		t.Fatalf("search page = %#v", search)
	}
	searchResponse = router.Handle(requestWithParams(t, IntID(22), MethodThreadSearchOccurrences, ThreadSearchOccurrencesParams{
		ThreadID: "sqlite-history", SearchTerm: "o", Cursor: search.NextCursor, Limit: &zero,
	}))
	if searchResponse.Error != nil {
		t.Fatalf("continued search response error = %+v", searchResponse.Error)
	}
	search = searchResponse.Result.(*ThreadSearchOccurrencesResponse)
	if len(search.Data) != 1 || search.Data[0].ItemID != "agent-1" || search.NextCursor != nil {
		t.Fatalf("continued search page = %#v", search)
	}

	resumeLimit := 1
	resumeResponse := router.Handle(requestWithParams(t, IntID(23), MethodThreadResume, ThreadResumeParams{
		ThreadID: "sqlite-history", ExcludeTurns: true,
		InitialTurnsPage: &ThreadInitialPageParams{Limit: &resumeLimit, SortDirection: SortDesc, ItemsView: TurnItemsSummary},
	}))
	if resumeResponse.Error != nil {
		t.Fatalf("paginated resume response error = %+v", resumeResponse.Error)
	}
	resumed := resumeResponse.Result.(*ThreadResumeResponse)
	if resumed.Thread == nil || len(resumed.Thread.Turns) != 0 || resumed.InitialTurnsPage == nil ||
		len(resumed.InitialTurnsPage.Data) != 1 || resumed.InitialTurnsPage.Data[0].ID != "turn-1" {
		t.Fatalf("paginated resume = %#v", resumed)
	}
	if resumed.TurnsBackwardsCursor == nil || resumed.ItemsBackwardsCursor == nil ||
		!strings.Contains(*resumed.TurnsBackwardsCursor, `"scope":{"kind":"turns"}`) ||
		!strings.Contains(*resumed.ItemsBackwardsCursor, `"scope":{"kind":"itemsByCreatedAtOrdinal"}`) {
		t.Fatalf("paginated resume cursors = turns %v items %v", resumed.TurnsBackwardsCursor, resumed.ItemsBackwardsCursor)
	}

	status := NewThreadStatusManager()
	runtimeRouter := NewRuntimeRouter(RuntimeServices{ThreadRouter: router, StateRuntime: runtime, ThreadStatus: status})
	runtimeItems := runtimeRouter.Handle(requestWithParams(t, IntID(3), MethodThreadItemsList, ThreadItemsListParams{ThreadID: "sqlite-history"}))
	if runtimeItems.Error != nil || len(runtimeItems.Result.(*ThreadItemsListResponse).Data) != 3 {
		t.Fatalf("runtime items = %#v error=%+v", runtimeItems.Result, runtimeItems.Error)
	}
	if err := runtimeRouter.registerActiveRuntimeTurn("sqlite-history", "turn-running", func() {}, now.Add(time.Minute).UnixMilli(), &turn.TurnStartParams{
		ThreadID: "sqlite-history", CWD: home, Model: "gpt-running", Prompt: "still running",
	}); err != nil {
		t.Fatal(err)
	}
	status.NoteTurnStarted("sqlite-history")
	runningResume := runtimeRouter.Handle(requestWithParams(t, IntID(24), MethodThreadResume, ThreadResumeParams{
		ThreadID: "sqlite-history", ExcludeTurns: true,
		InitialTurnsPage: &ThreadInitialPageParams{Limit: &resumeLimit, SortDirection: SortDesc, ItemsView: TurnItemsNotLoaded},
	}))
	if runningResume.Error != nil {
		t.Fatalf("running paginated resume error = %+v", runningResume.Error)
	}
	running := runningResume.Result.(*ThreadResumeResponse)
	if running.InitialTurnsPage == nil || len(running.InitialTurnsPage.Data) != 1 ||
		running.InitialTurnsPage.Data[0].ID != "turn-running" || running.InitialTurnsPage.Data[0].Status != TurnStatusInProgress ||
		running.InitialTurnsPage.NextCursor == nil || running.InitialTurnsPage.BackwardsCursor == nil {
		t.Fatalf("running paginated resume page = %#v", running.InitialTurnsPage)
	}
}

func TestRuntimeRouterFreshPaginatedThreadAllowsExcludedEphemeralForkWithSQLite(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	config, err := state.NewSqliteConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	stateRuntime, err := state.InitStateRuntime(ctx, config, "openai")
	if err != nil {
		t.Fatal(err)
	}
	defer stateRuntime.Close()

	threadRouter := NewRouter(session.NewStore(home))
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: threadRouter,
		ThreadStatus: NewThreadStatusManager(),
		StateRuntime: stateRuntime,
	})
	t.Cleanup(func() { _ = router.Close() })

	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:         home,
		HistoryMode: ThreadHistoryPaginated,
	}))
	if start.Error != nil {
		t.Fatalf("thread/start error = %+v", start.Error)
	}
	source := start.Result.(*ThreadStartResponse).Thread
	if source.Path == nil {
		t.Fatal("thread/start omitted planned rollout path")
	}
	if _, err := os.Stat(*source.Path); !os.IsNotExist(err) {
		t.Fatalf("fresh paginated source unexpectedly materialized: %v", err)
	}

	fork := router.Handle(requestWithParams(t, IntID(2), MethodThreadFork, ThreadForkParams{
		ThreadID:     source.ID,
		Ephemeral:    true,
		ExcludeTurns: true,
	}))
	if fork.Error != nil {
		t.Fatalf("thread/fork error = %+v", fork.Error)
	}
	forked := fork.Result.(*ThreadForkResponse).Thread
	if !forked.Ephemeral || forked.Path != nil || forked.HistoryMode != ThreadHistoryPaginated || len(forked.Turns) != 0 {
		t.Fatalf("fresh paginated fork = %+v", forked)
	}
}

func TestRouterPaginatedForkPersistsPhysicalHistoryReferenceOnly(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	config, err := state.NewSqliteConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := state.InitStateRuntime(ctx, config, "openai")
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	store := session.NewStore(home)
	router := NewRouter(store)
	t.Cleanup(func() { _ = router.Close() })
	router.SetStateRuntime(runtime)

	now := fixedTime()
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome: home, SessionID: "fork-source-sqlite", ThreadID: "fork-source-sqlite", Source: "cli", CWD: home,
		ModelProvider: "openai", HistoryMode: "paginated", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	for index, turnID := range []string{"turn-1", "turn-2"} {
		started := now.Add(time.Duration(index) * time.Minute)
		if err := recorder.AppendTurnStarted(turnID, started); err != nil {
			t.Fatal(err)
		}
		if err := rollout.AppendSessionItems(recorder, []session.Item{{
			ID: "user-" + turnID, Type: "message", Role: "user", Text: turnID, CreatedAt: started.Add(time.Second), Metadata: map[string]any{"turnId": turnID},
		}}, started); err != nil {
			t.Fatal(err)
		}
		if err := recorder.AppendTurnComplete(turnID, started.Add(2*time.Second), 2_000); err != nil {
			t.Fatal(err)
		}
	}
	sourcePath := recorder.Path()
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ReconcileRollout(ctx, sourcePath, false); err != nil {
		t.Fatal(err)
	}

	response := router.Handle(requestWithParams(t, IntID(41), MethodThreadFork, ThreadForkParams{
		ThreadID: "fork-source-sqlite", LastTurnID: "turn-1",
	}))
	if response.Error != nil {
		t.Fatalf("fork response error = %+v", response.Error)
	}
	forked := response.Result.(*ThreadForkResponse).Thread
	if forked == nil || forked.Path == nil {
		t.Fatalf("fork response = %#v", response.Result)
	}

	historyDB, err := runtime.ThreadHistoryDB(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var terminalOrdinal, terminalOffset int64
	if err := historyDB.QueryRowContext(ctx, `SELECT rollout_end_ordinal, rollout_end_byte_offset FROM thread_turns WHERE thread_id = ? AND turn_id = ?`, "fork-source-sqlite", "turn-1").Scan(&terminalOrdinal, &terminalOffset); err != nil {
		t.Fatal(err)
	}
	meta, err := rollout.FirstSessionMeta(*forked.Path)
	if err != nil {
		t.Fatal(err)
	}
	if meta.HistoryBase == nil || meta.HistoryBase.ThreadID != "fork-source-sqlite" || meta.HistoryBase.EndOrdinalExclusive != uint64(terminalOrdinal+1) || meta.HistoryBase.EndByteOffset != uint64(terminalOffset) {
		t.Fatalf("child history base = %#v, terminal = %d/%d", meta.HistoryBase, terminalOrdinal, terminalOffset)
	}
	lines, _, err := rollout.Load(*forked.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || lines[0].Type != "session_meta" || lines[0].Ordinal == nil || *lines[0].Ordinal != meta.HistoryBase.EndOrdinalExclusive {
		t.Fatalf("child rollout copied parent history: %#v", lines)
	}
	stored, err := store.Load(session.ThreadID(forked.ID))
	if err != nil {
		t.Fatal(err)
	}
	if stored.HistoryBase == nil || stored.HistoryBase.EndOrdinalExclusive != meta.HistoryBase.EndOrdinalExclusive || stored.HistoryBase.EndByteOffset != meta.HistoryBase.EndByteOffset {
		t.Fatalf("stored child history base = %#v", stored.HistoryBase)
	}
	if err := router.Close(); err != nil {
		t.Fatal(err)
	}
	reopenedStore := session.NewStore(home)
	restarted := NewRouter(reopenedStore)
	t.Cleanup(func() { _ = restarted.Close() })
	restarted.SetStateRuntime(runtime)
	materialized, err := reopenedStore.Read(session.ThreadID(forked.ID), true, true)
	if err != nil {
		t.Fatalf("read physical fork after restart: %v", err)
	}
	if len(materialized.Items) != 1 || materialized.Items[0].Text != "turn-1" || len(materialized.Metadata.RolloutTurns) != 1 || materialized.Metadata.RolloutTurns[0].ID != "turn-1" {
		t.Fatalf("restarted physical fork history = items %#v turns %#v", materialized.Items, materialized.Metadata.RolloutTurns)
	}
	local := session.Item{ID: "child-local", Type: "message", Role: "user", Text: "child", CreatedAt: now.Add(3 * time.Minute), Metadata: map[string]any{"turnId": "turn-child"}}
	if _, err := reopenedStore.AppendItems(session.ThreadID(forked.ID), []session.Item{local}); err != nil {
		t.Fatalf("append physical fork after restart: %v", err)
	}
	physical, err := reopenedStore.Load(session.ThreadID(forked.ID))
	if err != nil {
		t.Fatal(err)
	}
	if len(physical.Items) != 1 || physical.Items[0].ID != local.ID {
		t.Fatalf("physical child snapshot copied inherited history: %#v", physical.Items)
	}
}

func TestRouterLegacyThreadItemsListIsMethodNotFoundWithRustStateRuntime(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	config, err := state.NewSqliteConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := state.InitStateRuntime(ctx, config, "openai")
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	router := NewRouter(session.NewStore(home))
	router.SetStateRuntime(runtime)
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{CodexHome: home, ThreadID: "legacy-items", HistoryMode: "legacy", Now: fixedTime()})
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ReconcileRollout(ctx, recorder.Path(), false); err != nil {
		t.Fatal(err)
	}
	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadItemsList, ThreadItemsListParams{ThreadID: "legacy-items"}))
	if response.Error == nil || response.Error.Code != JSONRPCMethodNotFoundErrorCode || response.Error.Message != "thread/items/list is not supported yet" {
		t.Fatalf("legacy items response = %+v", response)
	}
}

func TestRouterRolloutLookupRepairsStaleMissingAndMismatchedState(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	config, err := state.NewSqliteConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := state.InitStateRuntime(ctx, config, "openai")
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	router := NewRouter(session.NewStore(home))
	router.SetStateRuntime(runtime)

	writeRollout := func(threadID string, now time.Time) string {
		recorder, err := rollout.NewRecorder(&rollout.CreateParams{
			CodexHome: home, ThreadID: threadID, Source: "cli", CWD: home,
			ModelProvider: "openai", HistoryMode: "paginated", Now: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := recorder.Close(); err != nil {
			t.Fatal(err)
		}
		if err := runtime.ReconcileRollout(ctx, recorder.Path(), false); err != nil {
			t.Fatal(err)
		}
		return recorder.Path()
	}
	pathA := writeRollout("repair-a", fixedTime())
	pathB := writeRollout("repair-b", fixedTime().Add(time.Second))

	if _, err := runtime.StateDB().ExecContext(ctx, `UPDATE threads SET rollout_path = ? WHERE id = 'repair-a'`, filepath.Join(home, "missing.jsonl")); err != nil {
		t.Fatal(err)
	}
	if got, err := router.findThreadRolloutPath("repair-a", false); err != nil || got != pathA {
		t.Fatalf("stale path fallback = %q, %v", got, err)
	}
	assertStateRolloutPath(t, runtime, "repair-a", pathA)

	if _, err := runtime.StateDB().ExecContext(ctx, `DELETE FROM threads WHERE id = 'repair-a'`); err != nil {
		t.Fatal(err)
	}
	if got, err := router.findThreadRolloutPath("repair-a", false); err != nil || got != pathA {
		t.Fatalf("missing row fallback = %q, %v", got, err)
	}
	assertStateRolloutPath(t, runtime, "repair-a", pathA)

	if _, err := runtime.StateDB().ExecContext(ctx, `UPDATE threads SET rollout_path = ? WHERE id = 'repair-a'`, pathB); err != nil {
		t.Fatal(err)
	}
	if got, err := router.findThreadRolloutPath("repair-a", false); err != nil || got != pathA {
		t.Fatalf("mismatched path fallback = %q, %v", got, err)
	}
	assertStateRolloutPath(t, runtime, "repair-a", pathA)
}

func TestRouterThreadListUsesStateRowsAndScanRepairWithoutSnapshots(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	config, err := state.NewSqliteConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := state.InitStateRuntime(ctx, config, "openai")
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	router := NewRouter(session.NewStore(home))
	router.SetStateRuntime(runtime)

	const (
		parentID = "0198f001-0000-7000-8000-000000000001"
		childID  = "0198f001-0000-7000-8000-000000000002"
		newID    = "0198f001-0000-7000-8000-000000000003"
		staleID  = "0198f001-0000-7000-8000-000000000004"
		section  = "0198f001-0000-7000-8000-000000000010"
	)
	writeRollout := func(threadID, preview string, now time.Time, reconcile bool) string {
		recorder, err := rollout.NewRecorder(&rollout.CreateParams{
			CodexHome: home, ThreadID: threadID, Source: "cli", CWD: home,
			ModelProvider: "openai", HistoryMode: "paginated", Now: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		payload, _ := json.Marshal(map[string]any{"type": "user_message", "message": preview})
		if err := recorder.AppendLine(rollout.Line{Type: "event_msg", Timestamp: now.Add(time.Second).Format(time.RFC3339Nano), Payload: payload}); err != nil {
			t.Fatal(err)
		}
		if err := recorder.Close(); err != nil {
			t.Fatal(err)
		}
		if reconcile {
			if err := runtime.ReconcileRollout(ctx, recorder.Path(), false); err != nil {
				t.Fatal(err)
			}
		}
		return recorder.Path()
	}
	parentPath := writeRollout(parentID, "alpha request", fixedTime(), true)
	_ = writeRollout(childID, "bravo request", fixedTime().Add(time.Second), true)
	_ = writeRollout(newID, "charlie request", fixedTime().Add(2*time.Second), false)
	stalePath := writeRollout(staleID, "stale request", fixedTime().Add(3*time.Second), true)
	if err := os.Remove(stalePath); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.StateDB().ExecContext(ctx, `INSERT INTO thread_sections (id, name) VALUES (?, 'Work')`, section); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.StateDB().ExecContext(ctx, `UPDATE threads SET name = 'Alpha Name', thread_section_id = ?, section_position = 1000000, section_entered_at_ms = 123000 WHERE id = ?`, section, parentID); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.StateDB().ExecContext(ctx, `INSERT INTO thread_spawn_edges (parent_thread_id, child_thread_id, status) VALUES (?, ?, 'running')`, parentID, childID); err != nil {
		t.Fatal(err)
	}
	limit := 20

	stateOnly := router.Handle(requestWithParams(t, IntID(1), MethodThreadList, ThreadListParams{UseStateDBOnly: true, Limit: &limit}))
	if stateOnly.Error != nil {
		t.Fatalf("state-only list error: %+v", stateOnly.Error)
	}
	stateThreads := stateOnly.Result.(*ThreadListResponse).Data
	if got := threadListIDs(stateThreads); !reflect.DeepEqual(got, []string{childID, parentID}) {
		t.Fatalf("state-only IDs = %v", got)
	}
	for i := range stateThreads {
		if stateThreads[i].ID == parentID && (stateThreads[i].Name == nil || *stateThreads[i].Name != "Alpha Name" || stateThreads[i].Path == nil || *stateThreads[i].Path != parentPath) {
			t.Fatalf("state parent thread = %+v", stateThreads[i])
		}
	}

	normal := router.Handle(requestWithParams(t, IntID(2), MethodThreadList, ThreadListParams{Limit: &limit}))
	if normal.Error != nil {
		t.Fatalf("scan-repair list error: %+v", normal.Error)
	}
	if got := threadListIDs(normal.Result.(*ThreadListResponse).Data); !reflect.DeepEqual(got, []string{newID, childID, parentID}) {
		t.Fatalf("scan-repair IDs = %v", got)
	}
	assertStateRolloutPath(t, runtime, newID, rollout.PathForThread(home, newID, fixedTime().Add(2*time.Second)))

	sectionParams := ThreadListParams{UseStateDBOnly: true, Limit: &limit, SectionID: OptionalString{Set: true, Value: stringPtr(section)}}
	sectionList := router.Handle(requestWithParams(t, IntID(3), MethodThreadList, sectionParams))
	if sectionList.Error != nil || !reflect.DeepEqual(threadListIDs(sectionList.Result.(*ThreadListResponse).Data), []string{parentID}) {
		t.Fatalf("section list = %#v error=%+v", sectionList.Result, sectionList.Error)
	}

	relationList := router.Handle(requestWithParams(t, IntID(4), MethodThreadList, ThreadListParams{UseStateDBOnly: true, Limit: &limit, ParentThreadID: stringPtr(parentID)}))
	if relationList.Error != nil || !reflect.DeepEqual(threadListIDs(relationList.Result.(*ThreadListResponse).Data), []string{childID}) {
		t.Fatalf("relation list = %#v error=%+v", relationList.Result, relationList.Error)
	}
}

func threadListIDs(threads []Thread) []string {
	ids := make([]string, 0, len(threads))
	for i := range threads {
		ids = append(ids, threads[i].ID)
	}
	return ids
}

func assertStateRolloutPath(t *testing.T, runtime *state.StateRuntime, threadID, want string) {
	t.Helper()
	var got string
	if err := runtime.StateDB().QueryRow(`SELECT rollout_path FROM threads WHERE id = ?`, threadID).Scan(&got); err != nil || got != filepath.Clean(want) {
		t.Fatalf("state rollout path for %s = %q, %v; want %q", threadID, got, err, want)
	}
}

func TestStateRuntimeStartupRecoversOnlyCorruptDatabase(t *testing.T) {
	ctx := context.Background()
	config, err := state.NewSqliteConfig(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := state.InitStateRuntime(ctx, config, "openai")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.StateDB().ExecContext(ctx, `CREATE TABLE go_recovery_marker (value TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.StateDB().ExecContext(ctx, `INSERT INTO go_recovery_marker (value) VALUES ('preserved')`); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.LogsDBPath(), []byte("not sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.WriteFile(config.LogsDBPath()+suffix, []byte("sidecar"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	recovered, err := initStateRuntimeWithFreshStartOnCorruption(ctx, config, "openai")
	if err != nil {
		t.Fatalf("recovery error = %v", err)
	}
	defer recovered.Close()
	var marker string
	if err := recovered.StateDB().QueryRowContext(ctx, `SELECT value FROM go_recovery_marker`).Scan(&marker); err != nil || marker != "preserved" {
		t.Fatalf("unrelated state database was not preserved: %q, %v", marker, err)
	}
	var migrationCount int
	if err := recovered.LogsDB().QueryRowContext(ctx, `SELECT COUNT(*) FROM _sqlx_migrations`).Scan(&migrationCount); err != nil || migrationCount != 2 {
		t.Fatalf("recreated logs migrations = %d, %v", migrationCount, err)
	}
	backupMatches, err := filepath.Glob(filepath.Join(config.Home(), "db-backups", "sqlite-*", state.LogsSQLiteFilename+"*"))
	if err != nil || len(backupMatches) == 0 {
		t.Fatalf("logs backup files = %v, %v", backupMatches, err)
	}
	stateBackups, err := filepath.Glob(filepath.Join(config.Home(), "db-backups", "sqlite-*", state.StateSQLiteFilename+"*"))
	if err != nil || len(stateBackups) != 0 {
		t.Fatalf("state database must not be backed up with logs: %v, %v", stateBackups, err)
	}
}

func TestStdioStartupFailsClosedOnKnownMigrationMismatch(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	config, err := state.NewSqliteConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := state.InitStateRuntime(ctx, config, "openai")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.StateDB().ExecContext(ctx, `UPDATE _sqlx_migrations SET checksum = X'00' WHERE version = 1`); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	server := NewDefaultStdioServer(&StdioOptions{CodexHome: home})
	if server.router.StartupError() == nil {
		t.Fatal("known migration mismatch must fail startup")
	}
	err = server.Serve(strings.NewReader(""), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "migration 1 checksum mismatch") {
		t.Fatalf("Serve() error = %v", err)
	}
	if matches, globErr := filepath.Glob(filepath.Join(home, "db-backups", "sqlite-*")); globErr != nil || len(matches) != 0 {
		t.Fatalf("checksum mismatch must not trigger corruption backup: %v, %v", matches, globErr)
	}
}
func TestRuntimeRouterThreadGoalFreshPaginatedThreadMaterializesRolloutOnDemand(t *testing.T) {
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
	now := time.Date(2026, 8, 15, 8, 0, 0, 0, time.UTC)
	threadID := session.ThreadID("goal-fresh-thread")
	rolloutPath := rollout.PathForThread(home, string(threadID), now)
	record := &session.Record{
		ID: threadID, SessionID: string(threadID), CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{
			CWD:           home,
			ModelProvider: "openai",
			HistoryMode:   "paginated",
			Extra:         map[string]any{"rollout_path": rolloutPath},
		},
	}
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(rolloutPath); !os.IsNotExist(err) {
		t.Fatalf("fixture requires a missing rollout file at %s (stat err=%v)", rolloutPath, err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: threadRouter, StateRuntime: stateRuntime})

	objective := "improve benchmark coverage"
	set := router.Handle(requestWithParams(t, IntID(1), MethodThreadGoalSet, GoalSetParams{ThreadID: string(threadID), Objective: &objective}))
	if set.Error != nil {
		t.Fatalf("thread/goal/set on fresh paginated thread error: %+v", set.Error)
	}
	goal := set.Result.(*GoalSetResponse).Goal
	if goal.Status != GoalActive || goal.Objective != objective {
		t.Fatalf("created goal = %#v", goal)
	}
	if _, err := os.Stat(rolloutPath); err != nil {
		t.Fatalf("thread/goal/set should materialize the rollout file at %s: %v", rolloutPath, err)
	}
	get := router.Handle(requestWithParams(t, IntID(2), MethodThreadGoalGet, GoalGetParams{ThreadID: string(threadID)}))
	if get.Error != nil || get.Result.(*GoalGetResponse).Goal == nil || get.Result.(*GoalGetResponse).Goal.Objective != objective {
		t.Fatalf("thread/goal/get after set = %+v", get)
	}
	clear := router.Handle(requestWithParams(t, IntID(3), MethodThreadGoalClear, GoalClearParams{ThreadID: string(threadID)}))
	if clear.Error != nil || !clear.Result.(*GoalClearResponse).Cleared {
		t.Fatalf("thread/goal/clear = %+v", clear)
	}
	missing := router.Handle(requestWithParams(t, IntID(4), MethodThreadGoalGet, GoalGetParams{ThreadID: string(threadID)}))
	if missing.Error != nil || missing.Result.(*GoalGetResponse).Goal != nil {
		t.Fatalf("thread/goal/get after clear = %+v", missing)
	}
}

func TestRuntimeRouterThreadGoalFeatureGateMatchesRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("model = \"gpt-5\"\n[features]\ngoals = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := session.NewStore(home)
	createRecord(t, store, "thread-goal-gate", time.Now())
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
	})

	objective := "disabled goal"
	set := router.Handle(requestWithParams(t, IntID(1), MethodThreadGoalSet, GoalSetParams{ThreadID: "thread-goal-gate", Objective: &objective}))
	if set.Error == nil || set.Error.Code != JSONRPCInvalidRequestErrorCode || set.Error.Message != "goals feature is disabled" {
		t.Fatalf("thread/goal/set with goals disabled = %+v", set)
	}
	get := router.Handle(requestWithParams(t, IntID(2), MethodThreadGoalGet, GoalGetParams{ThreadID: "thread-goal-gate"}))
	if get.Error == nil || get.Error.Code != JSONRPCInvalidRequestErrorCode || get.Error.Message != "goals feature is disabled" {
		t.Fatalf("thread/goal/get with goals disabled = %+v", get)
	}
	clear := router.Handle(requestWithParams(t, IntID(3), MethodThreadGoalClear, GoalClearParams{ThreadID: "thread-goal-gate"}))
	if clear.Error == nil || clear.Error.Code != JSONRPCInvalidRequestErrorCode || clear.Error.Message != "goals feature is disabled" {
		t.Fatalf("thread/goal/clear with goals disabled = %+v", clear)
	}
}

func TestRuntimeRouterThreadGoalFeatureGateSeededFromOptions(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(filepath.Join(home, "sessions"))
	router := NewDefaultRuntimeRouterWithOptions(store, home, &RuntimeRouterOptions{
		FeatureEnablement: map[string]bool{"goals": false},
	})
	defer router.Close()
	createRecord(t, store, "thread-goal-options", time.Now())

	objective := "disabled goal"
	set := router.Handle(requestWithParams(t, IntID(1), MethodThreadGoalSet, GoalSetParams{ThreadID: "thread-goal-options", Objective: &objective}))
	if set.Error == nil || set.Error.Code != JSONRPCInvalidRequestErrorCode || set.Error.Message != "goals feature is disabled" {
		t.Fatalf("thread/goal/set with goals disabled via options = %+v", set)
	}
	get := router.Handle(requestWithParams(t, IntID(2), MethodThreadGoalGet, GoalGetParams{ThreadID: "thread-goal-options"}))
	if get.Error == nil || get.Error.Message != "goals feature is disabled" {
		t.Fatalf("thread/goal/get with goals disabled via options = %+v", get)
	}
}
