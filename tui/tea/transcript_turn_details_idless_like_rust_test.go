package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	"codex_go/tui/chatwidget"
	"codex_go/utils"
)

func sideMessagesContain(messages []codextui.Message, text string) bool {
	for _, message := range messages {
		if strings.Contains(utils.StripANSI(message.Text), text) {
			return true
		}
	}
	return false
}

// TestOlderHistorySideConversationDropsInterruptionNoticeLikeRust covers Rust
// #60554: older pages restore interruption notices for the main conversation
// only, because a side chat never owned that turn boundary.
func TestOlderHistorySideConversationDropsInterruptionNoticeLikeRust(t *testing.T) {
	page := func() OlderHistoryPage {
		return OlderHistoryPage{Messages: []codextui.Message{
			{Role: codextui.RoleUser, Text: "older-side-prompt"},
			{Role: codextui.RoleHistory, Text: chatwidget.ConversationInterruptedNotice, RawText: chatwidget.ConversationInterruptedNotice, InterruptionNotice: true},
		}}
	}

	side := historyPaginationModel(t, nil)
	side.State.SetThreadID("thread-side")
	side.sideConversations["thread-parent"] = &activeSideConversation{ParentThreadID: "thread-parent", SideThreadID: "thread-side"}
	side.applyOlderHistoryPage("thread-side", page(), nil)
	if sideMessagesContain(side.State.Messages, chatwidget.ConversationInterruptedNotice) {
		t.Fatal("side conversation restored a main-conversation interruption notice")
	}
	if !sideMessagesContain(side.State.Messages, "older-side-prompt") {
		t.Fatal("side conversation lost its older page")
	}

	main := historyPaginationModel(t, nil)
	main.State.SetThreadID("thread-main")
	main.applyOlderHistoryPage("thread-main", page(), nil)
	if !sideMessagesContain(main.State.Messages, chatwidget.ConversationInterruptedNotice) {
		t.Fatal("main conversation dropped the interruption notice")
	}
}

// TestCollapseTurnDetailsSummarizesToolCellsWithoutActivityIDsLikeRust covers
// Rust #60555: restored tool cells report no activity ids, and the summary must
// still hide their output while keeping a disclosure identity.
func TestCollapseTurnDetailsSummarizesToolCellsWithoutActivityIDsLikeRust(t *testing.T) {
	messages := []codextui.Message{
		{Role: codextui.RoleUser, Text: "generate an image"},
		{Role: codextui.RoleHistory, Text: "Image generation failed\nprivate prompt", RawText: "Image generation failed\nprivate prompt", ToolActivity: true},
		{Role: codextui.RoleAssistant, Text: "done"},
	}
	collapsed := CollapseTurnDetails(messages, nil)
	if collapsed == nil {
		t.Fatal("tool cell without activity ids was not summarized")
	}
	summary := collapsed[1]
	if !summary.TurnDetailSummary {
		t.Fatalf("summary row missing: %#v", summary)
	}
	if got, want := summary.Text, "\u25b8 Image generation failed"; got != want {
		t.Fatalf("summary = %q, want %q", got, want)
	}
	if strings.Contains(summary.Text, "private prompt") {
		t.Fatalf("summary leaked tool output: %q", summary.Text)
	}
	if len(summary.ActivityIDs) != 1 || summary.ActivityIDs[0] == "" {
		t.Fatalf("summary disclosure identity = %#v, want one stable id", summary.ActivityIDs)
	}

	group := []codextui.Message{
		{Role: codextui.RoleUser, Text: "inspect"},
		{Role: codextui.RoleHistory, Text: "Ran ls\n  \u2514 output-one", RawText: "Ran ls\n  \u2514 output-one", ToolActivity: true},
		{Role: codextui.RoleHistory, Text: "Ran pwd\n  \u2514 output-two", RawText: "Ran pwd\n  \u2514 output-two", ToolActivity: true},
		{Role: codextui.RoleAssistant, Text: "done"},
	}
	collapsedGroup := CollapseTurnDetails(group, nil)
	if collapsedGroup == nil || collapsedGroup[1].Text != "\u25b8 Ran 2 commands" {
		t.Fatalf("id-less tool group did not fold: %#v", collapsedGroup)
	}
	if strings.Contains(collapsedGroup[1].Text, "output-one") {
		t.Fatalf("folded group leaked output: %q", collapsedGroup[1].Text)
	}
}
