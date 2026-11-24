package tea

import (
	"strings"
	"testing"

	mousemsg "codex_go/tui/mousemsg"

	"codex_go/protocol"
	codextui "codex_go/tui"
)

// TestSubAgentLinkClickDefersUntilDiscoveryLikeRust mirrors Rust #62159: a
// transcript link clicked while agent discovery is still running opens the
// agent once the list arrives, instead of switching with an unknown entry.
func TestSubAgentLinkClickDefersUntilDiscoveryLikeRust(t *testing.T) {
	switched := []string{}
	state := codextui.NewState(&codextui.Options{Model: "gpt-5"})
	state.SetThreadID("root-thread")
	model := NewModel(state, Options{
		Width:  100,
		Height: 24,
		OnReadAgents: func(string) ([]codextui.AgentThreadEntry, error) {
			return []codextui.AgentThreadEntry{{ThreadID: teaSubAgentThreadID, AgentNickname: "reviewer"}}, nil
		},
		OnSwitchAgent: func(threadID string) (AgentThreadSwitchResponse, error) {
			switched = append(switched, threadID)
			return AgentThreadSwitchResponse{
				Entry:    codextui.AgentThreadEntry{ThreadID: threadID},
				Messages: []codextui.Message{{Role: codextui.RoleAssistant, Text: "agent transcript"}},
				Status:   "idle",
			}, nil
		},
	})
	item := protocol.SubAgentActivityItem("item-1", "started", teaSubAgentThreadID, "reviewer")
	model.renderSubAgentActivity(&item)
	_ = model.viewString()

	// Discovery is in flight: the picker refresh holds a pending request.
	discovery := model.applyAgentCommand()
	if model.pendingAgentRefreshRequestID == 0 {
		t.Fatal("discovery did not start a pending agent list request")
	}

	region := model.transcriptSelectionRegion
	clicked := false
	for row, line := range region.text {
		column := findDisplayColumn(line, "reviewer")
		if column < 0 {
			continue
		}
		_, cmd := model.Update(mousemsg.MouseMsg{
			X:      region.x + column,
			Y:      region.y + row,
			Action: mousemsg.MouseActionPress,
			Button: mousemsg.MouseButtonLeft,
			Ctrl:   true,
		})
		runTeaCmd(t, model, cmd)
		clicked = true
		break
	}
	if !clicked {
		t.Fatalf("subagent label not found in %#v", region.text)
	}
	if len(switched) != 0 {
		t.Fatalf("link switched %#v before discovery finished", switched)
	}
	if model.pendingAgentLinkSwitchThreadID != teaSubAgentThreadID {
		t.Fatalf("deferred link = %q, want %s", model.pendingAgentLinkSwitchThreadID, teaSubAgentThreadID)
	}

	result, cmd := model.Update(discovery())
	model = result.(*Model)
	runTeaCmd(t, model, cmd)
	if len(switched) != 1 || switched[0] != teaSubAgentThreadID {
		t.Fatalf("switched threads = %#v, want the linked agent after discovery", switched)
	}
	if model.pendingAgentLinkSwitchThreadID != "" {
		t.Fatalf("deferred link survived: %q", model.pendingAgentLinkSwitchThreadID)
	}
	if got := strings.TrimSpace(model.State.ThreadID); got != teaSubAgentThreadID {
		t.Fatalf("displayed thread = %q, want %s", got, teaSubAgentThreadID)
	}
}
