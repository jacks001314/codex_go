package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
)

func newSubagentApprovalModel(t *testing.T) *Model {
	t.Helper()
	animations := false
	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 30, AnimationsEnabled: &animations})
	model.State.ThreadID = "root"
	model.agentItems = []codextui.AgentThreadEntry{
		{ThreadID: "root", IsPrimary: true},
		{ThreadID: "worker-a", AgentNickname: "Ada", AgentRole: "explorer"},
		{ThreadID: "01a0b1c2-3d4e-7f60-8a9b-00000000e435", AgentPath: "/root/reviewer"},
		{ThreadID: "01a0b1c2-3d4e-7f60-8a9b-0000000000ff"},
	}
	return model
}

func TestSubagentApprovalGroupsPickerRowsLikeRust(t *testing.T) {
	model := newSubagentApprovalModel(t)
	pendingThread := "01a0b1c2-3d4e-7f60-8a9b-00000000e435"

	if cmd := model.routeApprovalRequest(ApprovalRequestMsg{ID: "approval-1", ThreadID: pendingThread, Title: "Run command?"}); cmd != nil {
		t.Fatalf("background approval returned a command: %v", cmd)
	}
	if model.modal != nil {
		t.Fatalf("background approval opened a modal over the active thread: %#v", model.modal)
	}
	if len(model.pendingApprovals) != 1 {
		t.Fatalf("pending approvals = %#v", model.pendingApprovals)
	}

	model.showAgentPicker(model.visibleAgentEntries(model.agentItems), "root", "")
	if model.modal == nil || model.modal.id != "agent-picker" {
		t.Fatalf("agent picker = %#v", model.modal)
	}
	if got := strings.TrimSpace(model.modal.options[0].ID); got != pendingThread {
		t.Fatalf("first picker row = %q, want the waiting thread %q", got, pendingThread)
	}
	if label := model.modal.options[0].Label; !strings.Contains(label, "(needs approval)") {
		t.Fatalf("waiting row label = %q, want the approval marker", label)
	}
	if selected := model.agentPickerSelectedThreadID(); selected != pendingThread {
		t.Fatalf("initial selection = %q, want the first waiting thread", selected)
	}
	for _, option := range model.modal.options[1:] {
		if strings.Contains(option.Label, "(needs approval)") {
			t.Fatalf("unexpected approval marker on %q", option.Label)
		}
	}
}

func TestSubagentApprovalNoticeListsWaitingThreadsLikeRust(t *testing.T) {
	model := newSubagentApprovalModel(t)
	pendingThread := "01a0b1c2-3d4e-7f60-8a9b-00000000e435"
	model.routeApprovalRequest(ApprovalRequestMsg{ID: "approval-1", ThreadID: pendingThread})

	if labels := model.pendingThreadApprovalLabels(); len(labels) != 1 || labels[0] != "/root/reviewer" {
		t.Fatalf("pending labels = %#v, want the agent path", labels)
	}
	bottom := model.renderBottomPane()
	if !strings.Contains(bottom, "Approval needed in /root/reviewer") {
		t.Fatalf("bottom pane = %q, want the approval notice", bottom)
	}
	if !strings.Contains(bottom, "/subagents to review requests") {
		t.Fatalf("bottom pane = %q, want the review hint", bottom)
	}
}

func TestSubagentApprovalReplaysWhenThreadSelectedLikeRust(t *testing.T) {
	model := newSubagentApprovalModel(t)
	pendingThread := "01a0b1c2-3d4e-7f60-8a9b-00000000e435"
	model.routeApprovalRequest(ApprovalRequestMsg{ID: "approval-1", ThreadID: pendingThread, Title: "Run command?"})

	model.switchThreadID(pendingThread)
	if model.modal == nil || model.modal.id != "approval-1" || model.modal.threadID != pendingThread {
		t.Fatalf("approval after switching = %#v", model.modal)
	}
	if len(model.pendingApprovals) != 0 {
		t.Fatalf("pending approvals after replay = %#v", model.pendingApprovals)
	}
	if labels := model.pendingThreadApprovalLabels(); len(labels) != 0 {
		t.Fatalf("labels for the active thread = %#v, want none", labels)
	}
}

func TestSubagentApprovalLabelsMatchThreadLabelLikeRust(t *testing.T) {
	model := newSubagentApprovalModel(t)
	cases := map[string]string{
		"root":                                 "Main [default]",
		"worker-a":                             "Ada [explorer]",
		"01a0b1c2-3d4e-7f60-8a9b-00000000e435": "/root/reviewer",
		"01a0b1c2-3d4e-7f60-8a9b-0000000000ff": "Agent (0000000000ff)",
		"unknown-thread":                       "Agent (thread)",
	}
	for threadID, want := range cases {
		if got := model.threadLabel(threadID); got != want {
			t.Fatalf("threadLabel(%q) = %q, want %q", threadID, got, want)
		}
	}
}

func TestAgentPickerKeepsSelectionWhenApprovalReordersRowsLikeRust(t *testing.T) {
	model := newSubagentApprovalModel(t)
	model.showAgentPicker(model.visibleAgentEntries(model.agentItems), "root", "worker-a")
	if got := model.agentPickerSelectedThreadID(); got != "worker-a" {
		t.Fatalf("selection before the approval = %q", got)
	}

	model.routeApprovalRequest(ApprovalRequestMsg{
		ID:       "approval-1",
		ThreadID: "01a0b1c2-3d4e-7f60-8a9b-00000000e435",
	})
	if model.modal == nil || model.modal.id != "agent-picker" {
		t.Fatalf("picker did not stay open: %#v", model.modal)
	}
	if got := model.agentPickerSelectedThreadID(); got != "worker-a" {
		t.Fatalf("selection after reorder = %q, want worker-a", got)
	}
	if got := strings.TrimSpace(model.modal.options[0].ID); got == "worker-a" {
		t.Fatalf("waiting thread did not move to the top: %#v", model.modal.options)
	}
}

func TestSubagentApprovalAddsPickerRowWithoutMetadataLikeRust(t *testing.T) {
	model := newSubagentApprovalModel(t)
	lateThread := "01a0b1c2-3d4e-7f60-8a9b-00000000beef"
	model.routeApprovalRequest(ApprovalRequestMsg{ID: "approval-1", ThreadID: lateThread})

	model.showAgentPicker(model.visibleAgentEntries(model.agentItems), "root", "")
	index := modalOptionIndex(model.modal, lateThread)
	if index < 0 {
		t.Fatalf("picker rows = %#v, want a row for the metadata-less thread", model.modal.options)
	}
	if index != 0 {
		t.Fatalf("metadata-less waiting thread at %d, want it first", index)
	}
	if label := model.modal.options[index].Label; !strings.Contains(label, "(needs approval)") {
		t.Fatalf("row label = %q, want the approval marker", label)
	}
}
