package tea

import (
	"errors"
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	"codex_go/utils"
)

// runForkCmd drains the deferred fork command the way bubbletea does: the RPC
// runs off the update loop and its result arrives as a message.
func runForkCmd(t *testing.T, model *Model, cmd bubbletea.Cmd) *Model {
	t.Helper()
	if cmd == nil {
		t.Fatal("fork scheduled no command")
	}
	msg := cmd()
	if msg == nil {
		t.Fatal("fork command returned no message")
	}
	return drainForkMsg(t, model, msg)
}

// drainForkMsg applies the deferred message and any follow-up command it
// schedules (the picker wraps the fork in ExitAltScreen + request).
func drainForkMsg(t *testing.T, model *Model, msg bubbletea.Msg) *Model {
	t.Helper()
	if batch, ok := msg.(bubbletea.BatchMsg); ok {
		for _, sub := range batch {
			if sub == nil {
				continue
			}
			if subMsg := sub(); subMsg != nil {
				model = drainForkMsg(t, model, subMsg)
			}
		}
		return model
	}
	updated, next := model.Update(msg)
	model = updated.(*Model)
	if next != nil {
		if nextMsg := next(); nextMsg != nil {
			model = drainForkMsg(t, model, nextMsg)
		}
	}
	return model
}

// Rust #47185 spawns thread/fork and repaints while it is pending; every
// interactive Go fork surface must do the same, not block the key handler.
// `/fork` goes through the checkout picker into applyForkCurrentSession.
func TestSlashForkRunsOffUpdateLoopLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-source")
	var actions []codextui.SessionSelection
	model := NewModel(state, Options{
		Width:  120,
		Height: 40,
		OnSessionAction: func(selection codextui.SessionSelection) (*codextui.SessionSummary, error) {
			actions = append(actions, selection)
			return &codextui.SessionSummary{ThreadID: "thread-forked", Title: "Forked"}, nil
		},
	})

	typeText(t, model, "/fork")
	updated, cmd := model.Update(key(bubbletea.KeyEnter))
	model = updated.(*Model)
	if len(actions) != 0 {
		t.Fatalf("thread/fork ran inside the key handler: %#v", actions)
	}
	if !model.forkInProgress {
		t.Fatal("/fork did not mark the fork as pending")
	}
	if got := model.notice; got != forkInProgressNotice {
		t.Fatalf("notice = %q, want %q", got, forkInProgressNotice)
	}
	if view := utils.StripANSI(model.View()); !strings.Contains(view, "Forking conversation") {
		t.Fatalf("progress line missing from the first frame:\n%s", view)
	}

	model = runForkCmd(t, model, cmd)
	if len(actions) != 1 || actions[0].Kind != codextui.SessionSelectionFork ||
		actions[0].Target.ThreadID != "thread-source" {
		t.Fatalf("fork actions = %#v, want one fork of thread-source", actions)
	}
	if got := model.State.ThreadID; got != "thread-forked" {
		t.Fatalf("thread after fork = %q, want thread-forked", got)
	}
	if model.forkInProgress {
		t.Fatal("pending flag survived the fork result")
	}
	if got := model.notice; !strings.Contains(got, "thread-forked") {
		t.Fatalf("notice = %q, want the forked session reported", got)
	}
}

// A pending fork owns the keyboard: a second shortcut must not issue a second
// thread/fork (the server would create two orphan forks).
func TestPendingForkSwallowsRepeatTriggerLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-source")
	var actions []codextui.SessionSelection
	model := NewModel(state, Options{
		Width:  120,
		Height: 40,
		OnSessionAction: func(selection codextui.SessionSelection) (*codextui.SessionSummary, error) {
			actions = append(actions, selection)
			return &codextui.SessionSummary{ThreadID: "thread-forked"}, nil
		},
	})
	typeText(t, model, "/fork")
	updated, cmd := model.Update(key(bubbletea.KeyEnter))
	model = updated.(*Model)
	// Repeat triggers while the request is in flight: `/fork` typed again plus a
	// direct call into the same entry point (the command center/read-only view
	// path). Both must be refused.
	for _, r := range "fork" {
		model.Update(keyRunes(r))
	}
	if again := model.applyForkCurrentSession(""); again != nil {
		t.Fatal("a pending fork accepted a second request")
	}
	model = runForkCmd(t, model, cmd)
	if len(actions) != 1 {
		t.Fatalf("thread/fork calls = %d, want 1", len(actions))
	}
}

// The session picker hands its fork row to the same async path.
func TestSessionPickerForkRunsOffUpdateLoopLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-current")
	var actions []codextui.SessionSelection
	var responses []ModalResponse
	model := NewModel(state, Options{
		Width:  120,
		Height: 40,
		SessionPickerItems: []codextui.SessionSummary{
			{ThreadID: "thread-source", Title: "Source"},
		},
		OnSessionAction: func(selection codextui.SessionSelection) (*codextui.SessionSummary, error) {
			actions = append(actions, selection)
			return &codextui.SessionSummary{ThreadID: "thread-forked"}, nil
		},
		OnModalResponse: func(response ModalResponse) bubbletea.Cmd {
			responses = append(responses, response)
			return nil
		},
	})
	model.openSessionPicker(codextui.SessionPickerFork)
	if model.modal == nil || model.modal.sessionPicker == nil {
		t.Fatal("session picker did not open")
	}
	// Keep the picker in the inline (non-alt-screen) mode so the returned Cmd is
	// the fork request itself; the alt-screen wrapper is exercised by the PTY
	// scenario in scripts/fakeappserver.
	model.sessionPickerAltScreen = false
	updated, cmd := model.Update(key(bubbletea.KeyEnter))
	model = updated.(*Model)
	if len(actions) != 0 {
		t.Fatalf("thread/fork ran inside the picker key handler: %#v", actions)
	}
	if model.modal != nil {
		t.Fatal("picker stayed open over a pending fork")
	}
	if !model.forkInProgress || model.notice != forkInProgressNotice {
		t.Fatalf("pending=%v notice=%q, want the progress notice", model.forkInProgress, model.notice)
	}
	model = runForkCmd(t, model, cmd)
	if len(actions) != 1 || actions[0].Target.ThreadID != "thread-source" {
		t.Fatalf("fork actions = %#v, want one fork of thread-source", actions)
	}
	if state.ThreadID != "thread-forked" {
		t.Fatalf("thread = %q, want thread-forked", state.ThreadID)
	}
	if len(responses) != 1 || responses[0].ID != "session-picker-fork" || responses[0].Picker == nil {
		t.Fatalf("modal responses = %#v", responses)
	}
}

// A failing unlocked fork reports the error, clears its progress state and
// leaves the active thread where it was.
func TestUnlockedForkFailureKeepsCurrentThreadLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-source")
	model := NewModel(state, Options{
		Width:  120,
		Height: 40,
		OnSessionAction: func(codextui.SessionSelection) (*codextui.SessionSummary, error) {
			return nil, errors.New("simulated backend error")
		},
	})
	typeText(t, model, "/fork")
	updated, cmd := model.Update(key(bubbletea.KeyEnter))
	model = updated.(*Model)
	model = runForkCmd(t, model, cmd)
	if model.forkInProgress {
		t.Fatal("failed fork left the pending flag set")
	}
	if state.ThreadID != "thread-source" {
		t.Fatalf("thread = %q, want the source thread", state.ThreadID)
	}
	if !strings.Contains(model.notice, "simulated backend error") {
		t.Fatalf("notice = %q, want the backend error", model.notice)
	}
}

// A pending fork owns the keyboard, but an explicit exit must not be swallowed:
// a backend that never answers would otherwise trap the process.
func TestPendingForkStillHonoursExplicitQuitLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-source")
	model := NewModel(state, Options{
		Width:  120,
		Height: 40,
		OnSessionAction: func(codextui.SessionSelection) (*codextui.SessionSummary, error) {
			return &codextui.SessionSummary{ThreadID: "thread-forked"}, nil
		},
	})
	typeText(t, model, "/fork")
	updated, cmd := model.Update(key(bubbletea.KeyEnter))
	model = updated.(*Model)
	if !model.forkInProgress {
		t.Fatal("fork did not become pending")
	}
	_, quitCmd := model.Update(key(bubbletea.KeyCtrlC))
	if quitCmd == nil {
		t.Fatal("ctrl+c was swallowed by the pending fork")
	}
	if msg := quitCmd(); msg == nil {
		t.Fatal("ctrl+c produced no message")
	} else if _, ok := msg.(bubbletea.QuitMsg); !ok {
		t.Fatalf("ctrl+c produced %T, want bubbletea.QuitMsg", msg)
	}
	// The fork request itself is still the deferred command; quitting does not
	// cancel it (the server may already have created the thread).
	if cmd == nil {
		t.Fatal("pending fork lost its command")
	}
}
