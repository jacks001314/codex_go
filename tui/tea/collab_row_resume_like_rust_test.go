package tea

import (
	"strings"
	"testing"

	"codex_go/protocol"
	codextui "codex_go/tui"
)

// Rust #63444: a spawn row replayed from persisted history is relabelled when
// the subagent metadata arrives after the resume, like a live row.
func TestResumedCollabRowsRelabelOnMetadataLikeRust(t *testing.T) {
	item := protocol.CollabToolCallItem("spawn", "spawn_agent", "/root", []string{"/root/worker"}, nil, nil, "completed")
	messages, ok := NewResumeTranscriptBuilder("/root", 80).Project(&item)
	if !ok || len(messages) == 0 {
		t.Fatalf("resume projection did not own the item: ok=%v messages=%d", ok, len(messages))
	}
	if messages[len(messages)-1].CollabToolCall == nil {
		t.Fatalf("resume projection dropped the collab item: %#v", messages[len(messages)-1])
	}

	model := NewModel(nil, Options{Width: 80, Height: 24})
	model.applyResumeResponseWithCWD("thread-root", SessionResumeResponse{Messages: messages}, "/root")
	if text := collabTranscriptText(model); !strings.Contains(text, "Spawned /root/worker") {
		t.Fatalf("replayed row lost its thread id: %q", text)
	}

	model.applyAgentListResult(AgentListResultMsg{
		CurrentThreadID: "thread-root",
		Entries: []codextui.AgentThreadEntry{
			{ThreadID: "/root/worker", AgentNickname: "Scout"},
		},
	})

	text := collabTranscriptText(model)
	if !strings.Contains(text, "Spawned Scout") {
		t.Fatalf("metadata arriving after resume did not relabel the row: %q", text)
	}
	if strings.Contains(text, "Spawned /root/worker") {
		t.Fatalf("replayed row kept the thread id: %q", text)
	}
}

// Rust #63444: a page of older history spliced above the window registers its
// collab rows too, so scrolling back does not leave stale thread ids.
func TestOlderHistoryCollabRowsRelabelLikeRust(t *testing.T) {
	item := protocol.CollabToolCallItem("spawn", "spawn_agent", "/root", []string{"/root/worker"}, nil, nil, "completed")
	messages, ok := NewResumeTranscriptBuilder("/root", 80).Project(&item)
	if !ok || len(messages) == 0 {
		t.Fatalf("resume projection did not own the item: ok=%v messages=%d", ok, len(messages))
	}

	model := NewModel(nil, Options{Width: 80, Height: 24})
	model.switchThreadID("thread-root")
	model.applyOlderHistoryPage("thread-root", OlderHistoryPage{Messages: messages}, nil)
	if text := collabTranscriptText(model); !strings.Contains(text, "Spawned /root/worker") {
		t.Fatalf("older page lost its thread id: %q", text)
	}

	model.applyAgentListResult(AgentListResultMsg{
		CurrentThreadID: "thread-root",
		Entries: []codextui.AgentThreadEntry{
			{ThreadID: "/root/worker", AgentNickname: "Scout"},
		},
	})
	if text := collabTranscriptText(model); !strings.Contains(text, "Spawned Scout") {
		t.Fatalf("older page row was not relabelled: %q", text)
	}
}

// Rust #63444 refreshes parked panes too: a row captured in a retained side
// conversation still gains its nickname when the metadata lands.
func TestParkedSideTranscriptCollabRowsRelabelLikeRust(t *testing.T) {
	item := protocol.CollabToolCallItem("spawn", "spawn_agent", "/root", []string{"/root/worker"}, nil, nil, "completed")
	messages, ok := NewResumeTranscriptBuilder("/root", 80).Project(&item)
	if !ok || len(messages) == 0 {
		t.Fatalf("resume projection did not own the item: ok=%v messages=%d", ok, len(messages))
	}
	parked := append([]codextui.Message(nil), messages...)

	model := NewModel(nil, Options{Width: 80, Height: 24})
	model.sideConversations = map[string]*activeSideConversation{
		"/root": {ParentThreadID: "/root", SideThreadID: "/root-side", ParentMessages: parked},
	}
	model.registerCollabTranscriptRows(parked)

	model.applyAgentListResult(AgentListResultMsg{
		Entries: []codextui.AgentThreadEntry{
			{ThreadID: "/root/worker", AgentNickname: "Scout"},
		},
	})

	if !strings.Contains(parked[0].Text, "Spawned Scout") {
		t.Fatalf("parked pane row was not relabelled: %q", parked[0].Text)
	}
}
