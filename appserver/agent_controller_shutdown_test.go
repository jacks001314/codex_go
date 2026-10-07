package appserver

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/session"
)

// failingSpawnGraph is an agent.Store whose teardown writes fail, so the close
// path exercises Rust #51515's failure report instead of dropping the errors.
type failingSpawnGraph struct {
	listErr error
	setErr  error
}

func (failingSpawnGraph) UpsertThreadSpawnEdge(string, string, agent.ThreadSpawnEdgeStatus) error {
	return nil
}

func (s failingSpawnGraph) SetThreadSpawnEdgeStatus(string, agent.ThreadSpawnEdgeStatus) error {
	return s.setErr
}

func (failingSpawnGraph) ListThreadSpawnChildren(string, *agent.ThreadSpawnEdgeStatus) ([]string, error) {
	return nil, nil
}

func (s failingSpawnGraph) ListThreadSpawnDescendants(string, *agent.ThreadSpawnEdgeStatus) ([]string, error) {
	return nil, s.listErr
}

// TestRuntimeAgentControllerShutdownReportLikeRust mirrors Rust #51515: agent
// tree shutdown reports each failed operation with a stable, payload-free error
// category instead of a generic error, and the report travels back to the caller.
func TestRuntimeAgentControllerShutdownReportLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	parent := &session.Record{ID: "parent", SessionID: "parent", CreatedAt: now, UpdatedAt: now, RecencyAt: now, Metadata: session.Metadata{CWD: t.TempDir(), Model: "gpt-parent", ModelProvider: "openai"}}
	if err := store.Create(parent); err != nil {
		t.Fatal(err)
	}
	graph := failingSpawnGraph{
		listErr: fmt.Errorf("wrapped: %w", session.ErrConflict),
		setErr:  fmt.Errorf("spawn graph write failed: secret-payload"),
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), SpawnGraph: graph})
	controller := newRuntimeAgentController(router, "parent", parent.Metadata.CWD, 1).(*runtimeAgentController)
	preset, ok := firstSpawnAgentCatalogPreset(controller)
	if !ok {
		t.Skip("the catalog has no picker-visible V2 spawn model")
	}
	modelID := preset.Model
	spawned, err := controller.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{Model: &modelID, ResolvedRole: "reviewer", NicknameCandidates: []string{"Sage"}})
	if err != nil {
		t.Fatal(err)
	}
	closed, err := controller.CloseAgent(context.Background(), &agent.CloseAgentArgs{Target: spawned.AgentID})
	if err != nil {
		t.Fatal(err)
	}
	report := closed.ShutdownReport
	if report == nil {
		t.Fatal("a failed teardown returned no shutdown report")
	}
	if report.TreeID != "parent" {
		t.Fatalf("tree id = %q, want the runtime correlation id", report.TreeID)
	}
	if len(report.Failures) != 2 {
		t.Fatalf("failures = %#v, want the list and spawn-edge failures", report.Failures)
	}
	if report.Failures[0].Operation != "list_descendants" || report.Failures[0].ErrorKind != "thread_store_conflict" {
		t.Fatalf("first failure = %#v", report.Failures[0])
	}
	if report.Failures[1].Operation != "close_spawn_edge" || report.Failures[1].ThreadID != spawned.AgentID || report.Failures[1].ErrorKind != "operation_error" {
		t.Fatalf("second failure = %#v", report.Failures[1])
	}
	text := report.String()
	if strings.Contains(text, "secret-payload") || strings.Contains(text, "wrapped") {
		t.Fatalf("report leaked a raw error payload: %q", text)
	}
	if !strings.Contains(text, "error_kind=thread_store_conflict") {
		t.Fatalf("report = %q", text)
	}
}

// TestRuntimeAgentControllerCleanShutdownHasNoReportLikeRust mirrors Rust's
// successful tree shutdown: a clean teardown reports no failures at all, so the
// existing CloseAgentResult shape is preserved.
func TestRuntimeAgentControllerCleanShutdownHasNoReportLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	parent := &session.Record{ID: "parent", SessionID: "parent", CreatedAt: now, UpdatedAt: now, RecencyAt: now, Metadata: session.Metadata{CWD: t.TempDir(), Model: "gpt-parent", ModelProvider: "openai"}}
	if err := store.Create(parent); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), SpawnGraph: agent.NewMemoryStore()})
	controller := newRuntimeAgentController(router, "parent", parent.Metadata.CWD, 1).(*runtimeAgentController)
	preset, ok := firstSpawnAgentCatalogPreset(controller)
	if !ok {
		t.Skip("the catalog has no picker-visible V2 spawn model")
	}
	modelID := preset.Model
	spawned, err := controller.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{Model: &modelID, ResolvedRole: "reviewer", NicknameCandidates: []string{"Sage"}})
	if err != nil {
		t.Fatal(err)
	}
	closed, err := controller.CloseAgent(context.Background(), &agent.CloseAgentArgs{Target: spawned.AgentID})
	if err != nil {
		t.Fatal(err)
	}
	if closed.ShutdownReport != nil {
		t.Fatalf("clean shutdown reported failures: %#v", closed.ShutdownReport)
	}
}
