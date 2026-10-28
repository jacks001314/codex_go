package turn

import (
	"context"
	"testing"

	"codex_go/model"
	"codex_go/tool"
)

type recordingDynamicToolCaller struct {
	calls []DynamicToolCallParams
}

func (c *recordingDynamicToolCaller) Request(_ context.Context, method string, params any, target any) error {
	if call, ok := params.(*DynamicToolCallParams); ok {
		c.calls = append(c.calls, *call)
	}
	if response, ok := target.(*DynamicToolCallResponse); ok {
		*response = DynamicToolCallResponse{
			Success:      true,
			ContentItems: []DynamicToolCallOutputContentItem{{Type: "inputText", Text: "ok"}},
		}
	}
	return nil
}

func dynamicToolSpecForTest(name string) DynamicToolSpec {
	return DynamicToolSpec{
		Type: "function",
		Function: &DynamicToolFunctionSpec{
			Name:        name,
			Description: "Check " + name + ".",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		},
	}
}

func dynamicToolNamesForTest(specs []tool.Spec) map[string]bool {
	names := map[string]bool{}
	for _, spec := range specs {
		names[spec.Name.Key()] = true
	}
	return names
}

func dynamicToolKeyForTest(name string) string {
	value := tool.PlainName(name)
	return value.Key()
}

func TestRuntimeReplaceDynamicToolsUpdatesCatalogLikeRust(t *testing.T) {
	caller := &recordingDynamicToolCaller{}
	options := DefaultToolRegistryOptions(t.TempDir())
	options.CodexVersion = "test"
	options.DynamicToolCaller = caller
	options.DynamicTools = []DynamicToolSpec{dynamicToolSpecForTest("release")}
	options.ThreadID = "thread-1"
	options.TurnID = "turn-1"
	router, err := BuildToolRouter(options)
	if err != nil {
		t.Fatalf("BuildToolRouter() error = %v", err)
	}
	runtime := NewRuntime(&RuntimeOptions{
		Router:                 router,
		DynamicToolCaller:      caller,
		DynamicToolThreadID:    "thread-1",
		DynamicToolTurnID:      "turn-1",
		DynamicTools:           options.DynamicTools,
		DynamicToolStepRefresh: true,
	})
	if !dynamicToolNamesForTest(router.ModelVisibleSpecs())[dynamicToolKeyForTest("release")] {
		t.Fatalf("initial specs = %#v", router.ModelVisibleSpecs())
	}

	if err := runtime.ReplaceDynamicTools([]DynamicToolSpec{dynamicToolSpecForTest("deployment")}); err != nil {
		t.Fatalf("ReplaceDynamicTools() error = %v", err)
	}
	visible := dynamicToolNamesForTest(router.ModelVisibleSpecs())
	if visible[dynamicToolKeyForTest("release")] || !visible[dynamicToolKeyForTest("deployment")] {
		t.Fatalf("visible specs after replacement = %#v", router.ModelVisibleSpecs())
	}
	// A call emitted before the replacement still dispatches with the old
	// executor, exactly like Rust's preserved in-flight calls.
	if _, ok := router.Executor(tool.PlainName("release")); !ok {
		t.Fatal("removed dynamic tool lost its executor")
	}
	if _, err := router.Dispatch(context.Background(), &tool.Invocation{
		ToolName: tool.PlainName("release"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{}`},
		CallID:   "call-release",
	}); err != nil {
		t.Fatalf("Dispatch(release) error = %v", err)
	}
	if len(caller.calls) != 1 || caller.calls[0].Tool != "release" {
		t.Fatalf("caller calls = %#v", caller.calls)
	}

	if err := runtime.ReplaceDynamicTools(nil); err != nil {
		t.Fatalf("ReplaceDynamicTools(clear) error = %v", err)
	}
	if names := dynamicToolNamesForTest(router.ModelVisibleSpecs()); names[dynamicToolKeyForTest("deployment")] || names[dynamicToolKeyForTest("release")] {
		t.Fatalf("visible specs after clear = %#v", router.ModelVisibleSpecs())
	}
}

func TestRuntimeReplaceDynamicToolsRejectsInvalidCatalogLikeRust(t *testing.T) {
	router, err := BuildToolRouter(DefaultToolRegistryOptions(t.TempDir()))
	if err != nil {
		t.Fatalf("BuildToolRouter() error = %v", err)
	}
	runtime := NewRuntime(&RuntimeOptions{Router: router})
	if err := runtime.ReplaceDynamicTools([]DynamicToolSpec{{Type: "function", Function: &DynamicToolFunctionSpec{Name: ""}}}); err == nil {
		t.Fatal("ReplaceDynamicTools() accepted an empty tool name")
	}
}

type replacingDynamicToolsAgent struct {
	runtime     *Runtime
	replacement []DynamicToolSpec
	requests    []model.AgentRequest
}

func (a *replacingDynamicToolsAgent) Run(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	a.requests = append(a.requests, *request)
	if len(a.requests) == 1 {
		// Replace the catalog while a call from this step is still pending.
		if err := a.runtime.ReplaceDynamicTools(a.replacement); err != nil {
			return nil, err
		}
		return &model.AgentResponse{
			ResponseID: "resp-release",
			Items:      []model.AgentItem{{Type: "function_call", CallID: "call-release", Name: "release", Arguments: `{}`}},
		}, nil
	}
	return &model.AgentResponse{
		ResponseID: "resp-done",
		Message:    "done",
		Items:      []model.AgentItem{{Type: "agent_message", Text: "done"}},
	}, nil
}

func TestRuntimeReplaceDynamicToolsAppliesToNextStepLikeRust(t *testing.T) {
	caller := &recordingDynamicToolCaller{}
	options := DefaultToolRegistryOptions(t.TempDir())
	options.CodexVersion = "test"
	options.DynamicToolCaller = caller
	options.DynamicTools = []DynamicToolSpec{dynamicToolSpecForTest("release")}
	options.ThreadID = "thread-1"
	options.TurnID = "turn-1"
	router, err := BuildToolRouter(options)
	if err != nil {
		t.Fatalf("BuildToolRouter() error = %v", err)
	}
	agent := &replacingDynamicToolsAgent{replacement: []DynamicToolSpec{dynamicToolSpecForTest("deployment")}}
	runtime := NewRuntime(&RuntimeOptions{
		Agent:                  agent,
		Router:                 router,
		MaxTurns:               4,
		DynamicToolCaller:      caller,
		DynamicToolThreadID:    "thread-1",
		DynamicToolTurnID:      "turn-1",
		DynamicTools:           options.DynamicTools,
		DynamicToolStepRefresh: true,
	})
	agent.runtime = runtime
	if _, err := runtime.Run(context.Background(), &AgentLoopRequest{
		Prompt:   "check the release",
		Model:    "gpt-test",
		ThreadID: "thread-1",
		TurnID:   "turn-1",
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(agent.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(agent.requests))
	}
	first := dynamicToolNamesForTestSpecsForRequest(t, agent.requests[0].Tools)
	second := dynamicToolNamesForTestSpecsForRequest(t, agent.requests[1].Tools)
	if !first["release"] || first["deployment"] {
		t.Fatalf("first step tools = %#v", first)
	}
	if second["release"] || !second["deployment"] {
		t.Fatalf("second step tools = %#v", second)
	}
	// The pending call still reached the host after the replacement.
	if len(caller.calls) != 1 || caller.calls[0].Tool != "release" {
		t.Fatalf("caller calls = %#v", caller.calls)
	}
}

func dynamicToolNamesForTestSpecsForRequest(t *testing.T, tools []any) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	for _, raw := range tools {
		value, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name, _ := value["name"].(string)
		if name == "release" || name == "deployment" {
			names[name] = true
		}
	}
	return names
}
