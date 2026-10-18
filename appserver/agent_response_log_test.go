package appserver

import (
	"testing"

	"codex_go/model"
)

func TestAgentResponseAttributionMatchesRustSessionSources(t *testing.T) {
	cases := []struct {
		name       string
		source     string
		parent     string
		wantType   string
		wantParent string
		wantOK     bool
	}{
		{name: "main", source: "cli", wantType: "main", wantOK: true},
		{name: "thread spawn", source: "subAgentThreadSpawn", parent: "root", wantType: "subagent", wantParent: "root", wantOK: true},
		{name: "review", source: "subAgentReview", wantOK: false},
		{name: "compact", source: "subAgentCompact", wantOK: false},
		{name: "memory", source: "subAgentMemoryConsolidation", wantOK: false},
		{name: "internal", source: "internal_memory_consolidation", wantOK: false},
		{name: "other subagent", source: "subAgentOther", wantOK: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			gotType, gotParent, gotOK := agentResponseAttribution(&appTurnRunConfig{
				ThreadSource:   testCase.source,
				ParentThreadID: testCase.parent,
			})
			if gotType != testCase.wantType || gotParent != testCase.wantParent || gotOK != testCase.wantOK {
				t.Fatalf("attribution = %q/%q/%v, want %q/%q/%v", gotType, gotParent, gotOK, testCase.wantType, testCase.wantParent, testCase.wantOK)
			}
		})
	}
}

func TestAgentResponseFinalAnswerRequiresMessageAndPhase(t *testing.T) {
	cases := []struct {
		name string
		item model.AgentItem
		want bool
	}{
		{name: "final", item: model.AgentItem{Type: "agent_message", Text: "done", Data: map[string]any{"phase": "final_answer"}}, want: true},
		{name: "commentary", item: model.AgentItem{Type: "agent_message", Text: "working", Data: map[string]any{"phase": "commentary"}}},
		{name: "reasoning", item: model.AgentItem{Type: "reasoning", Text: "thinking", Data: map[string]any{"phase": "final_answer"}}},
		{name: "empty", item: model.AgentItem{Type: "agent_message", Text: " ", Data: map[string]any{"phase": "final_answer"}}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := agentResponseIsFinalAnswer(testCase.item); got != testCase.want {
				t.Fatalf("final answer = %v, want %v", got, testCase.want)
			}
		})
	}
}
