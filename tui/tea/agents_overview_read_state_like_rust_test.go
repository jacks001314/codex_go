package tea

import (
	"testing"

	codextui "codex_go/tui"
	agentsoverview "codex_go/tui/agents_overview"
)

func TestAgentThreadReadStateUpdatesDashboardRowsLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 24})
	model.State.ThreadID = "thread-1"
	model.agentsOverview = agentsoverview.New([]agentsoverview.Row{
		{ThreadID: "thread-1", Name: "One", Group: agentsoverview.GroupWorking},
	}, "", false)

	model.applyAgentThreadReadState(AgentThreadReadStateMsg{
		ThreadID: "thread-1",
		Unread:   true,
		Revision: "revision-1",
	})
	if got := model.agentsOverview.Rows[0]; !got.Unread || got.UnreadRevision != "revision-1" {
		t.Fatalf("unread row = %#v", got)
	}
	if got := model.unreadThreadRevisions["thread-1"]; got != "revision-1" {
		t.Fatalf("unread revision = %q", got)
	}

	model.applyAgentThreadReadState(AgentThreadReadStateMsg{
		ThreadID: "thread-1",
		Unread:   false,
		Revision: "revision-2",
	})
	if got := model.agentsOverview.Rows[0]; got.Unread || got.UnreadRevision != "revision-2" {
		t.Fatalf("read row = %#v", got)
	}
	if _, ok := model.unreadThreadRevisions["thread-1"]; ok {
		t.Fatal("read notification retained the unread revision")
	}
}

func TestFocusedConversationAcknowledgesEachObservedRevisionOnceLikeRust(t *testing.T) {
	type call struct {
		threadID string
		revision string
	}
	var calls []call
	model := NewModel(codextui.NewState(nil), Options{
		Width:  100,
		Height: 24,
		OnAcknowledgeThreadRead: func(threadID, revision string) {
			calls = append(calls, call{threadID, revision})
		},
	})
	model.State.ThreadID = "thread-1"
	model.terminalFocused = true
	model.rememberThreadReadState("thread-1", true, "revision-1")

	model.observeFocusedThreadRead()
	model.observeFocusedThreadRead()
	if len(calls) != 1 || calls[0] != (call{"thread-1", "revision-1"}) {
		t.Fatalf("first view calls = %#v", calls)
	}

	model.rememberThreadReadState("thread-1", true, "revision-2")
	model.observeFocusedThreadRead()
	if len(calls) != 2 || calls[1] != (call{"thread-1", "revision-2"}) {
		t.Fatalf("new receipt calls = %#v", calls)
	}

	// A hidden dashboard does not count as viewing the conversation. Returning
	// to it is a new view, so the same receipt may be acknowledged again.
	model.agentsOverview = agentsoverview.New(nil, "", false)
	model.observeFocusedThreadRead()
	model.agentsOverview = nil
	model.observeFocusedThreadRead()
	if len(calls) != 3 || calls[2] != (call{"thread-1", "revision-2"}) {
		t.Fatalf("returning view calls = %#v", calls)
	}
}

func TestFocusedConversationDefersReceiptWhileTurnRunsLikeRust(t *testing.T) {
	var revisions []string
	model := NewModel(codextui.NewState(nil), Options{
		Width:  100,
		Height: 24,
		OnAcknowledgeThreadRead: func(_, revision string) {
			revisions = append(revisions, revision)
		},
	})
	model.State.ThreadID = "thread-1"
	model.terminalFocused = true
	model.observeFocusedThreadRead()

	model.State.Status = "running"
	model.rememberThreadReadState("thread-1", true, "revision-running")
	model.observeFocusedThreadRead()
	if len(revisions) != 0 {
		t.Fatalf("acknowledged while the turn was running: %#v", revisions)
	}

	model.State.Status = "idle"
	model.observeFocusedThreadRead()
	if len(revisions) != 1 || revisions[0] != "revision-running" {
		t.Fatalf("acknowledged after the turn = %#v", revisions)
	}
}

func TestReadOnlyViewAcknowledgesFrozenReceiptLikeRust(t *testing.T) {
	var acknowledged string
	model := NewModel(codextui.NewState(nil), Options{
		Width:  100,
		Height: 24,
		OnAcknowledgeThreadRead: func(_, revision string) {
			acknowledged = revision
		},
	})
	model.State.ThreadID = "thread-1"
	model.terminalFocused = true
	model.rememberThreadReadState("thread-1", true, "revision-frozen")
	model.setReadOnlyThread(true)
	if model.frozenReadReceipt == nil || model.frozenReadReceipt.revision != "revision-frozen" {
		t.Fatalf("frozen receipt = %#v", model.frozenReadReceipt)
	}
	if _, ok := model.unreadThreadRevisions["thread-1"]; ok {
		t.Fatal("freezing retained the live unread revision")
	}
	model.observeFocusedThreadRead()
	if acknowledged != "revision-frozen" {
		t.Fatalf("acknowledged revision = %q", acknowledged)
	}
}
