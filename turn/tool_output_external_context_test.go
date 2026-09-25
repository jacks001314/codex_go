package turn

import (
	"context"
	"testing"

	"codex_go/model"
	"codex_go/tool"
)

// Rust parity: codex-rs/core/src/tools/registry.rs handle_any_tool, which marks
// the thread's memory mode polluted when a successful tool output declares
// external context and memories are disabled on it. The responder-error and
// fatal paths return before the check.
func TestToolDispatcherReportsExternalContextOutputsLikeRust(t *testing.T) {
	tests := []struct {
		name         string
		output       *tool.Output
		handlerErr   error
		wantCallback bool
		wantError    bool
	}{
		{
			name:         "tool output with the marker",
			output:       &tool.Output{Success: true, Body: "shared discussion", ContainsExternalContext: true},
			wantCallback: true,
		},
		{
			name:   "plain tool output",
			output: &tool.Output{Success: true, Body: "ok"},
		},
		{
			name:         "non-fatal failure output keeps the marker",
			output:       &tool.Output{Success: false, Body: "post text exceeds the board limits", ContainsExternalContext: true},
			wantCallback: true,
		},
		{
			name:       "responder error",
			handlerErr: tool.RespondToModel("no"),
		},
		{
			name:       "fatal error",
			handlerErr: tool.Fatal("boom"),
			wantError:  true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry := tool.NewRegistry()
			output := test.output
			handlerErr := test.handlerErr
			if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("board-tool")}, func(context.Context, *tool.Invocation) (*tool.Output, error) {
				return output, handlerErr
			})); err != nil {
				t.Fatalf("register board-tool: %v", err)
			}
			calls := 0
			dispatcher := NewToolDispatcher(&ToolDispatcherOptions{
				Router:   tool.NewRouter(registry),
				ThreadID: "thread-1",
				OnToolOutputExternalContext: func(context.Context, *tool.Invocation, *tool.Output) {
					calls++
				},
			})
			_, err := dispatcher.ExecuteToolItems(context.Background(), []model.AgentItem{{
				Type: "function_call", CallID: "call-1", Name: "board-tool", Arguments: `{}`,
			}})
			if got := err != nil; got != test.wantError {
				t.Fatalf("ExecuteToolItems() error = %v, wantError %v", err, test.wantError)
			}
			if got := calls > 0; got != test.wantCallback {
				t.Fatalf("callback fired = %v, want %v", got, test.wantCallback)
			}
		})
	}
}

// The runtime binds the turn's thread ID to the host handler so the host never
// has to read it back out of the invocation context.
func TestRuntimeBindsThreadIDToExternalContextHandler(t *testing.T) {
	type report struct {
		threadID string
		body     string
	}
	reports := []report{}
	runtime := NewRuntime(&RuntimeOptions{
		OnToolOutputExternalContext: func(ctx context.Context, threadID string, invocation *tool.Invocation) {
			_ = ctx
			_ = invocation
			reports = append(reports, report{threadID: threadID})
		},
	})
	handler := runtime.toolOutputExternalContextHandler("  thread-1  ")
	if handler == nil {
		t.Fatal("handler is nil")
	}
	handler(context.Background(), &tool.Invocation{CallID: "call-1"}, &tool.Output{ContainsExternalContext: true})
	if len(reports) != 1 || reports[0].threadID != "thread-1" {
		t.Fatalf("reports = %#v", reports)
	}
	if other := NewRuntime(&RuntimeOptions{}).toolOutputExternalContextHandler("thread-1"); other != nil {
		t.Fatal("handler without a host callback is not nil")
	}
}
