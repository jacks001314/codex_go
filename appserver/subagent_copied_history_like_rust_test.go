package appserver

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/rollout"
	"codex_go/session"
)

// TestRuntimeAgentControllerV2SpawnCopiesPaginatedParentHistoryLikeRust mirrors
// Rust #61833 `ForkPersistence::CopiedDeferred`: a spawned child persists the
// inherited model context as its own rollout prefix, records the first ordinal
// that belongs to its own history in session metadata, and keeps that history
// after the parent rollout disappears.
func TestRuntimeAgentControllerV2SpawnCopiesPaginatedParentHistoryLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	threadRouter := NewRouter(store)
	now := time.Now().UTC().Truncate(time.Second)
	parent := &session.Record{
		ID: "parent-copied-history", SessionID: "parent-copied-history",
		CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{
			CWD:           t.TempDir(),
			Model:         "gpt-5.4",
			ModelProvider: "openai",
			HistoryMode:   string(ThreadHistoryPaginated),
			RolloutTurns:  []session.TurnSnapshot{{ID: "turn-parent", Status: string(TurnStatusCompleted)}},
		},
		Items: []session.Item{
			{ID: "parent-user", Type: "message", Role: "user", Text: "parent prompt", CreatedAt: now, Metadata: map[string]any{"turnId": "turn-parent"}},
			{ID: "parent-assistant", Type: "message", Role: "assistant", Text: "parent answer", CreatedAt: now, Metadata: map[string]any{"turnId": "turn-parent"}},
		},
	}
	if err := store.Create(parent); err != nil {
		t.Fatal(err)
	}
	if err := threadRouter.createThreadRollout(parent, now); err != nil {
		t.Fatalf("createThreadRollout(parent) error = %v", err)
	}

	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: threadRouter})
	controller := newRuntimeAgentControllerWithVersion(router, string(parent.ID), parent.Metadata.CWD, 4, agent.VersionV2).(*runtimeAgentController)
	message := "child work"
	child, err := controller.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{ResolvedRole: "worker", Message: &message})
	if err != nil {
		t.Fatalf("SpawnAgent() error = %v", err)
	}

	childRecord, err := store.Read(session.ThreadID(child.AgentID), true, true)
	if err != nil {
		t.Fatalf("Read(child) error = %v", err)
	}
	if childRecord.HistoryBase != nil {
		t.Fatalf("child history base = %#v, want copied history without a parent reference", childRecord.HistoryBase)
	}
	if !threadHasItemText(childRecord.Items, "parent answer") {
		t.Fatalf("child items = %#v, want the copied parent history", childRecord.Items)
	}
	startOrdinal := childRecord.Metadata.SubagentHistoryStartOrdinal
	if startOrdinal == nil || *startOrdinal == 0 {
		t.Fatalf("subagent_history_start_ordinal = %v, want the first child-owned rollout ordinal", startOrdinal)
	}

	path := threadRouter.threadRolloutPath(childRecord)
	if strings.TrimSpace(path) == "" {
		t.Fatal("child rollout path is empty")
	}
	meta, err := rollout.FirstSessionMeta(path)
	if err != nil {
		t.Fatalf("FirstSessionMeta() error = %v", err)
	}
	if meta.SubagentHistoryStartOrdinal == nil || *meta.SubagentHistoryStartOrdinal != *startOrdinal {
		t.Fatalf("rollout subagent_history_start_ordinal = %v, want %d", meta.SubagentHistoryStartOrdinal, *startOrdinal)
	}
	lines, _, err := rollout.Load(path)
	if err != nil {
		t.Fatalf("Load(child rollout) error = %v", err)
	}
	prefixHasParent := false
	for _, line := range lines {
		if line.Ordinal != nil && *line.Ordinal < *startOrdinal && strings.Contains(string(line.Item), "parent answer") {
			prefixHasParent = true
		}
		if line.Ordinal != nil && *line.Ordinal >= *startOrdinal && strings.Contains(string(line.Item), "parent answer") {
			t.Fatalf("copied parent item leaked into the child's own history at ordinal %d", *line.Ordinal)
		}
	}
	if !prefixHasParent {
		t.Fatalf("rollout lines = %#v, want the copied parent history before ordinal %d", lines, *startOrdinal)
	}

	// The copied child must not depend on the parent rollout any more.
	parentPath := threadRouter.threadRolloutPath(parent)
	if err := os.Remove(parentPath); err != nil {
		t.Fatalf("remove parent rollout: %v", err)
	}
	cold := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	coldController := newRuntimeAgentController(cold, string(parent.ID), parent.Metadata.CWD, 4)
	resumed, err := coldController.ResumeAgent(context.Background(), &agent.ResumeAgentArgs{ID: child.AgentID})
	if err != nil {
		t.Fatalf("ResumeAgent(child) after parent rollout removal error = %v", err)
	}
	if resumed.Status.Kind == agent.AgentMessageStatusNotFound {
		t.Fatalf("resumed child status = %+v, want recovered history", resumed.Status)
	}
	reopened, err := store.Read(session.ThreadID(child.AgentID), true, true)
	if err != nil {
		t.Fatalf("Read(child) after parent rollout removal error = %v", err)
	}
	if !threadHasItemText(reopened.Items, "parent answer") {
		t.Fatalf("reopened child items = %#v, want the copied parent history to survive", reopened.Items)
	}
}

func threadHasItemText(items []session.Item, text string) bool {
	for i := range items {
		if strings.Contains(items[i].Text, text) {
			return true
		}
	}
	return false
}
