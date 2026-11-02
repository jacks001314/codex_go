package chatwidget

import (
	"testing"

	codextui "codex_go/tui"
)

// Rust #58767: /fork defers draft consumption until dispatch validates the
// thread state; MCP startup alone does not block it and a rejection keeps the
// draft.
func TestForkDispatchValidationMatchesRust(t *testing.T) {
	mcpStartup := GuardSlashCommandDispatch(SlashCommandGuardContext{
		Command:     codextui.CommandFork,
		CommandName: "fork",
		TaskRunning: true,
	})
	if !mcpStartup.Allowed {
		t.Fatalf("fork during MCP startup = %#v", mcpStartup)
	}

	running := GuardSlashCommandDispatch(SlashCommandGuardContext{
		Command:          codextui.CommandFork,
		CommandName:      "fork",
		TaskRunning:      true,
		AgentTurnRunning: true,
	})
	if running.Allowed || !running.RequestRedraw || running.DrainPendingSubmission {
		t.Fatalf("fork during a user turn = %#v", running)
	}
	if running.ErrorMessage != "'/fork' is disabled while a task is in progress." {
		t.Fatalf("fork busy message = %q", running.ErrorMessage)
	}

	side := GuardSlashCommandDispatch(SlashCommandGuardContext{
		Command:                codextui.CommandFork,
		CommandName:            "fork",
		ActiveSideConversation: true,
	})
	if side.Allowed || side.DrainPendingSubmission {
		t.Fatalf("fork in a side conversation = %#v", side)
	}
}
