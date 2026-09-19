package memories

import (
	"reflect"
	"testing"

	"codex_go/config"
)

func TestUsageFromPath(t *testing.T) {
	cases := []struct {
		path string
		want MemoryUsage
	}{
		{"/home/me/.gcode/memories/MEMORY.md", MemoryUsage{Kind: UsageKindMemoryMD, Version: config.MemoryVersionV1}},
		{`C:\Users\me\.gcode\memories\memory_summary.md`, MemoryUsage{Kind: UsageKindMemorySummary, Version: config.MemoryVersionV1}},
		{"memories/raw_memories.md", MemoryUsage{Kind: UsageKindRawMemories, Version: config.MemoryVersionV1}},
		{"memories/rollout_summaries/thread-a.md", MemoryUsage{Kind: UsageKindRolloutSummaries, Version: config.MemoryVersionV1}},
		{"memories/skills/ad_hoc/instructions.md", MemoryUsage{Kind: UsageKindSkills, Version: config.MemoryVersionV1}},
		// The v2 root is a sibling artifact namespace (#43797): the same
		// artifacts are reported with the version that holds them.
		{"memories_v2/MEMORY.md", MemoryUsage{Kind: UsageKindMemoryMD, Version: config.MemoryVersionV2}},
		{`C:\Users\me\.gcode\memories_v2\raw_memories.md`, MemoryUsage{Kind: UsageKindRawMemories, Version: config.MemoryVersionV2}},
		{"/home/me/.gcode/memories_v2/skills/x/SKILL.md", MemoryUsage{Kind: UsageKindSkills, Version: config.MemoryVersionV2}},
	}
	for _, tc := range cases {
		got, ok := UsageFromPath(tc.path)
		if !ok || got != tc.want {
			t.Fatalf("UsageFromPath(%q) = %+v/%v, want %+v", tc.path, got, ok, tc.want)
		}
	}
	if _, ok := UsageFromPath("/tmp/notes.md"); ok {
		t.Fatal("a path outside both memory roots must not classify")
	}
}

// TestUsageFromCommandFollowsTheParsedScript mirrors Rust's
// memories_usage_from_command: the reads a parsed script performs, in script
// order, each carrying the root version it was read from.
func TestUsageFromCommandFollowsTheParsedScript(t *testing.T) {
	command := "cat /tmp/.codex/memories/MEMORY.md " +
		"&& sed -n '1,20p' /tmp/.codex/memories_v2/rollout_summaries/thread.md " +
		"&& cat /tmp/.codex/memories/raw_memories.md " +
		"&& cat /tmp/.codex/memories_v2/skills/testing/SKILL.md"
	want := []MemoryUsage{
		{Kind: UsageKindMemoryMD, Version: config.MemoryVersionV1},
		{Kind: UsageKindRolloutSummaries, Version: config.MemoryVersionV2},
		{Kind: UsageKindRawMemories, Version: config.MemoryVersionV1},
		{Kind: UsageKindSkills, Version: config.MemoryVersionV2},
	}
	if got := UsageFromCommand(command); !reflect.DeepEqual(got, want) {
		t.Fatalf("UsageFromCommand() = %#v, want %#v", got, want)
	}
}

// A PowerShell read of the v2 root is attributed to v2 even though the path
// uses Windows separators (Rust normalizes them before classifying).
func TestUsageFromCommandNormalizesWindowsPaths(t *testing.T) {
	for _, reader := range []string{"Get-Content", "cat"} {
		command := reader + " 'C:\\Users\\test\\.codex\\memories_v2\\memory_summary.md'"
		want := []MemoryUsage{{Kind: UsageKindMemorySummary, Version: config.MemoryVersionV2}}
		if got := UsageFromCommand(command); !reflect.DeepEqual(got, want) {
			t.Fatalf("UsageFromCommand(%q) = %#v, want %#v", command, got, want)
		}
	}
}

// Rust reports nothing when any part of the script is an action it cannot
// classify, so a mixed chain never attributes a partial read.
func TestUsageFromCommandReportsNothingForAnUnclassifiedScript(t *testing.T) {
	cases := []string{
		"git status",
		"cat /tmp/.codex/memories/MEMORY.md; git status",
		"cat /tmp/.codex/memories/MEMORY.md && npm test",
		"cat /tmp/.codex/memories/MEMORY.md > /tmp/out.txt",
		// `~` survives shell expansion, so the parser refuses to guess the path.
		"cat ~/.codex/memories/MEMORY.md",
	}
	for _, command := range cases {
		if got := UsageFromCommand(command); got != nil {
			t.Fatalf("UsageFromCommand(%q) = %#v, want nil", command, got)
		}
	}
}

// A pipeline formatting helper is dropped before the read is attributed, so
// `cat <artifact> | tee out` still reports the artifact (Rust drops tee via
// drop_small_formatting_commands).
func TestUsageFromCommandDropsFormattingHelpers(t *testing.T) {
	want := []MemoryUsage{{Kind: UsageKindMemoryMD, Version: config.MemoryVersionV1}}
	for _, command := range []string{
		"cat /tmp/.codex/memories/MEMORY.md | tee /tmp/out.txt",
		"cat /tmp/.codex/memories/MEMORY.md | head -n 5",
	} {
		if got := UsageFromCommand(command); !reflect.DeepEqual(got, want) {
			t.Fatalf("UsageFromCommand(%q) = %#v, want %#v", command, got, want)
		}
	}
}

// A directory listing names the memory root, not an artifact, so it reports no
// usage on its own (Rust's ListFiles arm maps to None).
func TestUsageFromCommandIgnoresListings(t *testing.T) {
	if got := UsageFromCommand("ls /tmp/.codex/memories"); got != nil {
		t.Fatalf("UsageFromCommand(ls) = %#v, want nil", got)
	}
}
