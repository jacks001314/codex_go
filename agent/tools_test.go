package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"codex_go/tool"
)

func TestMultiAgentToolsLifecycle(t *testing.T) {
	controller := NewMemoryToolController()
	spawn := NewMultiAgentToolExecutor(MultiAgentToolSpawn, controller)
	output, err := spawn.Execute(context.Background(), &tool.Invocation{
		CallID:   "spawn",
		ToolName: tool.NamespacedName(MultiAgentV1Namespace, string(MultiAgentToolSpawn)),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"message":"research this"}`},
	})
	if err != nil {
		t.Fatalf("spawn Execute() error = %v", err)
	}
	var spawned SpawnAgentResult
	if err := json.Unmarshal([]byte(output.Body), &spawned); err != nil {
		t.Fatalf("Unmarshal spawn = %v", err)
	}
	if spawned.AgentID == "" || spawned.Nickname == nil {
		t.Fatalf("spawned = %#v", spawned)
	}

	send := NewMultiAgentToolExecutor(MultiAgentToolSend, controller)
	if _, err := send.Execute(context.Background(), &tool.Invocation{
		CallID:   "send",
		ToolName: tool.NamespacedName(MultiAgentV1Namespace, string(MultiAgentToolSend)),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"target":"` + spawned.AgentID + `","message":"continue"}`},
	}); err != nil {
		t.Fatalf("send Execute() error = %v", err)
	}

	controller.SetStatus(spawned.AgentID, AgentMessageStatus{Kind: AgentMessageStatusCompleted, Message: "done"})
	wait := NewMultiAgentToolExecutor(MultiAgentToolWait, controller)
	waitOutput, err := wait.Execute(context.Background(), &tool.Invocation{
		CallID:   "wait",
		ToolName: tool.NamespacedName(MultiAgentV1Namespace, string(MultiAgentToolWait)),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"targets":["` + spawned.AgentID + `"]}`},
	})
	if err != nil {
		t.Fatalf("wait Execute() error = %v", err)
	}
	if !strings.Contains(waitOutput.Body, `"completed"`) || !strings.Contains(waitOutput.Body, `"done"`) {
		t.Fatalf("wait output = %q", waitOutput.Body)
	}

	closeTool := NewMultiAgentToolExecutor(MultiAgentToolClose, controller)
	closeOutput, err := closeTool.Execute(context.Background(), &tool.Invocation{
		CallID:   "close",
		ToolName: tool.NamespacedName(MultiAgentV1Namespace, string(MultiAgentToolClose)),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"target":"` + spawned.AgentID + `"}`},
	})
	if err != nil {
		t.Fatalf("close Execute() error = %v", err)
	}
	if !strings.Contains(closeOutput.Body, `"completed"`) {
		t.Fatalf("close output = %q", closeOutput.Body)
	}
}

func TestMultiAgentToolHookPayloads(t *testing.T) {
	executor := NewMultiAgentToolExecutor(MultiAgentToolSpawn, NewMemoryToolController())
	invocation := &tool.Invocation{
		CallID:   "call-spawn",
		ToolName: tool.NamespacedName(MultiAgentV1Namespace, string(MultiAgentToolSpawn)),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"message":"split work"}`},
	}
	pre, ok := executor.PreToolUsePayload(invocation)
	if !ok {
		t.Fatal("PreToolUsePayload() ok = false")
	}
	if pre.ToolName == nil || pre.ToolName.Name != "spawn_agent" || len(pre.ToolName.MatcherAliases) != 1 || pre.ToolName.MatcherAliases[0] != "Agent" {
		t.Fatalf("pre.ToolName = %#v", pre.ToolName)
	}
	if input, ok := pre.ToolInput.(map[string]any); !ok || input["message"] != "split work" {
		t.Fatalf("pre.ToolInput = %#v", pre.ToolInput)
	}

	post, ok := executor.PostToolUsePayload(invocation, &tool.Output{
		CallID: "call-spawn",
		Body:   `{"agent_id":"agent-1"}`,
		Data:   map[string]any{"result": &SpawnAgentResult{AgentID: "agent-1"}},
	})
	if !ok {
		t.Fatal("PostToolUsePayload() ok = false")
	}
	if post.ToolName == nil || post.ToolName.Name != "spawn_agent" || post.ToolUseID != "call-spawn" {
		t.Fatalf("post = %#v", post)
	}
	if _, ok := post.ToolResponse.(*SpawnAgentResult); !ok {
		t.Fatalf("ToolResponse = %#v", post.ToolResponse)
	}
}

func TestMultiAgentToolUpdatedHookInput(t *testing.T) {
	executor := NewMultiAgentToolExecutor(MultiAgentToolSend, NewMemoryToolController())
	updated, err := executor.WithUpdatedHookInput(&tool.Invocation{
		ToolName: tool.NamespacedName(MultiAgentV1Namespace, string(MultiAgentToolSend)),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"target":"agent-1","message":"old"}`},
	}, map[string]any{"target": "agent-1", "message": "new"})
	if err != nil {
		t.Fatalf("WithUpdatedHookInput() error = %v", err)
	}
	if !strings.Contains(updated.Payload.Arguments, `"new"`) {
		t.Fatalf("updated = %#v", updated.Payload)
	}
}

func TestRegisterMultiAgentHandlersDiscoverable(t *testing.T) {
	registry := tool.NewRegistry()
	if err := RegisterMultiAgentHandlers(registry, NewMemoryToolController(), tool.ExposureDiscoverable); err != nil {
		t.Fatalf("RegisterMultiAgentHandlers() error = %v", err)
	}
	specs := registry.NamesAsSpecs()
	if len(specs) != 5 {
		t.Fatalf("specs = %#v", specs)
	}
	for _, spec := range specs {
		if spec.Exposure != tool.ExposureDiscoverable || spec.Search == nil {
			t.Fatalf("spec = %#v", spec)
		}
	}
	if err := tool.RegisterToolSearchFromRegistry(registry); err != nil {
		t.Fatalf("RegisterToolSearchFromRegistry() error = %v", err)
	}
	output, err := tool.NewRouter(registry).Dispatch(context.Background(), &tool.Invocation{
		CallID:   "search",
		ToolName: tool.PlainName(tool.ToolSearchName),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"query":"delegate parallel worker"}`},
	})
	if err != nil {
		t.Fatalf("tool_search Dispatch() error = %v", err)
	}
	if !strings.Contains(output.Body, "spawn_agent") {
		t.Fatalf("tool_search output = %q", output.Body)
	}
}

func TestRegisterMultiAgentHandlersWithRolesDescribesConfiguredTypes(t *testing.T) {
	registry := tool.NewRegistry()
	err := RegisterMultiAgentHandlersWithOptions(registry, &MultiAgentHandlerOptions{
		Controller: &captureToolController{}, Exposure: tool.ExposureModelVisible,
		Roles: map[string]RoleConfig{"reviewer": {Description: "Reviews changes.", Settings: map[string]string{"model_reasoning_effort": "high"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	executor, ok := registry.Lookup(tool.NamespacedName(MultiAgentV1Namespace, string(MultiAgentToolSpawn)))
	if !ok {
		t.Fatal("spawn_agent missing")
	}
	description := executor.Spec().Description
	if !strings.Contains(description, "`reviewer`: Reviews changes.") || !strings.Contains(description, "reasoning effort is set to `high`") || strings.Contains(description, "`explorer`") {
		t.Fatalf("description = %q", description)
	}
}
