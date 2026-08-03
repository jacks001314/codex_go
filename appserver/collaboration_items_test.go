package appserver

import (
	"context"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/tool"
	"codex_go/turn"
)

func TestRuntimeCollaborationV2ActivityEmitsStartedAndCompletedLifecycle(t *testing.T) {
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{})
	defer router.Close()
	router.SetNotificationSink(sink)
	now := time.Now().UTC()
	execution := &turn.ToolExecutionResult{
		Invocation: &tool.Invocation{
			CallID: "spawn-activity", ToolName: tool.NamespacedName(agent.MultiAgentV2Namespace, "spawn_agent"),
		},
		Output: &tool.Output{Success: true, Data: map[string]any{
			"subAgentActivity": map[string]any{"kind": "started", "agent_thread_id": "child-thread", "agent_path": "/root/worker"},
		}},
		StartedAt: now, FinishedAt: now.Add(time.Second),
	}

	router.runtimeToolCompletedNotifier("root-thread", "turn-1", t.TempDir(), false)(context.Background(), execution)
	notifications := sink.List()
	if len(notifications) != 2 || notifications[0].Method != NotificationItemStarted || notifications[1].Method != NotificationItemCompleted {
		t.Fatalf("activity lifecycle notifications = %#v", notifications)
	}
	started, ok := notifications[0].Params.(*ItemStartedNotification)
	if !ok {
		t.Fatalf("started notification params = %T", notifications[0].Params)
	}
	completed, ok := notifications[1].Params.(*ItemCompletedNotification)
	if !ok {
		t.Fatalf("completed notification params = %T", notifications[1].Params)
	}
	for lifecycle, item := range map[string]ThreadItemPayload{"started": started.Item, "completed": completed.Item} {
		if item["id"] != "spawn-activity" || item["type"] != "subAgentActivity" || item["kind"] != "started" ||
			item["agentThreadId"] != "child-thread" || item["agentPath"] != "/root/worker" {
			t.Fatalf("%s activity payload = %#v", lifecycle, item)
		}
	}
}

func TestSessionItemsForCollaborationHideRawPairAndPersistCanonicalActivity(t *testing.T) {
	now := time.Now().UTC()
	execution := &turn.ToolExecutionResult{
		Invocation: &tool.Invocation{
			CallID: "spawn-1", ToolName: tool.NamespacedName("collaboration", "spawn_agent"),
			Payload: tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"task_name":"worker","message":"gAAAA-secret"}`},
			Context: map[string]any{"thread_id": "root-thread"},
		},
		Output: &tool.Output{Success: true, Data: map[string]any{
			"result":           map[string]any{"task_name": "/root/worker"},
			"subAgentActivity": map[string]any{"kind": "started", "agent_thread_id": "child-thread", "agent_path": "/root/worker"},
		}},
		StartedAt: now, FinishedAt: now.Add(time.Second),
	}
	items := sessionItemsForAppToolExecution("turn-1", execution, now)
	if len(items) != 3 {
		t.Fatalf("items = %#v", items)
	}
	if !sessionItemIsHiddenThreadItem(&items[0]) || !sessionItemIsHiddenThreadItem(&items[1]) {
		t.Fatalf("raw collaboration pair is visible: %#v", items)
	}
	if items[2].Type != "subAgentActivity" || items[2].Data["agentPath"] != "/root/worker" {
		t.Fatalf("canonical presentation = %#v", items[2])
	}
	if items[2].Metadata["lifecycleNotified"] != true || shouldNotifyRuntimeItemCompleted(BuildThreadItem(items[2])) {
		t.Fatalf("canonical presentation should not emit a duplicate lifecycle notification: %#v", items[2])
	}
}

func TestAppCollaborationPresentationNormalizesTypedV1Results(t *testing.T) {
	now := time.Now().UTC()
	spawn := appCollabAgentThreadItem(&tool.Invocation{
		CallID: "spawn-v1", ToolName: tool.NamespacedName("agent", "spawn_agent"),
		Payload: tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"prompt":"inspect"}`},
	}, &tool.Output{Success: true, Data: map[string]any{
		"result": &agent.SpawnAgentResult{AgentID: "child-thread"},
	}}, "root-thread", "turn-v1", string(CollabAgentToolCallCompleted), CollabAgentToolSpawnAgent, now)
	receivers, ok := spawn.Data["receiverThreadIds"].([]string)
	if !ok || len(receivers) != 1 || receivers[0] != "child-thread" {
		t.Fatalf("typed spawn receiverThreadIds = %#v", spawn.Data["receiverThreadIds"])
	}

	wait := appCollabAgentThreadItem(&tool.Invocation{
		CallID: "wait-v1", ToolName: tool.NamespacedName("agent", "wait_agent"),
		Payload: tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"targets":["child-thread"]}`},
	}, &tool.Output{Success: true, Data: map[string]any{
		"result": &agent.WaitAgentResult{Status: map[string]agent.AgentMessageStatus{
			"child-thread": {Kind: agent.AgentMessageStatusCompleted, Message: "done"},
		}},
	}}, "root-thread", "turn-v1", string(CollabAgentToolCallCompleted), CollabAgentToolWait, now)
	states, ok := wait.Data["agentsStates"].(map[string]any)
	if !ok {
		t.Fatalf("typed wait agentsStates = %#v", wait.Data["agentsStates"])
	}
	state, ok := states["child-thread"].(map[string]any)
	if !ok || state["status"] != string(CollabAgentStatusCompleted) || state["message"] != "done" {
		t.Fatalf("typed wait child state = %#v", states["child-thread"])
	}
}
