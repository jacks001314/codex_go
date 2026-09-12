package tea

import (
	"testing"

	codextui "codex_go/tui"
)

// TestResumeAppliesServerModelAndProvider covers Rust #43360: restoring a
// session applies the thread's server-reported model and provider, while a
// summary without them leaves the current selection alone.
func TestResumeAppliesServerModelAndProvider(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 40})
	model.State.SetThreadID("thread-a")
	model.State.Model = "local-model"
	model.State.Provider = "local-provider"

	model.applyResumeResponse("thread-b", SessionResumeResponse{
		Summary: &codextui.SessionSummary{ThreadID: "thread-b", Model: "server-model", Provider: "server-provider"},
	})
	if model.State.Model != "server-model" || model.State.Provider != "server-provider" {
		t.Fatalf("state = (%q, %q), want the server metadata", model.State.Model, model.State.Provider)
	}

	model.applyResumeResponse("thread-c", SessionResumeResponse{
		Summary: &codextui.SessionSummary{ThreadID: "thread-c"},
	})
	if model.State.Model != "server-model" || model.State.Provider != "server-provider" {
		t.Fatalf("state = (%q, %q), want the previous selection preserved", model.State.Model, model.State.Provider)
	}
}

// TestAgentSwitchAppliesServerModelAndProvider covers the agent-switch path.
func TestAgentSwitchAppliesServerModelAndProvider(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 40})
	model.State.SetThreadID("thread-a")
	model.State.Model = "local-model"
	model.State.Provider = "local-provider"

	model.applyAgentSwitchResult(AgentSwitchResultMsg{
		ThreadID: "thread-b",
		Response: AgentThreadSwitchResponse{
			Entry:    codextui.AgentThreadEntry{ThreadID: "thread-b", AgentNickname: "Scout"},
			Messages: []codextui.Message{{Role: codextui.RoleAssistant, Text: "agent content"}},
			Status:   "idle",
			Model:    "server-model",
			Provider: "server-provider",
		},
	})
	if model.State.Model != "server-model" || model.State.Provider != "server-provider" {
		t.Fatalf("state = (%q, %q), want the server metadata", model.State.Model, model.State.Provider)
	}
}
