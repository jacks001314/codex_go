package appserver

import (
	"context"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/session"
)

// Rust #47267 collects the initial snapshot before waiting on updates and
// treats a missing thread as a final NotFound snapshot.
func TestRuntimeAgentControllerWaitAgentUsesInitialSnapshotsLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	child := &session.Record{ID: "child", SessionID: "child", ParentThreadID: "parent", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: t.TempDir(), RolloutTurns: []session.TurnSnapshot{{ID: "turn-1", Status: "completed"}}}}
	if err := store.Create(child); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	controller := newRuntimeAgentController(router, "parent", child.Metadata.CWD, 1)

	result, err := controller.WaitAgent(context.Background(), &agent.WaitAgentArgs{Targets: []string{"child", "missing"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.TimedOut {
		t.Fatalf("final initial snapshots still waited: %+v", result)
	}
	if status := result.Status["child"]; status.Kind != agent.AgentMessageStatusCompleted {
		t.Fatalf("child status = %+v", status)
	}
	if status := result.Status["missing"]; status.Kind != agent.AgentMessageStatusNotFound {
		t.Fatalf("missing status = %+v", status)
	}
}
