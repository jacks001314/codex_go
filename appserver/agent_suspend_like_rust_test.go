package appserver

import (
	"context"
	"strings"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/session"
	"codex_go/turn"
)

func newSuspendedAgentTreeFixture(t *testing.T) (*RuntimeRouter, *runtimeAgentController) {
	t.Helper()
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	root := &session.Record{
		ID: "root", SessionID: "root", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: t.TempDir(), Model: "gpt-parent", ModelProvider: "openai"},
	}
	child := &session.Record{
		ID: "child", SessionID: "child", ParentThreadID: "root", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{
			CWD: root.Metadata.CWD, Model: "gpt-parent", ModelProvider: "openai",
			ThreadSource: "subagentThreadSpawn", AgentPath: "/root/worker", AgentNickname: "Sage",
		},
	}
	for _, record := range []*session.Record{root, child} {
		if err := store.Create(record); err != nil {
			t.Fatalf("store.Create(%s): %v", record.ID, err)
		}
	}
	graph := agent.NewMemoryStore()
	if err := graph.UpsertThreadSpawnEdge("root", "child", agent.ThreadSpawnEdgeOpen); err != nil {
		t.Fatalf("UpsertThreadSpawnEdge: %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), SpawnGraph: graph})
	controller := newRuntimeAgentController(router, "root", root.Metadata.CWD, 1).(*runtimeAgentController)
	return router, controller
}

// Mirrors Rust #61198's agent-tree suspension: the root and its open named V2
// child report the turns that were running, both are stopped, and the threads,
// records and queued mail stay available for a later load.
func TestSuspendAgentTreeStopsTurnsAndPreservesStateLikeRust(t *testing.T) {
	router, controller := newSuspendedAgentTreeFixture(t)
	cancelled := make(chan string, 2)
	for _, turnID := range []struct{ threadID, turnID string }{
		{threadID: "root", turnID: "root-turn"},
		{threadID: "child", turnID: "child-turn"},
	} {
		threadID, id := turnID.threadID, turnID.turnID
		cancel := func() {
			cancelled <- threadID
			router.threads.ConsumeTurn(threadID, id, false)
		}
		if err := router.threads.RegisterTurn(threadID, id, cancel, time.Now().UnixMilli(), &turn.TurnStartParams{ThreadID: threadID}); err != nil {
			t.Fatalf("RegisterTurn(%s): %v", threadID, err)
		}
	}
	router.enqueueRuntimeAgentMessage("root", runtimeAgentCommunicationInputItem("/root/worker", "/root", "Suspended child result.", false, true))

	suspended, err := controller.SuspendAgentTree(context.Background(), "root")
	if err != nil {
		t.Fatalf("SuspendAgentTree() error = %v", err)
	}
	if suspended == nil || suspended.RootTurnID != "root-turn" {
		t.Fatalf("suspended = %#v", suspended)
	}
	if suspended.ChildTurns["child"] != "child-turn" {
		t.Fatalf("child turns = %#v", suspended.ChildTurns)
	}
	for range 2 {
		select {
		case <-cancelled:
		case <-time.After(2 * time.Second):
			t.Fatal("suspension did not stop every running turn")
		}
	}
	if id := router.threads.ActiveTurnID("root"); id != "" {
		t.Fatalf("root still runs %q after suspension", id)
	}
	if id := router.threads.ActiveTurnID("child"); id != "" {
		t.Fatalf("child still runs %q after suspension", id)
	}
	// The tree is suspended, not closed: records survive and queued mail stays
	// queued until the replacement host loads the agent.
	if _, err := router.threadRecord("child", true, false); err != nil {
		t.Fatalf("suspended child record: %v", err)
	}
	queued := router.drainRuntimeAgentMessages("root")
	if len(queued) != 1 {
		t.Fatalf("queued mail after suspension = %d, want 1", len(queued))
	}
	// A second suspension observes the same stopped state without inventing work.
	again, err := controller.SuspendAgentTree(context.Background(), "root")
	if err != nil {
		t.Fatalf("second SuspendAgentTree() error = %v", err)
	}
	if again.RootTurnID != "" || len(again.ChildTurns) != 0 {
		t.Fatalf("repeated suspension = %#v, want no running turns", again)
	}
}

// Loading a suspended agent restores its recorded identity without starting a
// model turn (Rust #61198 load_suspended_agent).
func TestLoadSuspendedAgentRestoresIdentityWithoutStartingLikeRust(t *testing.T) {
	router, controller := newSuspendedAgentTreeFixture(t)
	record, err := controller.LoadSuspendedAgent(context.Background(), "child")
	if err != nil {
		t.Fatalf("LoadSuspendedAgent() error = %v", err)
	}
	if record == nil || record.ID != session.ThreadID("child") || record.Metadata.AgentPath != "/root/worker" {
		t.Fatalf("loaded record = %#v", record)
	}
	if id := router.threads.ActiveTurnID("child"); id != "" {
		t.Fatalf("loading started turn %q", id)
	}
	if _, err := controller.LoadSuspendedAgent(context.Background(), "missing"); err == nil {
		t.Fatal("loading an unknown agent succeeded")
	}
	if _, err := controller.LoadSuspendedAgent(context.Background(), "  "); err == nil ||
		!strings.Contains(err.Error(), "id is required") {
		t.Fatalf("blank id error = %v", err)
	}
}

// A caller that cancels suspension still observes the reported turns; the wait
// itself fails closed instead of pretending the turns stopped (Rust #61198
// tests repeated suspension after caller cancellation).
func TestSuspendAgentTreeReportsCancelledWaitLikeRust(t *testing.T) {
	router, controller := newSuspendedAgentTreeFixture(t)
	if err := router.threads.RegisterTurn("root", "root-turn", func() {}, time.Now().UnixMilli(), &turn.TurnStartParams{ThreadID: "root"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	suspended, err := controller.SuspendAgentTree(ctx, "root")
	if err == nil {
		t.Fatal("suspension with a cancelled caller reported success")
	}
	if suspended == nil || suspended.RootTurnID != "root-turn" {
		t.Fatalf("suspended = %#v", suspended)
	}
}
