package tea

import (
	"testing"

	mousemsg "codex_go/tui/mousemsg"

	codextui "codex_go/tui"
)

// Rust #62159: a subagent link painted inside the reader still opens that agent
// beside the parent conversation, so the unfocused pane is a real link source.
func TestSidebarSubagentPaneLinkOpensNestedAgentLikeRust(t *testing.T) {
	model, _ := newSidebarSubagentPaneModel(t)
	runTeaCmd(t, model, model.switchToSidebarAgent("agent-1"))
	side := model.activeSide
	if side == nil || !side.WatchOnly {
		t.Fatalf("active side = %#v, want the watch-only reader", side)
	}
	// Thread links only carry canonical ids, so register a nested agent with one.
	model.mergeSidebarAgentEntries("parent-a", []codextui.AgentThreadEntry{
		{ThreadID: teaThreadLinkID, ParentThreadID: "agent-1", AgentNickname: "Nested"},
	})
	side.SideMessages = append(side.SideMessages, codextui.Message{
		Role: codextui.RoleAssistant,
		Text: "See [Nested agent](" + codextui.ThreadLinkDestination(teaThreadLinkID) + ").",
	})
	_ = model.viewString()
	if len(model.sidePaneThreadLinks) == 0 {
		model.renderSideWorkspace(model.width, model.height)
	}
	row, link, ok := firstSidePaneThreadLink(model.sidePaneThreadLinks)
	if !ok || link.ThreadID != teaThreadLinkID {
		t.Fatalf("side pane links = %#v, want the nested agent link", model.sidePaneThreadLinks)
	}

	_, cmd := model.Update(mousemsg.MouseMsg{
		X:      link.Start,
		Y:      row,
		Action: mousemsg.MouseActionPress,
		Button: mousemsg.MouseButtonLeft,
		Ctrl:   true,
	})
	if cmd == nil {
		t.Fatalf("side pane link click produced no command (link=%#v row=%d)", link, row)
	}
	runTeaCmd(t, model, cmd)

	if model.State.ThreadID != "parent-a" {
		t.Fatalf("main thread = %q, want the parent conversation", model.State.ThreadID)
	}
	if model.activeSide == nil || model.activeSide.SideThreadID != teaThreadLinkID {
		t.Fatalf("active side = %#v, want the linked nested agent", model.activeSide)
	}
}

func firstSidePaneThreadLink(links map[int][]regionThreadLink) (int, regionThreadLink, bool) {
	row := -1
	var found regionThreadLink
	for candidate, rowLinks := range links {
		if len(rowLinks) == 0 {
			continue
		}
		if row >= 0 && candidate >= row {
			continue
		}
		row, found = candidate, rowLinks[0]
	}
	return row, found, row >= 0
}
