package app

import (
	"strings"
	"testing"

	"codex_go/session"
)

// Hidden reasoning between exploration commands stays attached to the restored
// group: the compact row keeps both commands and the expanded transcript shows
// the reasoning in place (Rust #46710 thread grouping).
func TestSessionMessagesKeepReasoningInExplorationGroupLikeRust(t *testing.T) {
	record := &session.Record{Items: []session.Item{
		{ID: "c1", Type: "command_execution", Status: "completed", Data: map[string]any{"command": "ls -la", "output": "a\n"}},
		{ID: "r1", Type: "reasoning", Data: map[string]any{"summary": []any{"thinking about files"}}},
		{ID: "c2", Type: "command_execution", Status: "completed", Data: map[string]any{"command": "ls src", "output": "b\n"}},
	}}
	messages := interactiveSessionMessagesFromRecord(record, reasoningProjectionChatWidget, false)
	if len(messages) != 1 {
		t.Fatalf("messages = %d, want 1 grouped row: %#v", len(messages), messages)
	}
	for _, want := range []string{"ls -la", "List src"} {
		if !strings.Contains(messages[0].Text, want) {
			t.Fatalf("group dropped %q:\n%s", want, messages[0].Text)
		}
	}
	if !strings.Contains(ansiSGR.ReplaceAllString(messages[0].TranscriptText, ""), "thinking about files") {
		t.Fatalf("group transcript dropped its reasoning:\n%s", messages[0].TranscriptText)
	}
}
