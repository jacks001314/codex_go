package appserver

import (
	"encoding/json"
	"testing"

	"codex_go/session"
	"codex_go/tool"
	"codex_go/turn"
)

// TestSubAgentActivityCarriesResolvedModelAndEffortLikeRust mirrors Rust #51463:
// a started sub-agent activity reports the child's resolved model and reasoning
// effort, while other activity kinds report null.
func TestSubAgentActivityCarriesResolvedModelAndEffortLikeRust(t *testing.T) {
	started := &turn.ToolExecutionResult{Output: &tool.Output{Data: map[string]any{
		"subAgentActivity": map[string]any{
			"kind":             "started",
			"agent_thread_id":  "child-thread",
			"agent_path":       "/root/worker",
			"model":            "gpt-5.1",
			"reasoning_effort": "high",
		},
	}}}
	data, ok := appSubAgentActivityFromExecution(started)
	if !ok {
		t.Fatal("started activity was not recognized")
	}
	if data["model"] != "gpt-5.1" || data["reasoningEffort"] != "high" {
		t.Fatalf("activity data = %#v", data)
	}
	assertSubAgentActivityWire(t, data, "gpt-5.1", "high")

	// Other activity kinds carry null model/effort.
	interacted := &turn.ToolExecutionResult{Output: &tool.Output{Data: map[string]any{
		"subAgentActivity": map[string]any{"kind": "interacted", "agent_thread_id": "child-thread"},
	}}}
	data, ok = appSubAgentActivityFromExecution(interacted)
	if !ok {
		t.Fatal("interacted activity was not recognized")
	}
	assertSubAgentActivityWire(t, data, "", "")
}

func assertSubAgentActivityWire(t *testing.T, data map[string]any, wantModel, wantEffort string) {
	t.Helper()
	item := BuildThreadItem(session.Item{ID: "call-1", Type: "subAgentActivity", Data: data})
	encoded, err := json.Marshal(&item)
	if err != nil {
		t.Fatalf("marshal thread item: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal thread item: %v", err)
	}
	if wantModel == "" {
		if decoded["model"] != nil {
			t.Fatalf("wire model = %#v, want null (item = %s)", decoded["model"], encoded)
		}
	} else if decoded["model"] != wantModel {
		t.Fatalf("wire model = %#v, want %q", decoded["model"], wantModel)
	}
	if wantEffort == "" {
		if decoded["reasoningEffort"] != nil {
			t.Fatalf("wire reasoningEffort = %#v, want null (item = %s)", decoded["reasoningEffort"], encoded)
		}
	} else if decoded["reasoningEffort"] != wantEffort {
		t.Fatalf("wire reasoningEffort = %#v, want %q", decoded["reasoningEffort"], wantEffort)
	}
}
