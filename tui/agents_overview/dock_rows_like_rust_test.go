package agentsoverview

import (
	"strings"
	"testing"
)

// Rust #62990: the docked task list paints a second line with the task status
// and Git branch, and either line opens the task.
func TestDockedRowsPaintStatusAndBranchLikeRust(t *testing.T) {
	view := New([]Row{{
		ThreadID:  "task-1",
		Name:      "alpha",
		Group:     GroupReady,
		GitBranch: "feature/dock",
	}}, "task-1", false)
	view.DockOpen = true

	lines := view.RenderStyled(36, 20)
	if len(lines) == 0 {
		t.Fatal("docked dashboard painted nothing")
	}
	index := -1
	for i, line := range lines {
		if strings.Contains(stripANSIForTest(line), "alpha") {
			index = i
			break
		}
	}
	if index < 0 || index+1 >= len(lines) {
		t.Fatalf("task row not found in %#v", lines)
	}
	context := stripANSIForTest(lines[index+1])
	if !strings.Contains(context, "feature/dock") {
		t.Fatalf("context line = %q, want the branch", context)
	}
	if !strings.Contains(context, "Ready") {
		t.Fatalf("context line = %q, want the status", context)
	}
	if id, ok := view.ThreadAtRow(index); !ok || id != "task-1" {
		t.Fatalf("task row hit = %q %t", id, ok)
	}
	if id, ok := view.ThreadAtRow(index + 1); !ok || id != "task-1" {
		t.Fatalf("context row hit = %q %t", id, ok)
	}

	// The modal list keeps one line per task.
	plain := New([]Row{{ThreadID: "task-1", Name: "alpha", Group: GroupReady, GitBranch: "feature/dock"}}, "task-1", false)
	lines = plain.RenderStyled(36, 20)
	for i, line := range lines {
		if strings.Contains(stripANSIForTest(line), "alpha") && i+1 < len(lines) {
			if strings.Contains(stripANSIForTest(lines[i+1]), "feature/dock") {
				t.Fatalf("non-docked list painted a context line: %q", stripANSIForTest(lines[i+1]))
			}
			break
		}
	}
}
