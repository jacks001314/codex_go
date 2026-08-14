package tea

import (
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

func TestModelCtrlCOpensRunningTaskExitMenuInDaemonSession(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetStatus("running")
	model := NewModel(state, Options{LocalDaemonSession: true, OnInterrupt: func() bubbletea.Cmd { return nil }})

	_, cmd := model.Update(key(bubbletea.KeyCtrlC))
	if cmd != nil {
		t.Fatalf("Ctrl-C during running task in daemon session should open a menu, got cmd %#v", cmd)
	}
	if model.modal == nil || model.modal.kind != ModalKindRunningTaskExit {
		t.Fatalf("modal = %#v, want running task exit menu", model.modal)
	}
	if len(model.modal.options) != 3 {
		t.Fatalf("running task exit options = %#v, want 3", model.modal.options)
	}
}

func TestModelCtrlCKeepsInterruptWithoutDaemonSession(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetStatus("running")
	interrupted := 0
	model := NewModel(state, Options{OnInterrupt: func() bubbletea.Cmd {
		interrupted++
		return nil
	}})

	_, _ = model.Update(key(bubbletea.KeyCtrlC))
	if model.modal != nil {
		t.Fatalf("modal = %#v, want direct interrupt outside daemon session", model.modal)
	}
	if interrupted != 1 {
		t.Fatalf("interrupt calls = %d, want 1", interrupted)
	}
}

func TestModelApplyRunningTaskExit(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetStatus("running")
	interrupted := 0
	model := NewModel(state, Options{OnInterrupt: func() bubbletea.Cmd {
		interrupted++
		return func() bubbletea.Msg { return TurnInterruptedMsg{} }
	}})

	if cmd := model.applyRunningTaskExit(runningTaskExitCancel); cmd == nil {
		t.Fatal("cancel returned nil command")
	}
	if interrupted != 1 {
		t.Fatalf("cancel interrupted %d times, want 1", interrupted)
	}

	if cmd := model.applyRunningTaskExit(runningTaskExitBackground); cmd == nil {
		t.Fatal("run in background returned nil command")
	}
	// run in background should not interrupt
	if interrupted != 1 {
		t.Fatalf("background exit interrupted %d times, want 1 (from cancel)", interrupted)
	}

	if cmd := model.applyRunningTaskExit(runningTaskExitStopAndQuit); cmd == nil {
		t.Fatal("exit returned nil command")
	}
	if interrupted != 2 {
		t.Fatalf("exit interrupted %d times, want 2", interrupted)
	}
}
