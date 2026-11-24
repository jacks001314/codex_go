package tea

import (
	"strings"
	"testing"

	mousemsg "codex_go/tui/mousemsg"

	"codex_go/protocol"
	codextui "codex_go/tui"
	"codex_go/tui/history_cell"
)

const teaSubAgentThreadID = "019e60fc-0518-7c21-9596-980fe97225bc"

// TestSubAgentActivityLabelOpensAgentLikeRust mirrors Rust #62159: the subagent
// label in transcript activity is a link that opens that agent's transcript.
// Go's saved-conversation links activate with Ctrl-click.
func TestSubAgentActivityLabelOpensAgentLikeRust(t *testing.T) {
	switched := []string{}
	state := codextui.NewState(&codextui.Options{Model: "gpt-5"})
	model := NewModel(state, Options{
		Width:  100,
		Height: 24,
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

	region := model.transcriptSelectionRegion
	if region.w <= 0 || region.h <= 0 {
		t.Fatalf("transcript region not registered: %#v", region)
	}
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
		clicked = true
		runTeaCmd(t, model, cmd)
		break
	}
	if !clicked {
		t.Fatalf("subagent label not found in %#v", region.text)
	}
	if len(switched) != 1 || switched[0] != teaSubAgentThreadID {
		t.Fatalf("switched threads = %#v, want %s", switched, teaSubAgentThreadID)
	}
	if got := strings.TrimSpace(model.State.ThreadID); got != teaSubAgentThreadID {
		t.Fatalf("displayed thread = %q, want %s", got, teaSubAgentThreadID)
	}
}

// TestSubAgentActivityLabelNeedsCanonicalThreadLikeRust keeps a non-canonical
// id from painting a link the TUI cannot resolve.
func TestSubAgentActivityLabelNeedsCanonicalThreadLikeRust(t *testing.T) {
	cell, ok := historycell.NewSubAgentActivity("started", "reviewer")
	if !ok {
		t.Fatal("subagent activity cell unavailable")
	}
	linked := linkSubAgentActivityCell(cell, "not-a-thread-id")
	if strings.Contains(strings.Join(linked.Lines, "\n"), codextui.CodexThreadLinkPrefix) {
		t.Fatalf("non-canonical id painted a link: %#v", linked.Lines)
	}
}
