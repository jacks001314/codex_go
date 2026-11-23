package tea

import (
	"strings"
	"testing"

	"codex_go/protocol"
	codextui "codex_go/tui"
)

// Rust #63444: a spawn row painted before the nickname was known is relabelled
// once the agent metadata arrives, a row whose nickname stays unknown keeps the
// thread id it was drawn with, and a subagent that closes during the lookup
// falls back to the thread id again.
func TestCollabTranscriptRowsRelabelOnLateMetadataLikeRust(t *testing.T) {
	model := NewModel(nil, Options{Width: 80, Height: 24})
	receivers := []string{"/root/worker", "/root/other"}
	spawn := protocol.CollabToolCallItem("spawn", "spawn_agent", "/root", receivers[:1], nil, nil, "completed")
	unknown := protocol.CollabToolCallItem("spawn-2", "spawn_agent", "/root", receivers[1:], nil, nil, "completed")
	model.renderCollabAgentToolCall(&spawn, true)
	model.renderCollabAgentToolCall(&unknown, true)

	if text := collabTranscriptText(model); strings.Contains(text, "Spawned Scout") {
		t.Fatalf("nickname appeared before the metadata arrived: %q", text)
	}

	// The picker refresh delivers the nickname for one subagent only.
	model.applyAgentListResult(AgentListResultMsg{
		CurrentThreadID: "",
		Entries: []codextui.AgentThreadEntry{
			{ThreadID: "/root/worker", AgentNickname: "Scout"},
		},
	})

	text := collabTranscriptText(model)
	if !strings.Contains(text, "Spawned Scout") {
		t.Fatalf("late nickname did not relabel the row: %q", text)
	}
	if strings.Contains(text, "Spawned /root/worker") {
		t.Fatalf("relabelled row kept the thread id: %q", text)
	}
	if !strings.Contains(text, "Spawned /root/other") {
		t.Fatalf("row without metadata lost its thread id: %q", text)
	}

	// The subagent closes while the row is on screen: the archived entry no
	// longer carries a nickname, so the row falls back to the thread id.
	model.applyAgentListResult(AgentListResultMsg{
		CurrentThreadID: "",
		Entries: []codextui.AgentThreadEntry{
			{ThreadID: "/root/worker", Archived: true},
		},
	})
	text = collabTranscriptText(model)
	if strings.Contains(text, "Spawned Scout") {
		t.Fatalf("closed subagent kept its nickname: %q", text)
	}
	if !strings.Contains(text, "Spawned /root/worker") {
		t.Fatalf("closed subagent did not fall back to the thread id: %q", text)
	}
}

func collabTranscriptText(model *Model) string {
	if model == nil || model.State == nil {
		return ""
	}
	var builder strings.Builder
	for _, message := range model.State.Messages {
		builder.WriteString(message.Text)
		builder.WriteString("\n")
	}
	return builder.String()
}