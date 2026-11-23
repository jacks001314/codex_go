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

// Mirrors Rust #63381: legacy models that summarize on the final channel omit
// the final-answer schema from remote V2 compaction, Bedrock's `openai.` prefix
// is recognized, GPT-5.2-Codex keeps it, and the rule follows the model that
// handles each attempt.
func TestLegacyCompactionModelsOmitOutputSchemaLikeRust(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{"passed": map[string]any{"type": "boolean"}}}
	for _, testCase := range []struct {
		model  string
		legacy bool
	}{
		{"gpt-5", true},
		{"gpt-5-codex", true},
		{"gpt-5.1", true},
		{"gpt-5.1-codex-max", true},
		{"gpt-5.2", true},
		{"gpt-5.2-pro", true},
		{"gpt-5.2-codex", false},
		{"gpt-5.2-codex-max", false},
		{"gpt-5.4-nano", true},
		{"gpt-5.4-nano-preview", true},
		{"gpt-5.4", false},
		{"gpt-5.3", false},
		{"future-model", false},
		{"openai.gpt-5.1", true},
		{"openai.gpt-5.2-codex", false},
	} {
		if got := legacyCompactionModel(testCase.model); got != testCase.legacy {
			t.Fatalf("legacyCompactionModel(%q) = %v, want %v", testCase.model, got, testCase.legacy)
		}
		if got := compactionOutputSchemaForModel(testCase.model, schema); (got == nil) != testCase.legacy {
			t.Fatalf("compactionOutputSchemaForModel(%q) = %#v, want legacy=%v", testCase.model, got, testCase.legacy)
		}
	}

	strict := true
	for _, testCase := range []struct {
		model      string
		wantSchema bool
	}{
		{"gpt-5.2", false},
		{"gpt-5.2-codex", true},
		{"gpt-5.4", true},
		{"openai.gpt-5", false},
	} {
		agent := &compactSchemaAgent{}
		runner := &agentCompactRunner{agent: agent, model: testCase.model, outputSchema: schema, outputSchemaStrict: &strict}
		if _, err := runner.Compact(context.Background(), &compact.Request{
			ThreadID: "thread-" + testCase.model,
			TurnID:   "turn-1",
			Trigger:  compact.TriggerAuto,
			Reason:   compact.ReasonContextWindowExceeded,
			Phase:    compact.PhaseMidTurn,
		}); err != nil {
			t.Fatalf("%s: Compact() error = %v", testCase.model, err)
		}
		if len(agent.requests) != 1 {
			t.Fatalf("%s: agent requests = %d, want 1", testCase.model, len(agent.requests))
		}
		request := agent.requests[0]
		if (request.OutputSchema != nil) != testCase.wantSchema {
			t.Fatalf("%s: OutputSchema = %#v, want present=%v", testCase.model, request.OutputSchema, testCase.wantSchema)
		}
		if request.OutputSchemaStrict == nil || !*request.OutputSchemaStrict {
			t.Fatalf("%s: OutputSchemaStrict = %#v, want strict", testCase.model, request.OutputSchemaStrict)
		}
	}
}
