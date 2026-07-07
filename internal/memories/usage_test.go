package memories

import (
	"reflect"
	"testing"
)

func TestUsageKindFromPath(t *testing.T) {
	cases := []struct {
		path string
		want UsageKind
	}{
		{"/home/me/.codex/memories/MEMORY.md", UsageKindMemoryMD},
		{`C:\Users\me\.codex\memories\memory_summary.md`, UsageKindMemorySummary},
		{"memories/raw_memories.md", UsageKindRawMemories},
		{"memories/rollout_summaries/thread-a.md", UsageKindRolloutSummaries},
		{"memories/skills/ad_hoc/instructions.md", UsageKindSkills},
	}
	for _, tc := range cases {
		got, ok := UsageKindFromPath(tc.path)
		if !ok || got != tc.want {
			t.Fatalf("UsageKindFromPath(%q) = %s/%v, want %s", tc.path, got, ok, tc.want)
		}
	}
}

func TestUsageKindsFromCommand(t *testing.T) {
	cases := []struct {
		command string
		want    []UsageKind
	}{
		{"cat ~/.codex/memories/MEMORY.md", []UsageKind{UsageKindMemoryMD}},
		{"rg notes ~/.codex/memories/raw_memories.md ~/.codex/memories/skills/ad_hoc/instructions.md", []UsageKind{UsageKindRawMemories, UsageKindSkills}},
		{"git status", nil},
	}
	for _, tc := range cases {
		if got := UsageKindsFromCommand(tc.command); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("UsageKindsFromCommand(%q) = %#v, want %#v", tc.command, got, tc.want)
		}
	}
}
