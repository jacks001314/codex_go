package appserver

import (
	"context"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/session"
	"codex_go/tool"
	"codex_go/turn"
)

// Rust #47252 closes an agent that is known to the persisted agent graph even
// when its runtime is no longer loaded, and still records the closed spawn edge.
func TestRuntimeAgentControllerClosesKnownUnloadedAgentLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	parent := &session.Record{ID: "parent", SessionID: "parent", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: t.TempDir(), Model: "gpt-parent", ModelProvider: "openai"}}
	if err := store.Create(parent); err != nil {
		t.Fatal(err)
	}
	graph := agent.NewMemoryStore()
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), SpawnGraph: graph})
	controller := newRuntimeAgentController(router, "parent", parent.Metadata.CWD, 1).(*runtimeAgentController)
	spawned, err := controller.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{ResolvedRole: "reviewer"})
	if err != nil {
		t.Fatal(err)
	}

	// Drop the live registration so the target only exists in the thread store.
	controller.registry.ReleaseSpawnedThread(spawned.AgentID)
	if _, ok := controller.registry.MetadataForThread(spawned.AgentID); ok {
		t.Fatal("the target is still registered as a live agent")
	}

	closed, err := controller.CloseAgent(context.Background(), &agent.CloseAgentArgs{Target: spawned.AgentID})
	if err != nil {
		t.Fatalf("closing a known unloaded agent failed: %v", err)
	}
	if closed.PreviousStatus.Kind == agent.AgentMessageStatusNotFound {
		t.Fatalf("previous status = %+v, want the persisted agent status", closed.PreviousStatus)
	}
	edge := agent.ThreadSpawnEdgeClosed
	children, err := graph.ListThreadSpawnChildren("parent", &edge)
	if err != nil || len(children) != 1 || children[0] != spawned.AgentID {
		t.Fatalf("closed children = %#v, err=%v", children, err)
	}
}

// Rust #47252 fills the close_agent completion item from the pre-close
// snapshot and marks the item failed when that snapshot was NotFound.
func TestCloseAgentCompletionItemUsesSnapshotLikeRust(t *testing.T) {
	now := time.Now().UTC()
	execution := func(previous agent.AgentMessageStatus) *turn.ToolExecutionResult {
		return &turn.ToolExecutionResult{
			Invocation: &tool.Invocation{
				CallID: "close-1", ToolName: tool.NamespacedName(agent.MultiAgentV1Namespace, "close_agent"),
				Payload: tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"target":"child-thread"}`},
			},
			Output: &tool.Output{Success: true, Data: map[string]any{
				"result": &agent.CloseAgentResult{PreviousStatus: previous},
			}},
			StartedAt: now, FinishedAt: now.Add(time.Second),
		}
	}

	item, ok := collaborationCompletedThreadItem(execution(agent.AgentMessageStatus{Kind: agent.AgentMessageStatusRunning}), "root-thread", "turn-1")
	if !ok || item.Data["status"] != string(CollabAgentToolCallCompleted) {
		t.Fatalf("close item = %#v, ok=%t", item, ok)
	}
	states, _ := item.Data["agentsStates"].(map[string]any)
	state, _ := states["child-thread"].(map[string]any)
	if state["status"] != string(CollabAgentStatusRunning) {
		t.Fatalf("close agentsStates = %#v", item.Data["agentsStates"])
	}

	missing, ok := collaborationCompletedThreadItem(execution(agent.AgentMessageStatus{Kind: agent.AgentMessageStatusNotFound}), "root-thread", "turn-2")
	if !ok || missing.Data["status"] != string(CollabAgentToolCallFailed) {
		t.Fatalf("missing close item = %#v, ok=%t", missing, ok)
	}
	missingStates, _ := missing.Data["agentsStates"].(map[string]any)
	missingState, _ := missingStates["child-thread"].(map[string]any)
	if missingState["status"] != string(CollabAgentStatusNotFound) {
		t.Fatalf("missing close agentsStates = %#v", missing.Data["agentsStates"])
	}
}
