package tea

import (
	"strings"
	"testing"

	"codex_go/protocol"
	codextui "codex_go/tui"
)

// Rust #63444: a live collab row resolves the spawned subagent's nickname from
// the model's agent metadata instead of printing the raw thread id.
func TestCollabAgentRowResolvesNicknameLikeRust(t *testing.T) {
	model := NewModel(nil, Options{Width: 80, Height: 24})
	model.agentItems = []codextui.AgentThreadEntry{
		{ThreadID: "/root/worker", AgentNickname: "Scout", AgentRole: "worker"},
	}
	receivers := []string{"/root/worker"}
	item := protocol.CollabToolCallItem("spawn", "spawn_agent", "/root", receivers, nil, nil, "completed")

	model.renderCollabAgentToolCall(&item, true)
	if got := model.collabAgentNickname("/root/worker"); got != "Scout" {
		t.Fatalf("resolved nickname = %q", got)
	}
	if got := model.collabAgentNickname("/root/unknown"); got != "" {
		t.Fatalf("unknown thread resolved to %q", got)
	}

	var rendered string
	for _, message := range model.State.Messages {
		rendered += message.Text + "\n"
	}
	if !strings.Contains(rendered, "Spawned Scout") {
		t.Fatalf("transcript = %q", rendered)
	}
	if strings.Contains(rendered, "Spawned /root/worker") {
		t.Fatalf("transcript kept the raw thread id: %q", rendered)
	}
}
