package tea

import (
	"testing"

	bubbletea "charm.land/bubbletea/v2"

	codextui "codex_go/tui"
	keymsg "codex_go/tui/keymsg"
)

// Mirrors Rust #60969: a lost Cloud reply stream holds submitted input in the
// composer queue, and the quiet reopen releases exactly that prompt.
func TestCloudStreamDisconnectedQueuesInputUntilReconnectLikeRust(t *testing.T) {
	submitted := []SubmitRequest{}
	state := codextui.NewState(nil)
	state.SetThreadID("cloud-thread")
	model := NewModel(state, Options{
		Width:  100,
		Height: 30,
		OnSubmitRequest: func(request SubmitRequest) bubbletea.Cmd {
			submitted = append(submitted, request)
			return nil
		},
	})

	model.Update(CloudStreamDisconnectedMsg{ThreadID: "cloud-thread"})
	typeText(t, model, "held prompt")
	model.Update(key(keymsg.KeyEnter))
	if len(submitted) != 0 {
		t.Fatalf("submitted %#v while the Cloud stream was disconnected", submitted)
	}
	if len(model.queued) != 1 || model.queued[0].Request.Prompt != "held prompt" {
		t.Fatalf("queued = %#v, want the held prompt", model.queued)
	}

	model.Update(CloudStreamReconnectedMsg{ThreadID: "cloud-thread"})
	if len(submitted) != 1 || submitted[0].Prompt != "held prompt" {
		t.Fatalf("submitted after reconnect = %#v, want the held prompt", submitted)
	}
	if len(model.queued) != 0 {
		t.Fatalf("queued after reconnect = %#v, want none", model.queued)
	}
}

// A disconnect for another conversation must not hold the displayed thread.
func TestCloudStreamDisconnectedIgnoresOtherThreadsLikeRust(t *testing.T) {
	submitted := []SubmitRequest{}
	state := codextui.NewState(nil)
	state.SetThreadID("cloud-thread")
	model := NewModel(state, Options{
		Width:  100,
		Height: 30,
		OnSubmitRequest: func(request SubmitRequest) bubbletea.Cmd {
			submitted = append(submitted, request)
			return nil
		},
	})

	model.Update(CloudStreamDisconnectedMsg{ThreadID: "other-thread"})
	typeText(t, model, "send now")
	model.Update(key(keymsg.KeyEnter))
	if len(submitted) != 1 || submitted[0].Prompt != "send now" {
		t.Fatalf("submitted = %#v, want the prompt to reach the running thread", submitted)
	}
}
