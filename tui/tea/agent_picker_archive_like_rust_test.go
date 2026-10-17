package tea

import (
	"errors"
	"reflect"
	"testing"

	"codex_go/protocol"
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

func TestAgentPickerArchiveCascadesToDescendantsLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 36})
	model.State.ThreadID = "root"
	model.agentItems = []codextui.AgentThreadEntry{
		{ThreadID: "root", IsPrimary: true},
		{ThreadID: "child", ParentThreadID: "root"},
		{ThreadID: "grandchild", ParentThreadID: "child"},
		{ThreadID: "sibling", ParentThreadID: "root"},
	}
	visibleIDs := func() []string {
		ids := []string{}
		for _, entry := range model.visibleThreads() {
			ids = append(ids, entry.ThreadID)
		}
		return ids
	}

	model.setAgentPickerThreadVisibility("child", true)
	if got, want := visibleIDs(), []string{"root", "sibling"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("visible after child archive = %#v, want %#v", got, want)
	}
	if !model.agentThreadArchived("child") || !model.agentThreadArchived("grandchild") {
		t.Fatalf("archived descendants = child:%v grandchild:%v", model.agentThreadArchived("child"), model.agentThreadArchived("grandchild"))
	}

	model.setAgentPickerThreadVisibility("child", false)
	if got, want := visibleIDs(), []string{"root", "child", "grandchild", "sibling"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("visible after child unarchive = %#v, want %#v", got, want)
	}

	model.setAgentPickerThreadVisibility("root", true)
	if got := visibleIDs(); len(got) != 0 {
		t.Fatalf("visible after root archive = %#v, want none", got)
	}

	model.setAgentPickerThreadVisibility("root", false)
	if got, want := visibleIDs(), []string{"root", "child", "grandchild", "sibling"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("visible after root unarchive = %#v, want %#v", got, want)
	}
}

func TestAgentPickerVisibilityChangeDuringRefreshQueuesFollowUp(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 36})
	model.State.ThreadID = "root"
	calls := 0
	model.onReadAgents = func(currentThreadID string) ([]codextui.AgentThreadEntry, error) {
		calls++
		if calls == 1 {
			return []codextui.AgentThreadEntry{
				{ThreadID: "root", IsPrimary: true},
				{ThreadID: "worker", ParentThreadID: "root"},
			}, nil
		}
		return []codextui.AgentThreadEntry{
			{ThreadID: "root", IsPrimary: true},
			{ThreadID: "worker", ParentThreadID: "root", Archived: true},
		}, nil
	}
	model.agentItems = []codextui.AgentThreadEntry{
		{ThreadID: "root", IsPrimary: true},
		{ThreadID: "worker", ParentThreadID: "root"},
	}
	refresh := model.applyAgentCommand()
	if refresh == nil {
		t.Fatal("agent command did not start a refresh")
	}
	requestID := model.pendingAgentRefreshRequestID
	first := refresh().(AgentListResultMsg)
	if len(first.Entries) != 2 {
		t.Fatalf("first refresh entries = %#v", first.Entries)
	}

	// Archive while the first request is still in flight. Its stale result must
	// not reveal the worker, and a follow-up refresh must be queued.
	model.setAgentPickerThreadVisibility("worker", true)
	followUp := model.applyAgentListResult(first)
	if followUp == nil {
		t.Fatal("visibility change during refresh did not queue a follow-up")
	}
	if got := model.visibleThreads(); len(got) != 1 || got[0].ThreadID != "root" {
		t.Fatalf("visible after stale result = %#v, want root only", got)
	}
	if requestID != first.RequestID {
		t.Fatalf("request id = %d, want %d", first.RequestID, requestID)
	}

	second := followUp().(AgentListResultMsg)
	if calls != 2 {
		t.Fatalf("reader calls = %d, want 2", calls)
	}
	if cmd := model.applyAgentListResult(second); cmd != nil {
		t.Fatalf("follow-up unexpectedly queued another refresh: %v", cmd)
	}
	if got := model.visibleThreads(); len(got) != 1 || got[0].ThreadID != "root" {
		t.Fatalf("visible after follow-up = %#v, want root only", got)
	}
}

func TestAgentPickerUnarchiveLoadsUncachedThreadLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 36})
	model.State.ThreadID = "root"
	model.agentItems = []codextui.AgentThreadEntry{{ThreadID: "root", IsPrimary: true}}
	calls := 0
	model.onReadAgents = func(currentThreadID string) ([]codextui.AgentThreadEntry, error) {
		calls++
		return []codextui.AgentThreadEntry{
			{ThreadID: "root", IsPrimary: true},
			{ThreadID: "worker", ParentThreadID: "root"},
		}, nil
	}
	command := model.setAgentPickerThreadVisibility("worker", false)
	if command == nil {
		t.Fatal("uncached unarchive did not request an agent refresh")
	}
	result := command().(AgentListResultMsg)
	if cmd := model.applyAgentListResult(result); cmd != nil {
		t.Fatalf("uncached unarchive queued an unexpected second refresh: %v", cmd)
	}
	if calls != 1 {
		t.Fatalf("reader calls = %d, want 1", calls)
	}
	if got := model.visibleThreads(); len(got) != 2 || got[1].ThreadID != "worker" {
		t.Fatalf("visible after uncached unarchive = %#v, want root + worker", got)
	}
}

func TestAgentPickerParentUnarchiveKeepsIndependentlyArchivedDescendantLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 36})
	model.State.ThreadID = "root"
	model.agentItems = []codextui.AgentThreadEntry{
		{ThreadID: "root", IsPrimary: true},
		{ThreadID: "child", ParentThreadID: "root"},
		{ThreadID: "grandchild", ParentThreadID: "child"},
	}
	model.setAgentPickerThreadVisibility("grandchild", true)
	if cause := model.agentThreadArchiveCause["grandchild"]; cause != "self" {
		t.Fatalf("independent archive cause = %q, want self", cause)
	}
	model.setAgentPickerThreadVisibility("root", true)
	if cause := model.agentThreadArchiveCause["child"]; cause != "parent:root" {
		t.Fatalf("cascaded child cause = %q, want parent:root", cause)
	}
	if cause := model.agentThreadArchiveCause["grandchild"]; cause != "self" {
		t.Fatalf("parent archive overwrote independent cause = %q", cause)
	}

	model.setAgentPickerThreadVisibility("root", false)
	visible := map[string]bool{}
	for _, entry := range model.visibleThreads() {
		visible[entry.ThreadID] = true
	}
	if !visible["root"] || !visible["child"] {
		t.Fatalf("parent unarchive did not restore root/child: %#v", visible)
	}
	if visible["grandchild"] {
		t.Fatalf("parent unarchive restored an independently archived descendant: %#v", visible)
	}

	model.setAgentPickerThreadVisibility("grandchild", false)
	if got := model.visibleThreads(); len(got) != 3 {
		t.Fatalf("visible after descendant unarchive = %#v", got)
	}
}

func TestAgentPickerFailedRefreshStillRunsQueuedFollowUp(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 36})
	model.State.ThreadID = "root"
	model.agentItems = []codextui.AgentThreadEntry{
		{ThreadID: "root", IsPrimary: true},
		{ThreadID: "worker", ParentThreadID: "root"},
	}
	calls := 0
	model.onReadAgents = func(currentThreadID string) ([]codextui.AgentThreadEntry, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("temporary agent list failure")
		}
		return []codextui.AgentThreadEntry{
			{ThreadID: "root", IsPrimary: true},
			{ThreadID: "worker", ParentThreadID: "root", Archived: true},
		}, nil
	}
	refresh := model.applyAgentCommand()
	if refresh == nil {
		t.Fatal("agent command did not start a refresh")
	}
	first := refresh().(AgentListResultMsg)
	if first.Err == nil {
		t.Fatal("fixture refresh should fail")
	}
	model.setAgentPickerThreadVisibility("worker", true)
	followUp := model.applyAgentListResult(first)
	if followUp == nil {
		t.Fatal("failed refresh dropped the queued visibility follow-up")
	}
	second := followUp().(AgentListResultMsg)
	if second.Err != nil {
		t.Fatalf("follow-up refresh error = %v", second.Err)
	}
	if cmd := model.applyAgentListResult(second); cmd != nil {
		t.Fatalf("unexpected third refresh: %v", cmd)
	}
	if calls != 2 {
		t.Fatalf("reader calls = %d, want 2", calls)
	}
	if got := model.visibleThreads(); len(got) != 1 || got[0].ThreadID != "root" {
		t.Fatalf("visible after failed-refresh follow-up = %#v, want root only", got)
	}
}

func TestAgentPickerArchiveMarksReplayOnlyLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 36})
	model.State.ThreadID = "root"
	model.agentItems = []codextui.AgentThreadEntry{
		{ThreadID: "root", IsPrimary: true},
		{ThreadID: "worker", ParentThreadID: "root", IsRunning: true},
	}
	model.backgroundThreadEvents["worker"] = []protocol.ThreadEvent{{Type: "turn.started", ThreadID: "worker"}}
	model.setAgentPickerThreadVisibility("worker", true)
	entry, ok := model.agentEntry("worker")
	if !ok || !entry.Archived || entry.IsRunning {
		t.Fatalf("archived worker entry = %#v", entry)
	}
	if events := model.backgroundThreadEvents["worker"]; len(events) != 0 {
		t.Fatalf("archive kept live replay events: %#v", events)
	}
	model.applyThreadScopedEvent(ThreadScopedEventMsg{
		ThreadID: "worker",
		Event:    protocol.ThreadEvent{Type: "item.started", ThreadID: "worker"},
	})
	if events := model.backgroundThreadEvents["worker"]; len(events) != 0 {
		t.Fatalf("archived thread buffered a late notification: %#v", events)
	}
}
