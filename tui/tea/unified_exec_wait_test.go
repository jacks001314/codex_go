package tea

import (
	"strings"
	"testing"

	"codex_go/protocol"
	codextui "codex_go/tui"
	"codex_go/utils"
)

func unifiedExecStartupItem(id string, command string, processID string) protocol.ThreadItem {
	item := protocol.CommandExecutionItem(id, command, "", nil, "in_progress")
	item.CallID = id
	item.Metadata = map[string]any{"source": "unifiedExecStartup"}
	if processID != "" {
		item.Metadata["processId"] = processID
	}
	return item
}

// TestModelTerminalInteractionOwnsStatusWhileWaiting covers Rust #43921: an
// empty-stdin poll for a tracked background terminal takes the status row, the
// reasoning heading cannot move while it waits, and typing into the process
// flushes the streak and renders the interaction.
func TestModelTerminalInteractionOwnsStatusWhileWaiting(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetStatus("running")
	model := NewModel(state, Options{Width: 80, Height: 24})

	model.Update(ThreadEventMsg{Event: protocol.ItemStarted(unifiedExecStartupItem("call-startup", `bash -lc "python server.py"`, "proc-1"))})
	if len(model.commandLifecycle.UnifiedExecProcesses) != 1 {
		t.Fatalf("tracked processes = %#v", model.commandLifecycle.UnifiedExecProcesses)
	}
	model.Update(TerminalInteractionMsg{ProcessID: "proc-1"})
	if wait := model.commandLifecycle.UnifiedExecWait; wait == nil || wait.ProcessID != "proc-1" {
		t.Fatalf("wait streak = %#v", wait)
	}
	if !strings.EqualFold(strings.TrimSpace(model.workingStatusHeader), "Waiting for background terminal") {
		t.Fatalf("status header = %q", model.workingStatusHeader)
	}
	view := utils.StripANSI(model.View())
	if !strings.Contains(view, "Waiting for background terminal") || !strings.Contains(view, "python server.py") {
		t.Fatalf("waiting status missing:\n%s", view)
	}

	// Reasoning deltas must not replace the waiting status.
	model.Update(ThreadEventMsg{Event: protocol.ReasoningSummaryDelta("reasoning-1", "## Thinking hard\n")})
	if !strings.EqualFold(strings.TrimSpace(model.workingStatusHeader), "Waiting for background terminal") {
		t.Fatalf("reasoning moved the waiting heading: %q", model.workingStatusHeader)
	}

	// A typed stdin flushes the streak and records the interaction.
	model.Update(TerminalInteractionMsg{ProcessID: "proc-1", Stdin: "print(1)\n"})
	if model.commandLifecycle.UnifiedExecWait != nil {
		t.Fatalf("wait streak survived typed stdin: %#v", model.commandLifecycle.UnifiedExecWait)
	}
	if strings.EqualFold(strings.TrimSpace(model.workingStatusHeader), "Waiting for background terminal") {
		t.Fatal("waiting status survived the flush")
	}
	view = utils.StripANSI(model.View())
	if !strings.Contains(view, "print(1)") {
		t.Fatalf("terminal interaction missing from the transcript:\n%s", view)
	}
}

// TestModelTerminalInteractionRequiresATrackedProcess covers Rust's gate: an
// empty-stdin poll with no tracked command display is ignored.
func TestModelTerminalInteractionRequiresATrackedProcess(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetStatus("running")
	model := NewModel(state, Options{Width: 80, Height: 24})
	model.Update(TerminalInteractionMsg{ProcessID: "proc-unknown"})
	if model.commandLifecycle.UnifiedExecWait != nil {
		t.Fatalf("untracked process started a wait streak: %#v", model.commandLifecycle.UnifiedExecWait)
	}
	if strings.TrimSpace(model.workingStatusHeader) != "" {
		t.Fatalf("unknown process changed the header: %q", model.workingStatusHeader)
	}
}

// TestModelUnifiedExecWaitFlushesOnCompletion covers Rust
// on_command_execution_completed: completing the waiting process releases the
// status row.
func TestModelUnifiedExecWaitFlushesOnCompletion(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetStatus("running")
	model := NewModel(state, Options{Width: 80, Height: 24})
	model.Update(ThreadEventMsg{Event: protocol.ItemStarted(unifiedExecStartupItem("call-startup", "go test ./...", "proc-1"))})
	model.Update(TerminalInteractionMsg{ProcessID: "proc-1"})
	if model.commandLifecycle.UnifiedExecWait == nil {
		t.Fatal("wait streak was not started")
	}
	completed := protocol.CommandExecutionItem("call-startup", "go test ./...", "ok", intPtrTea(0), "completed")
	completed.CallID = "call-startup"
	completed.Metadata = map[string]any{"source": "unifiedExecStartup", "processId": "proc-1"}
	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(completed)})
	if model.commandLifecycle.UnifiedExecWait != nil {
		t.Fatalf("wait streak survived process completion: %#v", model.commandLifecycle.UnifiedExecWait)
	}
	if len(model.commandLifecycle.UnifiedExecProcesses) != 0 {
		t.Fatalf("processes after completion = %#v", model.commandLifecycle.UnifiedExecProcesses)
	}
}

func intPtrTea(value int) *int { return &value }
