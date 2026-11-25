package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
)

// Rust #40625: changing directories ignores closed agents, but any other open
// agent that is still running blocks the change with a distinct message.
func TestWorkingDirectoryChangeOtherAgentsLikeRust(t *testing.T) {
	destination := t.TempDir()
	model := workingDirectoryChangeModel(t, true, func(threadID string, cwd string) (*codextui.SessionSummary, error) {
		return &codextui.SessionSummary{ThreadID: "thread-b", CWD: cwd}, nil
	})
	model.agentItems = []codextui.AgentThreadEntry{
		{ThreadID: "thread-a"},
		{ThreadID: "idle-agent"},
		{ThreadID: "closed-agent", IsRunning: true, IsClosed: true},
	}
	if cmd := model.applyWorkingDirectoryChangeCommand(destination); cmd == nil {
		t.Fatal("closed or idle agents must not block a directory change")
	}
	model.workingDirectoryChangePending = false

	model.agentItems = append(model.agentItems, codextui.AgentThreadEntry{ThreadID: "running-agent", IsRunning: true})
	if cmd := model.applyWorkingDirectoryChangeCommand(destination); cmd != nil {
		t.Fatal("a running open agent must block a directory change")
	}
	if got := modelMessageText(model); !strings.Contains(got, "Cannot change: another agent is running.") {
		t.Fatalf("blocked message = %q", got)
	}
}
