package appserver

import (
	"errors"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/session"
)

func TestProvisionalSpawnCleanupDiscardsChildAndClosesEdgeLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	parent := &session.Record{ID: "parent", SessionID: "parent", CreatedAt: now, UpdatedAt: now, RecencyAt: now}
	child := &session.Record{
		ID: "child", SessionID: "child", ParentThreadID: "parent", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: t.TempDir(), AgentPath: "/root/worker"},
	}
	if err := store.Create(parent); err != nil {
		t.Fatal(err)
	}
	if err := store.Create(child); err != nil {
		t.Fatal(err)
	}
	graph := agent.NewMemoryStore()
	if err := graph.UpsertThreadSpawnEdge("parent", "child", agent.ThreadSpawnEdgeOpen); err != nil {
		t.Fatal(err)
	}
	registry := agent.NewRegistry()
	registry.RegisterRootThread("parent")
	registry.RegisterSpawnedThread(agent.Metadata{ThreadID: "child", Path: "/root/worker"})
	controller := &runtimeAgentController{
		router:   NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), SpawnGraph: graph}),
		registry: registry,
	}

	controller.cleanupProvisionalSpawn("child")

	if _, err := store.Read("child", true, false); !errors.Is(err, session.ErrThreadNotFound) {
		t.Fatalf("Read(child) error = %v, want thread not found", err)
	}
	open := agent.ThreadSpawnEdgeOpen
	if children, err := graph.ListThreadSpawnChildren("parent", &open); err != nil || len(children) != 0 {
		t.Fatalf("open children = %#v, error = %v", children, err)
	}
	closed := agent.ThreadSpawnEdgeClosed
	if children, err := graph.ListThreadSpawnChildren("parent", &closed); err != nil || len(children) != 1 || children[0] != "child" {
		t.Fatalf("closed children = %#v, error = %v", children, err)
	}
	if metadata, ok := registry.MetadataForThread("child"); ok {
		t.Fatalf("cancelled child remained registered: %#v", metadata)
	}
}
