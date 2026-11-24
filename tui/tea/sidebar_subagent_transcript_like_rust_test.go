package tea

import (
	"strings"
	"testing"

	bubbletea "charm.land/bubbletea/v2"

	codextui "codex_go/tui"
)

// Rust #62157: selecting a parent-owned subagent from the sidebar opens its
// transcript in the sidebar pane while the parent conversation keeps the main
// pane, its messages, and its composer.
func TestSidebarSubagentOpensInWatchOnlyPaneLikeRust(t *testing.T) {
	model, _ := newSidebarSubagentPaneModel(t)

	cmd := model.switchToSidebarAgent("agent-1")
	if cmd == nil {
		t.Fatal("sidebar subagent open produced no command")
	}
	runTeaCmd(t, model, cmd)

	if model.State.ThreadID != "parent-a" {
		t.Fatalf("main thread = %q, want the parent conversation", model.State.ThreadID)
	}
	if len(model.State.Messages) != 1 || model.State.Messages[0].Text != "hello from the main chat" {
		t.Fatalf("main messages = %#v, want the parent transcript untouched", model.State.Messages)
	}
	side := model.activeSide
	if side == nil {
		t.Fatal("no side pane opened for the subagent")
	}
	if !side.WatchOnly || side.SideThreadID != "agent-1" || side.ParentThreadID != "parent-a" {
		t.Fatalf("side pane = %#v, want a watch-only agent-1 transcript beside parent-a", side)
	}
	if side.ShowingSide {
		t.Fatal("watch-only pane took the composer from the parent conversation")
	}
	if len(side.SideMessages) != 1 || !strings.Contains(side.SideMessages[0].Text, "agent transcript for agent-1") {
		t.Fatalf("side messages = %#v, want the loaded agent transcript", side.SideMessages)
	}
	if tabs := model.sidebarTabsFor("parent-a"); len(tabs) != 1 || tabs[0] != side {
		t.Fatalf("sidebar tabs = %#v, want one tab for the agent", tabs)
	}
	// The pane never becomes the main conversation.
	if cmd := model.toggleSideConversation(); cmd != nil || side.ShowingSide || model.State.ThreadID != "parent-a" {
		t.Fatalf("toggle promoted the watch-only transcript: cmd=%v showing=%t thread=%q", cmd, side.ShowingSide, model.State.ThreadID)
	}
}

// Closing the reader drops the tab and leaves the agent's own thread alone.
func TestSidebarSubagentTranscriptCloseKeepsAgentThreadLikeRust(t *testing.T) {
	model, closed := newSidebarSubagentPaneModel(t)
	runTeaCmd(t, model, model.switchToSidebarAgent("agent-1"))
	side := model.activeSide
	if side == nil || !side.WatchOnly {
		t.Fatalf("side pane = %#v, want a watch-only transcript", side)
	}

	runTeaCmd(t, model, model.executeSidebarTabClose(side))

	if *closed != 0 {
		t.Fatalf("close callback ran %d times; the subagent thread must not be archived", *closed)
	}
	if model.activeSide != nil {
		t.Fatalf("active side survived the close: %#v", model.activeSide)
	}
	if tabs := model.sidebarTabsFor("parent-a"); len(tabs) != 0 {
		t.Fatalf("tabs after close = %#v", tabs)
	}
	if model.State.ThreadID != "parent-a" || len(model.State.Messages) != 1 {
		t.Fatalf("parent conversation changed: thread=%q messages=%#v", model.State.ThreadID, model.State.Messages)
	}
	if losses := model.sidebarTabCloseLosses(side); len(losses) != 0 {
		t.Fatalf("watch-only close asked to discard %#v", losses)
	}
}

// The watch-only pane paints only the transcript: no composer row and no status
// line, unlike a side chat.
func TestSidebarSubagentPanePaintsNoComposerLikeRust(t *testing.T) {
	model, _ := newSidebarSubagentPaneModel(t)
	runTeaCmd(t, model, model.switchToSidebarAgent("agent-1"))
	side := model.activeSide
	if side == nil {
		t.Fatal("no side pane opened")
	}
	rows, _, _ := model.renderPassiveSidePane(side, true, 40, 12)
	if len(rows) != 12 {
		t.Fatalf("pane rows = %d, want 12", len(rows))
	}
	joined := strings.Join(rows, "\n")
	if !strings.Contains(joined, "agent transcript for agent-1") {
		t.Fatalf("pane did not paint the agent transcript:\n%s", joined)
	}
	// The side-chat composer preview starts with U+203A; the reader never does.
	for _, glyph := range []string{"\u203a", "\u203A"} {
		if strings.Contains(joined, glyph) {
			t.Fatalf("watch-only pane painted a composer preview:\n%s", joined)
		}
	}
}

// A thread outside the parent's spawn tree keeps the ordinary switch path.
func TestSidebarSubagentOpenFallsBackForForeignThreadLikeRust(t *testing.T) {
	model, _ := newSidebarSubagentPaneModel(t)
	runTeaCmd(t, model, model.switchToSidebarAgent("stranger"))
	if model.activeSide != nil {
		t.Fatalf("foreign thread opened a side pane: %#v", model.activeSide)
	}
	if model.State.ThreadID != "stranger" {
		t.Fatalf("main thread = %q, want the ordinary switch to stranger", model.State.ThreadID)
	}
}

func newSidebarSubagentPaneModel(t *testing.T) (*Model, *int) {
	t.Helper()
	state := codextui.NewState(nil)
	state.SetThreadID("parent-a")
	state.AddMessage(codextui.RoleUser, "hello from the main chat")
	closed := 0
	model := NewModel(state, Options{
		Width:  120,
		Height: 30,
		OnSwitchAgent: func(threadID string) (AgentThreadSwitchResponse, error) {
			return AgentThreadSwitchResponse{
				Entry:    codextui.AgentThreadEntry{ThreadID: threadID, ParentThreadID: "parent-a"},
				Messages: []codextui.Message{{Role: codextui.RoleAssistant, Text: "agent transcript for " + threadID}},
				Status:   "idle",
			}, nil
		},
		OnCloseSide: func(SideCloseParams) (SideCloseResponse, error) {
			closed++
			return SideCloseResponse{}, nil
		},
		OnSubmit: func(string) bubbletea.Cmd { return nil },
	})
	model.featureSettings = map[string]bool{sideWorkspaceFeatureKey: true}
	model.mergeSidebarAgentEntries("parent-a", []codextui.AgentThreadEntry{
		{ThreadID: "parent-a", IsPrimary: true},
		{ThreadID: "agent-1", ParentThreadID: "parent-a", AgentNickname: "Scout", AgentRole: "worker"},
	})
	model.refreshTranscript()
	return model, &closed
}
