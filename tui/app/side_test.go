package app

import (
	"strings"
	"testing"
)

func TestSideBoundaryPromptMarksInheritedHistoryReferenceOnlyMatchRust(t *testing.T) {
	for _, want := range []string{
		"Side conversation boundary.",
		"Everything before this boundary is inherited history",
		"It is not your current task.",
		"Only messages submitted after this boundary are active",
		"Do not continue, execute, or complete",
		"separate from the main thread",
		"External tools may be available according to this thread's current",
		"Any tool calls or outputs visible before this boundary happened",
		"Sub-agents are off-limits in this side conversation.",
		"Do not modify files",
	} {
		if !strings.Contains(SideBoundaryPrompt, want) {
			t.Fatalf("SideBoundaryPrompt missing %q", want)
		}
	}
}
