package tea

import (
	"testing"

	codextui "codex_go/tui"
)

type noopApprovalReplayMsg struct{}

func newApprovalReplayModel(t *testing.T, threadID string) *Model {
	t.Helper()
	animations := false
	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 30, AnimationsEnabled: &animations})
	model.State.ThreadID = threadID
	return model
}

// Rust's enqueue_primary_thread_request delivers a request to the primary
// widget while primary_thread_id is still unknown. Waiting for a thread id the
// model has not been told about would strand the prompt with no menu.
func TestApprovalOpensBeforePrimaryThreadKnownLikeRust(t *testing.T) {
	model := newApprovalReplayModel(t, "")

	model.routeApprovalRequest(ApprovalRequestMsg{
		ID:       "approval-1",
		ThreadID: "01a0b1c2-3d4e-7f60-8a9b-00000000e435",
		Title:    "Run command?",
	})

	if model.modal == nil || model.modal.kind != ModalKindApproval || model.modal.id != "approval-1" {
		t.Fatalf("modal = %#v, want the approval request before the primary thread is known", model.modal)
	}
	if len(model.pendingApprovals) != 0 {
		t.Fatalf("pending approvals = %#v, want none", model.pendingApprovals)
	}
}

// Once the active thread id arrives, the queued request for that thread is
// replayed without waiting for a further user action (Rust replays the
// primary-thread buffer when the thread starts).
func TestApprovalReplaysWhenThreadIDArrivesLikeRust(t *testing.T) {
	model := newApprovalReplayModel(t, "root")

	model.routeApprovalRequest(ApprovalRequestMsg{
		ID:       "approval-1",
		ThreadID: "worker-b",
		Title:    "Run command?",
	})
	if model.modal != nil {
		t.Fatalf("background approval opened over the active thread: %#v", model.modal)
	}

	model.State.SetThreadID("worker-b")
	model.Update(noopApprovalReplayMsg{})

	if model.modal == nil || model.modal.kind != ModalKindApproval || model.modal.id != "approval-1" {
		t.Fatalf("modal = %#v, want the replayed approval after the thread became active", model.modal)
	}
	if model.modal.threadID != "worker-b" {
		t.Fatalf("modal thread = %q, want worker-b", model.modal.threadID)
	}
	if len(model.pendingApprovals) != 0 {
		t.Fatalf("pending approvals after replay = %#v", model.pendingApprovals)
	}
}

// While the primary thread is unknown an approval that arrives behind another
// modal still belongs to the visible widget; answering the first one must
// surface it instead of leaving it queued forever.
func TestQueuedApprovalSurfacesWithoutThreadIDLikeRust(t *testing.T) {
	model := newApprovalReplayModel(t, "")

	model.routeApprovalRequest(ApprovalRequestMsg{ID: "approval-1", ThreadID: "worker-a", Title: "Run first?"})
	model.routeApprovalRequest(ApprovalRequestMsg{ID: "approval-2", ThreadID: "worker-b", Title: "Run second?"})
	if model.modal == nil || model.modal.id != "approval-1" {
		t.Fatalf("first modal = %#v", model.modal)
	}
	if len(model.pendingApprovals) != 1 {
		t.Fatalf("pending approvals = %#v", model.pendingApprovals)
	}

	model.modal = nil
	model.openNextThreadApproval()

	if model.modal == nil || model.modal.id != "approval-2" {
		t.Fatalf("modal after the first answer = %#v, want the queued request", model.modal)
	}
	if len(model.pendingApprovals) != 0 {
		t.Fatalf("pending approvals after the second prompt = %#v", model.pendingApprovals)
	}
}
