package turn

import (
	"context"
	"reflect"
	"testing"

	"codex_go/retainedctx"
	"codex_go/tool"
)

// Mirrors Rust #47899: every sampling request carries the thread's cumulative
// MCP attribution snapshot, and the recorder is independent of the
// executed-tool-call metadata feature.
func TestRuntimeReportsMcpAttributionOnEveryRequestLikeRust(t *testing.T) {
	recorder := NewExecutedToolCallRecorder()
	source := retainedctx.McpAttributionSource{ServerName: "example", ToolName: "search", FirstTurnID: "turn_1"}
	recorder.RecordMcpSource(source)
	want := retainedctx.McpAttribution{
		Status:  retainedctx.McpAttributionStatusComplete,
		Sources: []retainedctx.McpAttributionSource{source},
	}

	agent := &fakeLoopAgent{}
	registry := tool.NewRegistry()
	runtime := NewRuntime(&RuntimeOptions{
		Agent:             agent,
		Router:            tool.NewRouter(registry),
		ExecutedToolCalls: recorder,
	})
	if _, err := runtime.Run(context.Background(), &AgentLoopRequest{Prompt: "run", ExecutedToolCallMetadataEnabled: false}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(agent.requests) == 0 {
		t.Fatal("agent saw no request")
	}
	for index, request := range agent.requests {
		if request.MCPAttribution == nil || !reflect.DeepEqual(*request.MCPAttribution, want) {
			t.Fatalf("request %d MCPAttribution = %#v, want %#v", index, request.MCPAttribution, want)
		}
	}
}

// A loop constructed without a separate attribution recorder reports nothing,
// so the model layer omits the member instead of inventing an empty status.
func TestAgentLoopWithoutMcpAttributionRecorderOmitsSnapshot(t *testing.T) {
	agent := &followUpLoopAgent{}
	loop := NewAgentLoop(&AgentLoopOptions{Agent: agent, MaxTurns: 1})
	if _, err := loop.Run(context.Background(), &AgentLoopRequest{Prompt: "run"}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(agent.requests) == 0 || agent.requests[0].MCPAttribution != nil {
		t.Fatalf("requests = %#v", agent.requests)
	}
}
