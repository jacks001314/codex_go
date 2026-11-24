package tea

import (
	"strings"
	"testing"

	bubbletea "charm.land/bubbletea/v2"

	codextui "codex_go/tui"
)

// Rust #62159: a subagent transcript link opens the agent beside the parent
// conversation in the sidebar reader, even when the sidebar list has not
// discovered the agent yet and only the agent list carries its ancestry.
func TestSubAgentTranscriptLinkOpensSidebarReaderLikeRust(t *testing.T) {
	model := newSidebarSubagentLinkModel(t)

	runTeaCmd(t, model, model.openThreadLink("agent-1"))

	if model.State.ThreadID != "parent-a" {
		t.Fatalf("main thread = %q, want the parent conversation", model.State.ThreadID)
	}
	side := model.activeSide
	if side == nil || !side.WatchOnly || side.SideThreadID != "agent-1" {
		t.Fatalf("side pane = %#v, want a watch-only agent-1 reader", side)
	}
	if !strings.Contains(side.SideMessages[0].Text, "agent transcript for agent-1") {
		t.Fatalf("side messages = %#v", side.SideMessages)
	}
}

// A deferred click resolves through the same path once discovery finishes, so a
// nested agent still lands in the reader rather than replacing the main chat.
func TestDeferredSubAgentLinkOpensSidebarReaderLikeRust(t *testing.T) {
	model := newSidebarSubagentLinkModel(t)
	model.pendingAgentLinkSwitchThreadID = "agent-2"

	runTeaCmd(t, model, model.resumeDeferredAgentLink())

	if model.State.ThreadID != "parent-a" {
		t.Fatalf("main thread = %q, want the parent conversation", model.State.ThreadID)
	}
	side := model.activeSide
	if side == nil || !side.WatchOnly || side.SideThreadID != "agent-2" {
		t.Fatalf("side pane = %#v, want the linked nested agent", side)
	}
	if model.pendingAgentLinkSwitchThreadID != "" {
		t.Fatalf("deferred link survived: %q", model.pendingAgentLinkSwitchThreadID)
	}
}

// A link to a thread outside the current conversation's spawn tree keeps the
// ordinary switch: the reader is only for parent-owned agents.
func TestTranscriptLinkOutsideConversationSwitchesLikeRust(t *testing.T) {
	model := newSidebarSubagentLinkModel(t)

	runTeaCmd(t, model, model.openThreadLink("unrelated"))

	if model.activeSide != nil {
		t.Fatalf("foreign link opened a side pane: %#v", model.activeSide)
	}
	if model.State.ThreadID != "unrelated" {
		t.Fatalf("main thread = %q, want the ordinary switch", model.State.ThreadID)
	}
}

func newSidebarSubagentLinkModel(t *testing.T) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	state.SetThreadID("parent-a")
	state.AddMessage(codextui.RoleUser, "hello from the main chat")
	model := NewModel(state, Options{
		Width:  120,
		Height: 30,
		OnSwitchAgent: func(threadID string) (AgentThreadSwitchResponse, error) {
			return AgentThreadSwitchResponse{
				Entry:    codextui.AgentThreadEntry{ThreadID: threadID},
				Messages: []codextui.Message{{Role: codextui.RoleAssistant, Text: "agent transcript for " + threadID}},
				Status:   "idle",
			}, nil
		},
		OnSubmit: func(string) bubbletea.Cmd { return nil },
	})
	model.featureSettings = map[string]bool{sideWorkspaceFeatureKey: true}
	// Only the agent list knows these threads: the sidebar tree is still empty
	// until its own discovery runs.
	model.agentItems = []codextui.AgentThreadEntry{
		{ThreadID: "parent-a", IsPrimary: true},
		{ThreadID: "agent-1", ParentThreadID: "parent-a", AgentNickname: "Scout", AgentRole: "worker"},
		{ThreadID: "agent-2", ParentThreadID: "agent-1", AgentNickname: "Deep", AgentRole: "worker"},
		{ThreadID: "unrelated", ParentThreadID: "other-root"},
	}
	model.refreshTranscript()
	return model
}
