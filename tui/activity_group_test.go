package tui

import (
	"strings"
	"testing"
)

// Mirrors Rust ActivityGroup::transcript_lines: reasoning renders in order in
// the rich transcript and never in the compact or raw forms.
func TestActivityGroupInterleavesReasoningLikeRust(t *testing.T) {
	group := &ActivityGroup[string]{}
	// Calls and reasoning arrive incrementally, as they do live.
	group.Calls = append(group.Calls, "ls")
	if !group.PushReasoning("reasoning-1", "Read the file next.", "") {
		t.Fatal("PushReasoning rejected a new item")
	}
	group.Calls = append(group.Calls, "cat file.txt")
	if !group.PushReasoning("reasoning-2", "Waiting for the file.", "") {
		t.Fatal("PushReasoning rejected a new item")
	}
	// A re-delivered completion of the same item is a no-op.
	if group.PushReasoning("reasoning-2", "Waiting for the file.", "") {
		t.Fatal("PushReasoning accepted a duplicate item")
	}

	renderCall := func(index int, call string) []string {
		lines := []string{}
		if index > 0 {
			lines = append(lines, "")
		}
		return append(lines, "$ "+call)
	}
	rich := group.TranscriptLines(80, true, renderCall, func(reasoning ActivityReasoning, _ int) []string {
		return []string{"• " + reasoning.Content}
	})
	want := []string{
		"$ ls",
		"",
		"• Read the file next.",
		"",
		"$ cat file.txt",
		"",
		"• Waiting for the file.",
	}
	if strings.Join(rich, "\n") != strings.Join(want, "\n") {
		t.Fatalf("rich transcript =\n%s\nwant\n%s", strings.Join(rich, "\n"), strings.Join(want, "\n"))
	}

	raw := group.TranscriptLines(80, false, renderCall, func(reasoning ActivityReasoning, _ int) []string {
		return []string{"• " + reasoning.Content}
	})
	if strings.Join(raw, "\n") != strings.Join([]string{"$ ls", "", "$ cat file.txt"}, "\n") {
		t.Fatalf("raw transcript =\n%s", strings.Join(raw, "\n"))
	}
}

// A block that renders empty (no content) does not add a separator line.
func TestActivityGroupSkipsEmptyReasoningBlocksLikeRust(t *testing.T) {
	group := &ActivityGroup[string]{Calls: []string{"ls"}}
	group.PushReasoning("reasoning-1", "", "")
	lines := group.TranscriptLines(80, true, func(int, string) []string { return []string{"$ ls"} }, func(ActivityReasoning, int) []string {
		return nil
	})
	if strings.Join(lines, "\n") != "$ ls" {
		t.Fatalf("transcript = %#v", lines)
	}
}
