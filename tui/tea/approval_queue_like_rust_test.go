package tea

import (
	"testing"

	codextui "codex_go/tui"
)

func TestApprovalRequestsQueueInsteadOfReplacing(t *testing.T) {
	animations := false
	model := NewModel(codextui.NewState(nil), Options{Width: 90, Height: 24, AnimationsEnabled: &animations})
	model.Update(ApprovalRequestMsg{ID: "first", Title: "Run first?", Command: "echo first"})
	if model.modal == nil || model.modal.id != "first" {
		t.Fatalf("first approval modal = %#v", model.modal)
	}

	model.Update(ApprovalRequestMsg{ID: "second", Title: "Run second?", Command: "echo second"})
	if model.modal == nil || model.modal.id != "first" {
		t.Fatalf("second approval replaced the visible modal: %#v", model.modal)
	}
	if len(model.pendingApprovals) != 1 || model.pendingApprovals[0].ID != "second" {
		t.Fatalf("pending approvals = %#v", model.pendingApprovals)
	}

	model.respondModal(false)
	if model.modal == nil || model.modal.id != "second" {
		t.Fatalf("queued approval did not open after the first response: %#v", model.modal)
	}
	if len(model.pendingApprovals) != 0 {
		t.Fatalf("pending approvals after dequeue = %#v", model.pendingApprovals)
	}
}
