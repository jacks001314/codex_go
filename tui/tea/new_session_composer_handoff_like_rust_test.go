package tea

import (
	"strings"
	"testing"

	bubbletea "charm.land/bubbletea/v2"
	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
	"codex_go/utils"
)

// Rust #61383: while a new conversation is opening the composer stays editable,
// submission waits (so a second Enter never starts a competing turn), and a
// failed open hands the complete draft back so it can be submitted again.
func TestNewConversationComposerHandoffLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 120, Height: 40})
	var submitted []SubmitRequest
	model.onSubmitRequest = func(request SubmitRequest) bubbletea.Cmd {
		submitted = append(submitted, request)
		return func() bubbletea.Msg { return nil }
	}

	prompt := "first line\nsecond line"
	model.composer.SetValue(prompt)
	if _, cmd := model.Update(key(keymsg.KeyEnter)); cmd == nil {
		t.Fatal("opening submission returned no command")
	}
	if len(submitted) != 1 || submitted[0].Prompt != prompt || !model.userTurnPendingStart {
		t.Fatalf("opening submission = %#v pending=%v", submitted, model.userTurnPendingStart)
	}

	// The draft stays editable while the replacement thread starts, and Enter
	// queues instead of starting a second conversation.
	model.Update(keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune("typed while opening")})
	if got := model.composer.Value(); got != "typed while opening" {
		t.Fatalf("composer while opening = %q", got)
	}
	// A quick typed run opens the paste burst, as in Rust; the test presses Enter
	// after the window so it is an ordinary submit key.
	model.clearComposerPasteWindow()
	model.Update(key(keymsg.KeyEnter))
	if len(submitted) != 1 {
		t.Fatalf("submission during open started a turn: %#v", submitted)
	}
	if queued := model.QueuedRequests(); len(queued) != 1 || queued[0].Prompt != "typed while opening" {
		t.Fatalf("queued while opening = %#v", queued)
	}

	model.Update(NewSessionStartFailedMsg{
		ErrorMessage: "Failed to start a fresh session through the app server: boom",
		Request:      submitted[0],
	})
	if model.userTurnPendingStart {
		t.Fatal("failed open kept the pending start that no turn can clear")
	}
	if got := model.composer.Value(); got != prompt {
		t.Fatalf("restored draft = %q, want %q", got, prompt)
	}
	view := utils.StripANSI(model.viewString())
	if strings.Contains(view, "first line") && strings.Contains(view, "second line") && strings.Contains(view, "You") {
		t.Fatalf("optimistic prompt survived the failed open:\n%s", view)
	}
	if !strings.Contains(view, "Failed to start a fresh session through the app server: boom") {
		t.Fatalf("failure notice missing:\n%s", view)
	}

	// The restored multiline draft is a normal submission again.
	if _, cmd := model.Update(key(keymsg.KeyEnter)); cmd == nil {
		t.Fatal("resubmission after failure returned no command")
	}
	if len(submitted) != 2 || submitted[1].Prompt != prompt {
		t.Fatalf("resubmission = %#v", submitted)
	}
}
