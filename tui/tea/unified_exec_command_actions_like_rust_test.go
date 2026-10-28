package tea

import (
	"strings"
	"testing"

	"codex_go/protocol"
)

// TestUnifiedExecUnknownActionsMatchRustCommandCell covers the live and
// completed rendering of an exec_command whose shell alias the executor parsed
// as Unknown. Rust keeps the command out of the transcript while it runs (the
// status row owns the progress) and then renders "Ran <cmd>"; Go must not fall
// back to re-parsing the raw text and paint an "Exploring / List" cell.
func TestUnifiedExecUnknownActionsMatchRustCommandCell(t *testing.T) {
	model := exploringModel()
	unknown := []protocol.CommandAction{{Type: "unknown", Command: "ls"}}

	started := protocol.CommandExecutionItem("call-1", "ls", "", nil, "in_progress")
	started.Metadata = map[string]any{"source": "unifiedExecStartup"}
	started.CommandActions = &unknown
	model.applyItemStarted(&started, 0)

	view := model.View()
	if strings.Contains(view, "Exploring") || strings.Contains(view, "List ls") {
		t.Fatalf("unified-exec unknown command painted a live exploring cell:\n%s", view)
	}

	exitCode := 0
	completed := protocol.CommandExecutionItem("call-1", "ls", "", &exitCode, "completed")
	completed.Metadata = map[string]any{"source": "unifiedExecStartup"}
	completed.CommandActions = &unknown
	model.applyItemCompleted(&completed)

	view = model.View()
	if strings.Contains(view, "Explored") || strings.Contains(view, "List ls") {
		t.Fatalf("completed unified-exec command stayed an exploring cell:\n%s", view)
	}
	if !strings.Contains(view, "Ran ls") {
		t.Fatalf("completed unified-exec command should render Rust's Ran cell:\n%s", view)
	}
}
