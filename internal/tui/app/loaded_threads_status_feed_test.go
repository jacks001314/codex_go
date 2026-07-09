package app

import (
	"reflect"
	"strings"
	"testing"
)

func TestFindLoadedSubagentThreadsForPrimaryWalksSpawnTree(t *testing.T) {
	threads := []LoadedThread{
		{ID: "primary", Source: "cli"},
		{ID: "child-b", Source: LoadedThreadSourceSubAgentThreadSpawn, ParentThreadID: "primary", AgentNickname: "Scout", AgentRole: "explorer", AgentPath: "agents/scout.md"},
		{ID: "grandchild", Source: LoadedThreadSourceSubAgentThreadSpawn, ParentThreadID: "child-b", AgentNickname: "Atlas", AgentRole: "worker", AgentPath: "agents/atlas.md"},
		{ID: "child-a", Source: LoadedThreadSourceSubAgentThreadSpawn, ParentThreadID: "primary", AgentNickname: "Builder", AgentRole: "maker", AgentPath: "agents/builder.md"},
		{ID: "unrelated", Source: LoadedThreadSourceSubAgentThreadSpawn, ParentThreadID: "other"},
		{ID: "not-spawn", Source: "resume", ParentThreadID: "primary"},
	}

	got := FindLoadedSubagentThreadsForPrimary(threads, "primary")
	want := []LoadedSubagentThread{
		{ThreadID: "child-a", AgentNickname: "Builder", AgentRole: "maker", AgentPath: "agents/builder.md"},
		{ThreadID: "child-b", AgentNickname: "Scout", AgentRole: "explorer", AgentPath: "agents/scout.md"},
		{ThreadID: "grandchild", AgentNickname: "Atlas", AgentRole: "worker", AgentPath: "agents/atlas.md"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("loaded subagents = %#v, want %#v", got, want)
	}
}

func TestAgentActivitySummaryMatchesRustCases(t *testing.T) {
	tests := []struct {
		name string
		item AgentActivityItem
		want string
		ok   bool
	}{
		{name: "agent message", item: AgentActivityItem{Kind: AgentActivityAgentMessage, Text: " hello   world "}, want: "hello world", ok: true},
		{name: "reasoning", item: AgentActivityItem{Kind: AgentActivityReasoning, ReasoningSummaries: []string{"old", "new idea"}}, want: "new idea", ok: true},
		{name: "command", item: AgentActivityItem{Kind: AgentActivityCommandExecution, Command: "go test ./..."}, want: "$ go test ./...", ok: true},
		{name: "file change", item: AgentActivityItem{Kind: AgentActivityFileChange, Changes: 3}, want: "Updated 3 file(s)", ok: true},
		{name: "mcp", item: AgentActivityItem{Kind: AgentActivityMcpToolCall, Server: "docs", Tool: "search"}, want: "MCP docs/search", ok: true},
		{name: "dynamic", item: AgentActivityItem{Kind: AgentActivityDynamicToolCall, Namespace: "web", Tool: "open"}, want: "Tool web/open", ok: true},
		{name: "collab", item: AgentActivityItem{Kind: AgentActivityCollabToolCall, CollabTool: CollabToolSpawnAgent}, want: "Spawned an agent", ok: true},
		{name: "subagent", item: AgentActivityItem{Kind: AgentActivitySubAgentActivity, SubAgentActivity: SubAgentActivityInteracted, AgentPath: "agents/reviewer.md"}, want: "Contacted agents/reviewer.md", ok: true},
		{name: "web", item: AgentActivityItem{Kind: AgentActivityWebSearch, Query: "rust tui"}, want: "Web search: rust tui", ok: true},
		{name: "image", item: AgentActivityItem{Kind: AgentActivityImageView, Path: "diagram.png"}, want: "Viewed diagram.png", ok: true},
		{name: "generated", item: AgentActivityItem{Kind: AgentActivityImageGeneration}, want: "Generated an image", ok: true},
		{name: "review", item: AgentActivityItem{Kind: AgentActivityEnteredReviewMode}, want: "Entered review mode", ok: true},
		{name: "compact", item: AgentActivityItem{Kind: AgentActivityContextCompaction}, want: "Compacted context", ok: true},
		{name: "ignored", item: AgentActivityItem{Kind: "user_message", Text: "hidden"}, want: "", ok: false},
	}
	for _, tt := range tests {
		got, ok := AgentActivitySummary(tt.item)
		if got != tt.want || ok != tt.ok {
			t.Fatalf("%s summary = %q ok=%v, want %q ok=%v", tt.name, got, ok, tt.want, tt.ok)
		}
	}
}

func TestAgentStatusThreadPreviewUsesRecentUniqueItems(t *testing.T) {
	events := []AgentActivityEvent{
		{EventType: AgentActivityEventItemStarted, Item: AgentActivityItem{ID: "1", Kind: AgentActivityAgentMessage, Text: "one"}},
		{EventType: AgentActivityEventItemStarted, Item: AgentActivityItem{ID: "2", Kind: AgentActivityAgentMessage, Text: "two"}},
		{EventType: AgentActivityEventItemStarted, Item: AgentActivityItem{ID: "3", Kind: AgentActivityAgentMessage, Text: "three"}},
		{EventType: AgentActivityEventItemStarted, Item: AgentActivityItem{ID: "4", Kind: AgentActivityAgentMessage, Text: "four"}},
		{EventType: AgentActivityEventItemStarted, Item: AgentActivityItem{ID: "5", Kind: AgentActivityAgentMessage, Text: "five"}},
		{EventType: AgentActivityEventItemStarted, Item: AgentActivityItem{ID: "6", Kind: AgentActivityAgentMessage, Text: "six"}},
		{EventType: AgentActivityEventItemCompleted, Item: AgentActivityItem{ID: "6", Kind: AgentActivityAgentMessage, Text: "six updated"}},
		{EventType: AgentActivityEventItemStarted, Item: AgentActivityItem{ID: "7", Kind: AgentActivityAgentMessage, Text: "seven"}},
		{EventType: "ignored", Item: AgentActivityItem{ID: "8", Kind: AgentActivityAgentMessage, Text: "ignored"}},
	}

	preview := NewAgentStatusThreadPreview("agents/scout.md", events)
	want := []string{"two", "three", "four", "five", "six updated", "seven"}
	if !reflect.DeepEqual(preview.Activity, want) {
		t.Fatalf("activity = %#v, want %#v", preview.Activity, want)
	}
	if preview.AgentPath != "agents/scout.md" {
		t.Fatalf("agent path = %q", preview.AgentPath)
	}
}

func TestAgentStatusThreadPreviewWrapsAndKeepsLastThreeLines(t *testing.T) {
	preview := AgentStatusThreadPreview{Activity: []string{
		"alpha beta gamma delta",
		"epsilon zeta eta theta",
	}}
	lines := preview.PreviewLines(10)
	want := []string{"epsilon", "zeta eta", "theta"}
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("preview lines = %#v, want %#v", lines, want)
	}
}

func TestBoundedAgentActivitySummaryTruncatesAndCompactsWhitespace(t *testing.T) {
	long := strings.Repeat("x", AgentStatusPreviewGraphemes+20)
	got, ok := BoundedAgentActivitySummary("  hello \n\t world  " + long)
	if !ok {
		t.Fatalf("summary should be present")
	}
	if strings.Contains(got, "\n") || strings.Contains(got, "\t") || len([]rune(got)) > AgentStatusPreviewGraphemes {
		t.Fatalf("summary not compact/truncated: %q", got)
	}
}
