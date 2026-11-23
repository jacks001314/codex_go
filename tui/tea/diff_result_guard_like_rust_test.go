package tea

import (
	"testing"

	codextui "codex_go/tui"
)

// Rust #63400: an asynchronous diff result only opens when the thread and the
// working directory still match the session that asked for it.
func TestDiffResultRequiresMatchingThreadAndCWDLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-a")
	model := NewModel(state, Options{
		Width:            60,
		Height:           10,
		SessionPickerCWD: `D:\repo`,
		OnReadGitDiff: func(string, string) (string, bool, error) {
			return "diff --git a/app.go b/app.go\n", true, nil
		},
	})

	cmd := model.applyDiffCommand()
	if cmd == nil {
		t.Fatal("applyDiffCommand returned no command")
	}
	msg, ok := cmd().(DiffResultMsg)
	if !ok {
		t.Fatalf("diff command message = %T", cmd())
	}
	if msg.ThreadID != "thread-a" || msg.CWD != `D:\repo` {
		t.Fatalf("diff result identity = %q/%q", msg.ThreadID, msg.CWD)
	}

	staleThread := msg
	staleThread.ThreadID = "thread-b"
	if next := model.applyDiffResult(staleThread); next != nil || model.overlay != nil {
		t.Fatal("a diff from another thread opened the pager")
	}

	staleCWD := msg
	staleCWD.CWD = `D:\other`
	if next := model.applyDiffResult(staleCWD); next != nil || model.overlay != nil {
		t.Fatal("a diff from another directory opened the pager")
	}

	model.applyDiffResult(msg)
	if model.overlay == nil {
		t.Fatal("the current thread's diff did not open the pager")
	}
}
