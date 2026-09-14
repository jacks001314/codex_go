package appserver

import (
	"context"
	"testing"

	"codex_go/codexapi"
	"codex_go/compact"
	"codex_go/model"
)

type compactReasoningEffortAgent struct {
	requests []model.AgentRequest
}

func (a *compactReasoningEffortAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	a.requests = append(a.requests, *request)
	return &model.AgentResponse{ResponseID: "resp-compact", Message: "summary"}, nil
}

// TestAgentCompactRunnerSendsPinnedReasoningEffort mirrors Rust #43796: remote
// compaction carries the pinned request effort for the model.
func TestAgentCompactRunnerSendsPinnedReasoningEffort(t *testing.T) {
	agent := &compactReasoningEffortAgent{}
	runner := &agentCompactRunner{agent: agent, model: "gpt-5", effort: "high"}
	result, err := runner.Compact(context.Background(), &compact.Request{
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		Trigger:  compact.TriggerManual,
		Reason:   compact.ReasonUserRequested,
		Phase:    compact.PhaseStandaloneTurn,
	})
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if result == nil || result.Summary != "summary" {
		t.Fatalf("result = %#v", result)
	}
	if len(agent.requests) != 1 {
		t.Fatalf("agent requests = %#v", agent.requests)
	}
	if agent.requests[0].ReasoningEffort != "high" {
		t.Fatalf("compaction reasoning effort = %q, want pinned high", agent.requests[0].ReasoningEffort)
	}
	// Rust's compaction request kind serializes as "compaction" and the request
	// carries the compacted thread and turn.
	metadata := agent.requests[0].ClientMetadata
	if metadata[codexapi.RequestKindKey] != string(codexapi.ClientRequestCompaction) {
		t.Fatalf("compaction request kind = %q", metadata[codexapi.RequestKindKey])
	}
	if metadata[codexapi.ThreadIDKey] != "thread-1" || metadata[codexapi.TurnIDKey] != "turn-1" {
		t.Fatalf("compaction metadata = %#v", metadata)
	}
}
