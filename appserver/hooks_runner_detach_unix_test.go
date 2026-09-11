//go:build unix

package appserver

import (
	"context"
	"testing"
)

// Mirrors Rust #43876: a hook command runs in its own session, so shell startup
// code that touches the controlling terminal cannot stop it on background
// terminal I/O. A session leader's session ID equals its own PID.
func TestHookCommandDetachesFromControllingTerminalLikeRust(t *testing.T) {
	runner := NewHookRunner()
	hook := hookRunnerMetadata("detach", HookEventUserPromptSubmit, "", 0)
	command := `[ "$(ps -o sid= -p $$ | tr -d ' ')" = "$$" ] && printf detached || printf same-session`
	hook.Command = &command

	result, err := runner.RunUserPromptSubmit(context.Background(), &HookUserPromptSubmitRequest{
		ThreadID:       "thread-1",
		TurnID:         "turn-1",
		CWD:            t.TempDir(),
		Model:          "gpt-test",
		PermissionMode: "default",
		Prompt:         "hello",
		Hooks:          []HookMetadata{hook},
	})
	if err != nil {
		t.Fatalf("RunUserPromptSubmit() error = %v", err)
	}
	if len(result.Runs) != 1 || result.Runs[0].Status != HookRunCompleted {
		t.Fatalf("result = %+v", result)
	}
	if !hookEntriesContain(result.Runs[0].Entries, HookOutputContext, "detached") {
		t.Fatalf("hook did not detach from the controlling terminal session: %+v", result.Runs[0].Entries)
	}
}
