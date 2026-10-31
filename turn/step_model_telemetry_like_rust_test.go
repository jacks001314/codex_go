package turn

import (
	"context"
	"testing"

	"codex_go/model"
	"codex_go/tool"
)

// Rust #44242 attributes a tool call's telemetry to the model captured by the
// step that issued it. The dispatcher stamps that model into the execution's
// telemetry tags, which the tool-call and MCP metrics then carry.
func TestToolDispatcherTagsStepModelOnTelemetryLikeRust(t *testing.T) {
	registry := tool.NewRegistry()
	executor := &dispatcherTelemetryTagExecutor{ExecutorFunc: tool.NewExecutorFunc(
		tool.Spec{Name: tool.PlainName("echo")},
		func(context.Context, *tool.Invocation) (*tool.Output, error) {
			return &tool.Output{Success: true, Body: "ok"}, nil
		},
	)}
	if err := registry.Register(executor); err != nil {
		t.Fatal(err)
	}
	dispatcher := NewToolDispatcher(&ToolDispatcherOptions{Router: tool.NewRouter(registry)})
	dispatcher.SetStepSettings("gpt-step-2", "", "")
	results, err := dispatcher.ExecuteToolItems(context.Background(), []model.AgentItem{{
		Type:      "function_call",
		Name:      "echo",
		CallID:    "call-step-model",
		Arguments: `{}`,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %#v", results)
	}
	if got := results[0].TelemetryTags["model"]; got != "gpt-step-2" {
		t.Fatalf("model tag = %q, want gpt-step-2 (%#v)", got, results[0].TelemetryTags)
	}
	if got := results[0].TelemetryTags["mcp_server"]; got != "calendar" {
		t.Fatalf("executor tags were dropped: %#v", results[0].TelemetryTags)
	}

	// A later step replaces the attribution instead of accumulating models.
	dispatcher.SetStepSettings("gpt-step-3", "", "")
	results, err = dispatcher.ExecuteToolItems(context.Background(), []model.AgentItem{{
		Type:      "function_call",
		Name:      "echo",
		CallID:    "call-step-model-2",
		Arguments: `{}`,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := results[0].TelemetryTags["model"]; got != "gpt-step-3" {
		t.Fatalf("model tag after model change = %q, want gpt-step-3", got)
	}
}
