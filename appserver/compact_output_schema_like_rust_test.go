package appserver

import (
	"context"
	"testing"

	"codex_go/compact"
	"codex_go/model"
)

type compactSchemaAgent struct {
	requests []model.AgentRequest
}

func (a *compactSchemaAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	a.requests = append(a.requests, *request)
	return &model.AgentResponse{ResponseID: "resp-compact", Message: "summary"}, nil
}

// Mirrors Rust #63320: the remote compaction attempt carries the turn's
// final-answer schema through text.format, non-strict for basic session
// sources (guardian verdict format).
func TestAgentCompactRunnerCarriesOutputSchemaLikeRust(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{"output": map[string]any{"type": "string"}}}
	strict := false
	agent := &compactSchemaAgent{}
	runner := &agentCompactRunner{agent: agent, model: "gpt-test", outputSchema: schema, outputSchemaStrict: &strict}
	if _, err := runner.Compact(context.Background(), &compact.Request{
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		Trigger:  compact.TriggerAuto,
		Reason:   compact.ReasonContextWindowExceeded,
		Phase:    compact.PhaseMidTurn,
	}); err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if len(agent.requests) != 1 {
		t.Fatalf("agent requests = %d, want 1", len(agent.requests))
	}
	request := agent.requests[0]
	if request.OutputSchema == nil {
		t.Fatal("compaction request dropped the turn's output schema")
	}
	if request.OutputSchemaStrict == nil || *request.OutputSchemaStrict {
		t.Fatalf("output schema strict = %#v, want an explicit non-strict flag", request.OutputSchemaStrict)
	}

	// Without a basic session source the attempt keeps the strict default by
	// leaving the flag unset.
	strictDefault := true
	agent = &compactSchemaAgent{}
	runner = &agentCompactRunner{agent: agent, model: "gpt-test", outputSchema: schema, outputSchemaStrict: &strictDefault}
	if _, err := runner.Compact(context.Background(), &compact.Request{
		ThreadID: "thread-2",
		TurnID:   "turn-2",
		Trigger:  compact.TriggerAuto,
		Reason:   compact.ReasonContextWindowExceeded,
		Phase:    compact.PhaseMidTurn,
	}); err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if len(agent.requests) != 1 || agent.requests[0].OutputSchemaStrict == nil || !*agent.requests[0].OutputSchemaStrict {
		t.Fatalf("strict request = %#v", agent.requests)
	}
}
