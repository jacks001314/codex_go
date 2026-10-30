package appserver

import (
	"context"
	"errors"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/session"
)

type upsertFailingSpawnGraph struct{}

func (upsertFailingSpawnGraph) UpsertThreadSpawnEdge(string, string, agent.ThreadSpawnEdgeStatus) error {
	return errors.New("spawn edge write failed")
}

func (upsertFailingSpawnGraph) SetThreadSpawnEdgeStatus(string, agent.ThreadSpawnEdgeStatus) error {
	return nil
}

func (upsertFailingSpawnGraph) ListThreadSpawnChildren(string, *agent.ThreadSpawnEdgeStatus) ([]string, error) {
	return nil, nil
}

func (upsertFailingSpawnGraph) ListThreadSpawnDescendants(string, *agent.ThreadSpawnEdgeStatus) ([]string, error) {
	return nil, nil
}

// TestSpawnPublishesGraphEdgeBeforeChildStorageLikeRust pins Rust #58129: a
// failed spawn-edge publication must leave no child storage behind.
func TestSpawnPublishesGraphEdgeBeforeChildStorageLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	parent := &session.Record{ID: "parent", SessionID: "parent", CreatedAt: now, UpdatedAt: now, RecencyAt: now, Metadata: session.Metadata{CWD: t.TempDir(), Model: "gpt-parent", ModelProvider: "openai"}}
	if err := store.Create(parent); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), SpawnGraph: upsertFailingSpawnGraph{}})
	runtimeController := newRuntimeAgentController(router, "parent", parent.Metadata.CWD, 1).(*runtimeAgentController)
	preset, ok := firstSpawnAgentCatalogPreset(runtimeController)
	if !ok {
		t.Skip("the catalog has no picker-visible V2 spawn model")
	}
	modelID := preset.Model
	if _, err := agent.ToolController(runtimeController).SpawnAgent(context.Background(), &agent.SpawnAgentArgs{Model: &modelID, ResolvedRole: "worker"}); err == nil {
		t.Fatal("SpawnAgent() error = nil, want spawn-edge failure")
	}
	records, err := store.AllRecords()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].ID != "parent" {
		t.Fatalf("records after failed edge publication = %#v, want only parent", records)
	}
}
