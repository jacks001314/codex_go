package tea

import (
	"errors"
	"strings"
	"testing"

	bubbletea "charm.land/bubbletea/v2"

	chatwidget "codex_go/tui/chatwidget"
	"codex_go/tui/keymsg"
)

// Rust input_submission.rs: a `!` command in shell mode runs through the host
// instead of becoming a model prompt. The submission stays pending until the
// command's turn/started receipt arrives.
func TestUserShellCommandSubmitsThroughHostLikeRust(t *testing.T) {
	model := prefixTestModel(t)
	model.State.SetThreadID("thread-shell")
	calls := 0
	gotThread, gotCommand := "", ""
	model.onUserShellCommand = func(threadID string, command string) bubbletea.Cmd {
		calls++
		gotThread = threadID
		gotCommand = command
		return nil
	}

	model = typeComposerRunes(t, model, "!echo hi")
	updated, _ := model.Update(keymsg.KeyMsg{Type: keymsg.KeyEnter})
	model = updated.(*Model)

	if calls != 1 || gotThread != "thread-shell" || gotCommand != "echo hi" {
		t.Fatalf("host call = (%d, %q, %q), want one thread-shell/echo hi", calls, gotThread, gotCommand)
	}
	if requests := model.SubmittedRequests(); len(requests) != 0 {
		t.Fatalf("shell command became model prompts: %#v", requests)
	}
	if !model.userTurnPendingStart {
		t.Fatal("shell submission did not stay pending for its turn receipt")
	}
	if model.State.Status != "running" {
		t.Fatalf("status = %q, want running", model.State.Status)
	}
	if len(model.inputHistory) == 0 || model.inputHistory[len(model.inputHistory)-1] != "!echo hi" {
		t.Fatalf("prompt history = %#v", model.inputHistory)
	}
	if len(model.inputHistoryBash) == 0 || !model.inputHistoryBash[len(model.inputHistoryBash)-1] {
		t.Fatalf("shell mode not recorded in history: %#v", model.inputHistoryBash)
	}

	message := "Shell commands starting with ! are unavailable in Cloud conversations."
	model.Update(UserShellCommandResultMsg{ThreadID: "thread-shell", Command: "echo hi", Err: errors.New(message)})
	if model.userTurnPendingStart {
		t.Fatal("rejected shell command kept the turn pending")
	}
	if model.State.Status != "idle" {
		t.Fatalf("status after rejection = %q, want idle", model.State.Status)
	}
	if !strings.Contains(model.transcriptContent(), message) {
		t.Fatalf("rejection message missing: %q", model.transcriptContent())
	}
}

// An empty `!` shows the shell-command help instead of running anything.
func TestEmptyUserShellCommandShowsHelpLikeRust(t *testing.T) {
	model := prefixTestModel(t)
	model.State.SetThreadID("thread-shell")
	called := false
	model.onUserShellCommand = func(string, string) bubbletea.Cmd {
		called = true
		return nil
	}

	model = typeComposerRunes(t, model, "!")
	updated, _ := model.Update(keymsg.KeyMsg{Type: keymsg.KeyEnter})
	model = updated.(*Model)

	if called {
		t.Fatal("empty shell command reached the host")
	}
	if !strings.Contains(model.transcriptContent(), chatwidget.UserShellCommandHelpTitle) {
		t.Fatalf("help title missing: %q", model.transcriptContent())
	}
	if !strings.Contains(model.transcriptContent(), chatwidget.UserShellCommandHelpHint) {
		t.Fatalf("help hint missing: %q", model.transcriptContent())
	}
}

// A `!` command uses the shell path even when the key normally steers the
// running turn (Rust input_submission.rs checks the shell escape before the
// submission source decides steer versus turn).
func TestShellCommandUsesShellPathInsteadOfSteerLikeRust(t *testing.T) {
	model := prefixTestModel(t)
	model.State.SetThreadID("thread-shell")
	steered := false
	model.onSteerRequest = func(SubmitRequest, string) error {
		steered = true
		return nil
	}
	gotCommand := ""
	model.onUserShellCommand = func(_ string, command string) bubbletea.Cmd {
		gotCommand = command
		return nil
	}

	model = typeComposerRunes(t, model, "!ls")
	model.steerComposer()

	if steered {
		t.Fatal("shell command was sent as a steer")
	}
	if gotCommand != "ls" {
		t.Fatalf("shell command = %q, want ls", gotCommand)
	}
}
