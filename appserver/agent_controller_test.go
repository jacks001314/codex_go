package appserver

import (
	"context"
	"errors"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/session"
)

func TestRuntimeAgentControllerPersistsSpawnMetadataAndGraph(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	parent := &session.Record{ID: "parent", SessionID: "parent", CreatedAt: now, UpdatedAt: now, RecencyAt: now, Metadata: session.Metadata{CWD: t.TempDir(), Model: "gpt-parent", ModelProvider: "openai"}}
	if err := store.Create(parent); err != nil {
		t.Fatal(err)
	}
	graph := agent.NewMemoryStore()
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), SpawnGraph: graph})
	controller := newRuntimeAgentController(router, "parent", parent.Metadata.CWD, 1)
	modelID := "gpt-review"
	result, err := controller.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{Model: &modelID, ResolvedRole: "reviewer", NicknameCandidates: []string{"Sage"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.AgentID == "" || result.Nickname == nil || *result.Nickname != "Sage" {
		t.Fatalf("result = %+v", result)
	}
	record, err := store.Read(session.ThreadID(result.AgentID), false, true)
	if err != nil {
		t.Fatal(err)
	}
	if record.ParentThreadID != "parent" || record.Metadata.AgentRole != "reviewer" || record.Metadata.AgentNickname != "Sage" || record.Metadata.Model != "gpt-review" || record.Metadata.ModelProvider != "openai" || record.Metadata.ThreadSource != "subAgentThreadSpawn" {
		t.Fatalf("record = %+v", record)
	}
	children, err := graph.ListThreadSpawnChildren("parent", nil)
	if err != nil || len(children) != 1 || children[0] != result.AgentID {
		t.Fatalf("children = %#v, err=%v", children, err)
	}
	_, err = controller.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{ResolvedRole: "reviewer", NicknameCandidates: []string{"Scout"}})
	if !errors.Is(err, agent.ErrAgentLimitReached) {
		t.Fatalf("second spawn error = %v", err)
	}
	waited, err := controller.WaitAgent(context.Background(), &agent.WaitAgentArgs{Targets: []string{result.AgentID}})
	if err != nil || waited.Status[result.AgentID].Kind != agent.AgentMessageStatusPendingInit {
		t.Fatalf("waited = %+v, err=%v", waited, err)
	}
	closed, err := controller.CloseAgent(context.Background(), &agent.CloseAgentArgs{Target: result.AgentID})
	if err != nil || closed.PreviousStatus.Kind != agent.AgentMessageStatusPendingInit {
		t.Fatalf("closed = %+v, err=%v", closed, err)
	}
	closedFilter := agent.ThreadSpawnEdgeClosed
	closedChildren, err := graph.ListThreadSpawnChildren("parent", &closedFilter)
	if err != nil || len(closedChildren) != 1 || closedChildren[0] != result.AgentID {
		t.Fatalf("closed children = %#v, err=%v", closedChildren, err)
	}
	resumed, err := controller.ResumeAgent(context.Background(), &agent.ResumeAgentArgs{ID: result.AgentID})
	if err != nil || resumed.Status.Kind != agent.AgentMessageStatusPendingInit {
		t.Fatalf("resumed = %+v, err=%v", resumed, err)
	}
	metadata, ok := router.agentRegistry.MetadataForThread(result.AgentID)
	if !ok || metadata.Role != "reviewer" || metadata.Nickname != "Sage" {
		t.Fatalf("restored metadata = %+v, ok=%v", metadata, ok)
	}
	if _, err := controller.CloseAgent(context.Background(), &agent.CloseAgentArgs{Target: result.AgentID}); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{ResolvedRole: "reviewer", NicknameCandidates: []string{"Scout"}}); err != nil {
		t.Fatalf("spawn after close error = %v", err)
	}
}

func TestRuntimeAgentControllerReportsNotFound(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	controller := newRuntimeAgentController(router, "parent", t.TempDir(), 1)
	resumed, err := controller.ResumeAgent(context.Background(), &agent.ResumeAgentArgs{ID: "missing"})
	if err != nil || resumed.Status.Kind != agent.AgentMessageStatusNotFound {
		t.Fatalf("resumed = %+v, err=%v", resumed, err)
	}
	closed, err := controller.CloseAgent(context.Background(), &agent.CloseAgentArgs{Target: "missing"})
	if err != nil || closed.PreviousStatus.Kind != agent.AgentMessageStatusNotFound {
		t.Fatalf("closed = %+v, err=%v", closed, err)
	}
}

func TestRuntimeAgentControllerAppliesV2SubagentDeveloperInstructions(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	parent := &session.Record{ID: "parent", CreatedAt: now, UpdatedAt: now, RecencyAt: now, Metadata: session.Metadata{CWD: t.TempDir(), Instructions: "parent instructions"}}
	if err := store.Create(parent); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	controller := newRuntimeAgentControllerWithVersion(router, "parent", parent.Metadata.CWD, 2, agent.VersionV2)
	empty := ""
	result, err := controller.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{DeveloperInstructions: &empty})
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.Load(session.ThreadID(result.AgentID))
	if err != nil {
		t.Fatal(err)
	}
	if record.Metadata.Instructions != "" || record.Metadata.MultiAgentVersion != string(agent.VersionV2) {
		t.Fatalf("child metadata = %#v", record.Metadata)
	}

	inherited, err := controller.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{})
	if err != nil {
		t.Fatal(err)
	}
	inheritedRecord, err := store.Load(session.ThreadID(inherited.AgentID))
	if err != nil || inheritedRecord.Metadata.Instructions != "parent instructions" {
		t.Fatalf("inherited child = %#v, %v", inheritedRecord, err)
	}
}
