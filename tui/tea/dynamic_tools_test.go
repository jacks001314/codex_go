package tea

import (
	"strings"
	"testing"

	"codex_go/appserver"
	"codex_go/protocol"
	codextui "codex_go/tui"
	agentsoverview "codex_go/tui/agents_overview"
)

func TestDynamicToolItemsUpdateOriginalTranscriptRows(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-main")
	model := NewModel(state, Options{Width: 100, Height: 24})
	success := true
	arguments := any(map[string]any{"task": "one"})

	model.applyItemStarted(&protocol.ThreadItem{
		ID: "call-1", CallID: "call-1", Type: "dynamic_tool_call",
		ToolName: "create_task", Arguments: &arguments, Status: "inProgress",
	}, 0)
	model.applyItemStarted(&protocol.ThreadItem{
		ID: "call-2", CallID: "call-2", Type: "dynamic_tool_call",
		ToolName: "list_tasks", Status: "inProgress",
	}, 0)
	model.applyItemCompleted(&protocol.ThreadItem{
		ID: "call-2", CallID: "call-2", Type: "dynamic_tool_call",
		ToolName: "list_tasks", Success: &success, Output: "two done",
	})
	model.applyItemCompleted(&protocol.ThreadItem{
		ID: "call-1", CallID: "call-1", Type: "dynamic_tool_call",
		ToolName: "create_task", Success: &success, Output: "one done",
	})

	if len(model.State.Messages) != 2 {
		t.Fatalf("dynamic messages = %#v", model.State.Messages)
	}
	if !strings.Contains(model.State.Messages[0].Text, "create_task") ||
		!strings.Contains(model.State.Messages[0].Text, "one done") {
		t.Fatalf("first row = %q", model.State.Messages[0].Text)
	}
	if !strings.Contains(model.State.Messages[1].Text, "list_tasks") ||
		!strings.Contains(model.State.Messages[1].Text, "two done") {
		t.Fatalf("second row = %q", model.State.Messages[1].Text)
	}
}

func TestDynamicToolThreadStartedTracksDispatchedTasks(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-main")
	model := NewModel(state, Options{Width: 80, Height: 24})

	model.Update(DynamicToolThreadStartedMsg{ThreadID: "thread-child", TaskToolsAvailable: true})

	if !model.IsDynamicToolThread("thread-child") {
		t.Fatalf("dispatched task not tracked: %#v", model.dynamicToolThreads)
	}
	if model.IsDynamicToolThread("thread-other") {
		t.Fatal("unrelated thread should not be tracked")
	}
	// A blank thread id is ignored.
	model.Update(DynamicToolThreadStartedMsg{ThreadID: "  "})
	if len(model.dynamicToolThreads) != 1 {
		t.Fatalf("dynamic tool threads = %#v", model.dynamicToolThreads)
	}
}

// Rust #46946: lifecycle metadata registers a task even when no thread/started
// notification or completed refresh is available.
func TestDynamicToolThreadMetadataRegistersFallbackRow(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 80, Height: 24})
	model.agentsOverview = agentsoverview.New(nil, "", false)
	name := "Created by tool"
	thread := &appserver.Thread{
		ID:     "thread-created",
		Name:   &name,
		Status: appserver.IdleStatus(),
	}
	model.Update(DynamicToolThreadStartedMsg{ThreadID: thread.ID, Thread: thread})
	if len(model.agentsOverview.Rows) != 1 {
		t.Fatalf("fallback rows = %#v", model.agentsOverview.Rows)
	}
	if row := model.agentsOverview.Rows[0]; row.ThreadID != thread.ID || row.Name != name || row.Group != agentsoverview.GroupReady {
		t.Fatalf("fallback row = %#v", row)
	}

	ephemeral := &appserver.Thread{ID: "thread-ephemeral", Ephemeral: true, Status: appserver.IdleStatus()}
	model.Update(DynamicToolThreadStartedMsg{ThreadID: ephemeral.ID, Thread: ephemeral})
	if len(model.agentsOverview.Rows) != 1 {
		t.Fatalf("ephemeral task was registered: %#v", model.agentsOverview.Rows)
	}
}

// A child lifecycle response folds into an already loaded root row.
func TestDynamicToolChildMetadataFoldsIntoRoot(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 80, Height: 24})
	model.agentsOverview = agentsoverview.New([]agentsoverview.Row{{ThreadID: "root"}}, "root", false)
	parent := "root"
	child := &appserver.Thread{ID: "child", ParentThreadID: &parent, Status: appserver.IdleStatus()}
	model.Update(DynamicToolThreadStartedMsg{ThreadID: child.ID, Thread: child})
	if len(model.agentsOverview.Rows) != 1 || len(model.agentsOverview.Rows[0].ChildThreadIDs) != 1 ||
		model.agentsOverview.Rows[0].ChildThreadIDs[0] != "child" {
		t.Fatalf("child metadata was not folded: %#v", model.agentsOverview.Rows)
	}
}

// Delayed tool responses cannot restore an archived/deleted row.
func TestDynamicToolThreadMetadataRespectsRemovedTombstone(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 80, Height: 24})
	model.agentsOverview = agentsoverview.New(nil, "", false)
	model.agentsOverviewRemoved = map[string]bool{"removed": true}
	thread := &appserver.Thread{ID: "removed", Status: appserver.IdleStatus()}
	model.Update(DynamicToolThreadStartedMsg{ThreadID: thread.ID, Thread: thread})
	if len(model.agentsOverview.Rows) != 0 {
		t.Fatalf("removed task restored by delayed metadata: %#v", model.agentsOverview.Rows)
	}
	model.markThreadStarted("removed")
	if model.agentsOverviewRemoved["removed"] {
		t.Fatal("thread start did not clear the removal tombstone")
	}
}
