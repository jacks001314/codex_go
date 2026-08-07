package appserver

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/session"
	"codex_go/turn"
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

func TestRuntimeAgentControllerV1DepthLimitMatchesRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	parent := &session.Record{ID: "parent", SessionID: "parent", CreatedAt: now, UpdatedAt: now, RecencyAt: now, Metadata: session.Metadata{CWD: t.TempDir()}}
	if err := store.Create(parent); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	root := newRuntimeAgentController(router, "parent", parent.Metadata.CWD, 4).(*runtimeAgentController)
	child, err := root.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{ResolvedRole: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	childRecord, err := store.Read(session.ThreadID(child.AgentID), false, true)
	if err != nil {
		t.Fatal(err)
	}
	if childRecord.Metadata.AgentDepth != 1 {
		t.Fatalf("child agent depth = %d, want 1", childRecord.Metadata.AgentDepth)
	}
	// A depth-1 agent cannot spawn or resume (Rust default max_depth = 1).
	childController := newRuntimeAgentControllerForTurn(router, child.AgentID, "", childRecord.Metadata.CWD, 4, agent.VersionV1, nil).(*runtimeAgentController)
	if _, err := childController.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{ResolvedRole: "nested"}); !errors.Is(err, agent.ErrAgentDepthLimitReached) {
		t.Fatalf("nested spawn error = %v", err)
	}
	if _, err := childController.ResumeAgent(context.Background(), &agent.ResumeAgentArgs{ID: child.AgentID}); !errors.Is(err, agent.ErrAgentDepthLimitReached) {
		t.Fatalf("nested resume error = %v", err)
	}
	// V2 ignores max_depth and relies on concurrency slots.
	v2 := newRuntimeAgentControllerWithVersion(router, child.AgentID, childRecord.Metadata.CWD, 4, agent.VersionV2).(*runtimeAgentController)
	if _, err := v2.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{ResolvedRole: "nested"}); err != nil {
		t.Fatalf("V2 nested spawn should ignore depth limit: %v", err)
	}
}

func TestRuntimeAgentControllerV2InterruptMissingTargetReturnsError(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	controller, ok := newRuntimeAgentControllerWithVersion(router, "parent", t.TempDir(), 1, agent.VersionV2).(agent.V2ToolController)
	if !ok {
		t.Fatal("runtime agent controller does not implement V2ToolController")
	}
	result, err := controller.InterruptAgent(context.Background(), &agent.InterruptAgentArgs{Target: "missing"})
	if err == nil || !strings.Contains(err.Error(), "agent missing not found") {
		t.Fatalf("InterruptAgent() result=%#v error=%v", result, err)
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

func TestRuntimeAgentControllerChildInheritsTurnEnvironmentSelectionsLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	parent := &session.Record{ID: "parent", CreatedAt: now, UpdatedAt: now, RecencyAt: now, Metadata: session.Metadata{CWD: "/primary"}}
	if err := store.Create(parent); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	selections := []map[string]any{
		{"environmentId": "remote-primary", "cwd": "/primary"},
		{"environmentId": "local", "cwd": "/local"},
	}
	controller := newRuntimeAgentControllerWithEnvironmentSelections(router, "parent", parent.Metadata.CWD, 2, agent.VersionV2, selections)
	result, err := controller.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{})
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.Load(session.ThreadID(result.AgentID))
	if err != nil {
		t.Fatal(err)
	}
	persisted := environmentSelectionsFromAny(record.Metadata.Extra[runtimeEnvironmentSelectionsExtraKey])
	if len(persisted) != 2 || persisted[0]["environmentId"] != "remote-primary" || persisted[1]["environmentId"] != "local" {
		t.Fatalf("persisted environments = %#v", persisted)
	}
	params := &turn.TurnStartParams{ThreadID: result.AgentID}
	router.inheritTurnEnvironmentSelections(params)
	if len(params.Environments) != 2 || params.Environments[0]["cwd"] != "/primary" {
		t.Fatalf("inherited environments = %#v", params.Environments)
	}
}

func TestRuntimeAgentControllerAttributesChildTurnsToParentTurn(t *testing.T) {
	controller := newRuntimeAgentControllerForTurn(nil, "parent-thread", "parent-turn", t.TempDir(), 1, agent.VersionV1, nil)
	runtimeController, ok := controller.(*runtimeAgentController)
	if !ok {
		t.Fatalf("controller = %T", controller)
	}
	if runtimeController.parentID != "parent-thread" || runtimeController.parentTurnID != "parent-turn" {
		t.Fatalf("parent provenance = thread %q turn %q", runtimeController.parentID, runtimeController.parentTurnID)
	}
}

func TestRuntimeAgentControllerV2RegistersCanonicalPathAndImplementsTools(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	parent := &session.Record{ID: "parent", SessionID: "parent", CreatedAt: now, UpdatedAt: now, RecencyAt: now, Metadata: session.Metadata{CWD: t.TempDir()}}
	if err := store.Create(parent); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	controller, ok := newRuntimeAgentControllerWithVersion(router, "parent", parent.Metadata.CWD, 2, agent.VersionV2).(agent.V2ToolController)
	if !ok {
		t.Fatal("runtime controller does not implement V2ToolController")
	}
	message := "encrypted payload"
	spawned, err := controller.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{TaskName: "worker", Message: &message})
	if err != nil {
		t.Fatal(err)
	}
	if spawned.TaskName != "/root/worker" {
		t.Fatalf("task name = %q", spawned.TaskName)
	}
	metadata, ok := router.runtimeAgentRegistry("parent").MetadataForThread(spawned.AgentID)
	if !ok || metadata.Path != "/root/worker" {
		t.Fatalf("registry metadata = %#v/%t", metadata, ok)
	}
	listed, err := controller.ListAgents(context.Background(), &agent.ListAgentsArgs{})
	if err != nil || len(listed.Agents) != 2 || listed.Agents[0].AgentName != "/root" || listed.Agents[1].AgentName != "/root/worker" {
		t.Fatalf("listed = %#v, err=%v", listed, err)
	}
}
