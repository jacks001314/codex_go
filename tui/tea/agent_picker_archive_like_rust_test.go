package tea

import (
	"testing"

	codextui "codex_go/tui"
)

func TestAgentPickerVisibilityFollowsArchiveStateLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 36})
	model.State.ThreadID = "root"
	model.agentItems = []codextui.AgentThreadEntry{
		{ThreadID: "root", IsPrimary: true},
		{ThreadID: "worker-1"},
		{ThreadID: "worker-2"},
	}
	model.showAgentPicker(model.visibleAgentEntries(model.agentItems), "root", "worker-1")
	if model.modal == nil || model.modal.id != "agent-picker" {
		t.Fatalf("agent picker = %#v", model.modal)
	}
	if modalOptionIndex(model.modal, "worker-2") < 0 {
		t.Fatalf("initial picker options = %#v", model.modal.options)
	}

	model.setAgentPickerThreadVisibility("worker-1", true)
	if modalOptionIndex(model.modal, "worker-1") >= 0 {
		t.Fatalf("archived worker remained visible: %#v", model.modal.options)
	}
	if got := model.agentPickerSelectedThreadID(); got != "worker-2" {
		t.Fatalf("selection after archive = %q, want worker-2", got)
	}

	model.setAgentPickerThreadVisibility("worker-1", false)
	if modalOptionIndex(model.modal, "worker-1") < 0 {
		t.Fatalf("unarchived worker was not restored: %#v", model.modal.options)
	}
	if got := model.agentPickerSelectedThreadID(); got != "worker-2" {
		t.Fatalf("selection after unarchive = %q, want preserved worker-2", got)
	}
}

func TestAgentRefreshReplacesStaleCachedEntriesLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 36})
	model.State.ThreadID = "root"
	model.agentItems = []codextui.AgentThreadEntry{
		{ThreadID: "root", IsPrimary: true},
		{ThreadID: "archived-cache"},
	}
	model.showAgentPicker(model.agentItems, "root", "")
	model.applyAgentListResult(AgentListResultMsg{
		CurrentThreadID: "root",
		Entries: []codextui.AgentThreadEntry{
			{ThreadID: "root", IsPrimary: true},
		},
	})
	if model.modal == nil || model.modal.id != "agent-picker" {
		t.Fatalf("agent picker after refresh = %#v", model.modal)
	}
	if modalOptionIndex(model.modal, "archived-cache") >= 0 {
		t.Fatalf("stale cached agent survived refresh: %#v", model.modal.options)
	}
}

func TestAgentThreadVisibilityNotificationUpdatesOpenPickerLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 36})
	model.State.ThreadID = "root"
	model.agentItems = []codextui.AgentThreadEntry{
		{ThreadID: "root", IsPrimary: true},
		{ThreadID: "worker-1"},
	}
	model.showAgentPicker(model.agentItems, "root", "")
	updated, _ := model.Update(AgentThreadVisibilityMsg{ThreadID: "worker-1", Archived: true})
	model = updated.(*Model)
	if modalOptionIndex(model.modal, "worker-1") >= 0 {
		t.Fatalf("archived notification left worker visible: %#v", model.modal.options)
	}
	updated, _ = model.Update(AgentThreadVisibilityMsg{ThreadID: "worker-1", Archived: false})
	model = updated.(*Model)
	if modalOptionIndex(model.modal, "worker-1") < 0 {
		t.Fatalf("unarchive notification did not restore worker: %#v", model.modal.options)
	}
}
