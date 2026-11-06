package tea

import (
	"strings"
	"testing"

	"codex_go/protocol"
	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
)

func backgroundTerminalTestModel(t *testing.T, loader ListBackgroundTerminalsFunc) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	state.SetThreadID("thread-fork")
	return NewModel(state, Options{
		Width:                     80,
		Height:                    18,
		OnListBackgroundTerminals: loader,
	})
}

// Mirrors Rust #59319: a resumed fork shows the terminals the app server
// reports for the displayed thread, excludes a terminal that exited while the
// list request was pending, and never restores an inherited parent command.
func TestModelRestoresBackgroundTerminalsFromDisplayedThreadLikeRust(t *testing.T) {
	loadedThreadID := ""
	model := backgroundTerminalTestModel(t, func(threadID string) ([]BackgroundTerminal, error) {
		loadedThreadID = threadID
		return []BackgroundTerminal{
			{ItemID: "call-own", ProcessID: "1001", Command: "go test ./..."},
			{ItemID: "call-exited", ProcessID: "1002", Command: "sleep 100"},
		}, nil
	})
	// An inherited parent command replayed from the fork's history is rendered
	// in the transcript but never tracked as a live terminal.
	model.noteUnifiedExecCommandStarted(&protocol.ThreadItem{
		Type:     "command_execution",
		CallID:   "call-parent",
		Command:  "cargo build",
		Status:   "in_progress",
		Metadata: map[string]any{"source": "unified_exec_startup"},
	})

	cmd := model.beginBackgroundTerminalRestore()
	if cmd == nil {
		t.Fatal("restore command is nil with a wired loader")
	}
	// The second terminal finishes while the request is in flight.
	model.noteUnifiedExecCommandCompleted(&protocol.ThreadItem{Type: "command_execution", CallID: "call-exited"})

	message, ok := cmd().(BackgroundTerminalsLoadedMsg)
	if !ok {
		t.Fatalf("restore command produced %T", cmd())
	}
	model.Update(message)
	if loadedThreadID != "thread-fork" {
		t.Fatalf("loaded terminals for %q, want the displayed thread", loadedThreadID)
	}

	details := model.backgroundTerminalDetails()
	if len(details) != 1 || details[0].CommandDisplay != "go test ./..." {
		t.Fatalf("restored terminals = %#v, want only the displayed thread's live terminal", details)
	}

	typeText(t, model, "/ps")
	model.Update(key(keymsg.KeyEnter))
	view := model.viewString()
	if !strings.Contains(view, "go test ./...") {
		t.Fatalf("/ps missing the restored terminal:\n%s", view)
	}
	if strings.Contains(view, "sleep 100") {
		t.Fatalf("/ps restored a terminal that exited during the request:\n%s", view)
	}
	if strings.Contains(view, "cargo build") {
		t.Fatalf("/ps restored an inherited parent command:\n%s", view)
	}
}

// Mirrors Rust #59319: resetting the thread invalidates an in-flight restore, so
// a late list cannot repopulate terminals for the thread that was left.
func TestBackgroundTerminalRestoreIgnoresSupersededRequestLikeRust(t *testing.T) {
	model := backgroundTerminalTestModel(t, func(threadID string) ([]BackgroundTerminal, error) {
		return []BackgroundTerminal{{ItemID: "call-own", ProcessID: "1001", Command: "go test ./..."}}, nil
	})
	cmd := model.beginBackgroundTerminalRestore()
	model.clearBackgroundTerminals()
	message := cmd().(BackgroundTerminalsLoadedMsg)
	model.Update(message)
	if got := model.commandLifecycle.UnifiedExecProcesses; len(got) != 0 {
		t.Fatalf("superseded restore repopulated terminals: %#v", got)
	}
}

// Mirrors Rust #59319: the restore is requested after a replay finishes.
func TestResumeRequestsBackgroundTerminalRestoreLikeRust(t *testing.T) {
	model := backgroundTerminalTestModel(t, func(threadID string) ([]BackgroundTerminal, error) {
		return nil, nil
	})
	model.applyResumeResponseWithCWD("thread-fork", SessionResumeResponse{
		Summary: &codextui.SessionSummary{ThreadID: "thread-fork"},
	}, "")
	if !model.backgroundTerminalRestoreRequested {
		t.Fatal("resume did not request a background terminal restore")
	}
	if cmd := model.consumeBackgroundTerminalRestore(); cmd == nil {
		t.Fatal("consumeBackgroundTerminalRestore() = nil after a resume")
	}
}
