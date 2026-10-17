package turn

import (
	"context"
	"testing"

	"codex_go/model"
	"codex_go/tool"
)

type codedExecutorError struct{ code string }

func (e *codedExecutorError) Error() string            { return "request failed" }
func (e *codedExecutorError) RequestErrorCode() string { return e.code }

// TestToolDispatcherCarriesRequestErrorCodeLikeRust mirrors Rust #54094: the
// dispatcher copies a bounded request-failure category from the executor error
// onto the execution's telemetry tags before the error becomes model text.
func TestToolDispatcherCarriesRequestErrorCodeLikeRust(t *testing.T) {
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("mcp_probe")}, func(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
		return nil, &codedExecutorError{code: "request_timeout"}
	})); err != nil {
		t.Fatalf("register executor: %v", err)
	}
	dispatcher := NewToolDispatcher(&ToolDispatcherOptions{Router: tool.NewRouter(registry)})
	results, err := dispatcher.ExecuteToolItems(context.Background(), []model.AgentItem{{
		ID: "call-1", Type: "function_call", Name: "mcp_probe", Arguments: `{}`,
	}})
	if err != nil {
		t.Fatalf("ExecuteToolItems() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %#v", results)
	}
	if got := results[0].TelemetryTags[tool.RequestErrorCodeTelemetryTag]; got != "request_timeout" {
		t.Fatalf("telemetry code = %q, want request_timeout (%#v)", got, results[0].TelemetryTags)
	}
	if results[0].Response.Output.Text() != "request failed" {
		t.Fatalf("model output = %q", results[0].Response.Output.Text())
	}
}
