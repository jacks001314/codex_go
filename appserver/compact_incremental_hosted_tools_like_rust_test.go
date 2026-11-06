package appserver

import (
	"context"
	"testing"

	"codex_go/compact"
)

// TestCompactRunnerKeepsHostedWebSearchTopLevelLikeRust covers Rust #59832:
// a compaction prompt that replays recorded tool declarations keeps hosted web
// search in the top-level tools and marks the request as incremental.
func TestCompactRunnerKeepsHostedWebSearchTopLevelLikeRust(t *testing.T) {
	agent := &compactReasoningEffortAgent{}
	runner := &agentCompactRunner{
		agent:            agent,
		model:            "gpt-5",
		incrementalTools: true,
		hostedTools:      []any{map[string]any{"type": "web_search"}},
	}
	_, err := runner.Compact(context.Background(), &compact.Request{
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		Trigger:  compact.TriggerManual,
		Reason:   compact.ReasonUserRequested,
		Phase:    compact.PhaseStandaloneTurn,
		History: []compact.Item{{
			ID:   "catalog",
			Type: "additional_tools",
			Raw:  []byte(`{"type":"additional_tools","role":"developer","tools":[{"type":"function","name":"echo"}]}`),
		}},
	})
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if len(agent.requests) != 1 {
		t.Fatalf("agent requests = %d, want 1", len(agent.requests))
	}
	request := agent.requests[0]
	if !request.IncrementalTools {
		t.Fatal("compaction request did not keep the incremental layout")
	}
	if len(request.Tools) != 1 {
		t.Fatalf("compaction tools = %#v, want only hosted web_search", request.Tools)
	}
	tool, _ := request.Tools[0].(map[string]any)
	if tool == nil || tool["type"] != "web_search" {
		t.Fatalf("compaction tool = %#v, want web_search", request.Tools[0])
	}
}
