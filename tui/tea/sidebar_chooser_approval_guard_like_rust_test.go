package tea

import (
	"testing"
	"time"

	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
)

// Rust #59739: a key press that belongs to the focused sidebar chooser must
// not answer an approval dialog that has not been painted yet. Go keeps the
// approval behind the composer typing-idle delay and lets the chooser own
// input until it is painted.
func TestSidebarChooserInputCannotApprovePendingDialogLikeRust(t *testing.T) {
	animations := false
	state := codextui.NewState(nil)
	state.SetThreadID("thread-1")
	model := NewModel(state, Options{Width: 120, Height: 30, AnimationsEnabled: &animations})
	model.featureSettings = map[string]bool{sideWorkspaceFeatureKey: true}
	now := time.Unix(1500, 0)
	model.now = func() time.Time { return now }

	model.sidebarChooserOpen = true
	model.sidebarChooserParent = "thread-1"
	model.sidebarChooserFocused = true
	model.recordComposerActivityAt(now)
	model.pendingApprovals = append(model.pendingApprovals, ApprovalRequestMsg{
		ID: "approval-1", Title: "Run the command?", Command: "echo hi",
	})
	if cmd := model.syncApprovalPromptDelay(); cmd == nil {
		t.Fatal("no typing-idle timer armed for the pending approval")
	}
	if model.modal != nil {
		t.Fatalf("approval painted before the composer went idle: %#v", model.modal)
	}

	model.Update(key(keymsg.KeyEnter))
	if model.modal != nil {
		t.Fatalf("chooser Enter opened the unpainted approval dialog: %#v", model.modal)
	}
	if len(model.pendingApprovals) != 1 {
		t.Fatalf("chooser Enter answered the pending approval: %#v", model.pendingApprovals)
	}

	// Once the composer is idle the approval is painted and owns input.
	now = now.Add(2 * approvalPromptTypingIdleDelay)
	model.applyApprovalPromptDelay(approvalPromptDelayMsg{token: model.approvalPromptDelay.token})
	if model.modal == nil || model.modal.id != "approval-1" {
		t.Fatalf("approval did not open after the idle delay: %#v", model.modal)
	}
}
