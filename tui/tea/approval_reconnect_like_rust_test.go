package tea

import (
	"testing"

	bubbletea "charm.land/bubbletea/v2"

	codextui "codex_go/tui"
)

func reconnectTestModel(t *testing.T) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	state.SetThreadID("thread-active")
	model := NewModel(state, Options{Width: 100, Height: 40})
	model.ensureSize()
	return model
}

// Rust #58720: a disconnect retires the approvals whose broker died and asks the
// host to reattach exactly the threads that were still waiting.
func TestApprovalDisconnectRetiresQueueAndRequestsReattachLikeRust(t *testing.T) {
	model := reconnectTestModel(t)
	var requested [][]string
	model.onReconnectThreadApprovals = func(threadIDs []string) bubbletea.Cmd {
		requested = append(requested, append([]string(nil), threadIDs...))
		return nil
	}
	model.routeApprovalRequest(ApprovalRequestMsg{ID: "approval-1", ServerRequestID: "srv-1", ThreadID: "thread-bg-1", Title: "Run command?"})
	model.routeApprovalRequest(ApprovalRequestMsg{ID: "approval-2", ServerRequestID: "srv-2", ThreadID: "thread-bg-2", Title: "Run command?"})
	model.routeApprovalRequest(ApprovalRequestMsg{ID: "approval-3", ServerRequestID: "srv-1", ThreadID: "thread-bg-1", Title: "Run command?"})
	if got := len(model.PendingApprovals()); got != 2 {
		t.Fatalf("queued approvals = %d, want 2 (the duplicate request id is dropped)", got)
	}

	_, cmd := model.Update(AppServerDisconnectMsg{})
	if got := len(model.PendingApprovals()); got != 0 {
		t.Fatalf("queue survived the disconnect: %#v", model.PendingApprovals())
	}
	if cmd != nil {
		_ = cmd()
	}
	if len(requested) != 1 || len(requested[0]) != 2 || requested[0][0] != "thread-bg-1" || requested[0][1] != "thread-bg-2" {
		t.Fatalf("reattach request = %#v, want both waiting threads once", requested)
	}
}

// Rust retires a closed thread's pending prompt with the thread: an archived
// thread is neither reattached nor allowed to reopen a popup on replay.
func TestApprovalReconnectSkipsArchivedThreadsLikeRust(t *testing.T) {
	model := reconnectTestModel(t)
	var requested [][]string
	model.onReconnectThreadApprovals = func(threadIDs []string) bubbletea.Cmd {
		requested = append(requested, append([]string(nil), threadIDs...))
		return nil
	}
	model.routeApprovalRequest(ApprovalRequestMsg{ID: "approval-1", ServerRequestID: "srv-1", ThreadID: "thread-bg-1", Title: "Run command?"})
	model.archivedAgentThreads = map[string]bool{"thread-bg-1": true}

	_, cmd := model.Update(AppServerDisconnectMsg{})
	if cmd != nil {
		_ = cmd()
	}
	if len(requested) != 0 {
		t.Fatalf("archived thread was reattached: %#v", requested)
	}
	if cmd := model.routeApprovalRequest(ApprovalRequestMsg{ID: "approval-9", ServerRequestID: "srv-9", ThreadID: "thread-bg-1"}); cmd != nil {
		t.Fatal("a replay for an archived thread opened a popup")
	}
	if got := len(model.PendingApprovals()); got != 0 {
		t.Fatalf("archived thread rebuilt %d queued approvals", got)
	}
}
