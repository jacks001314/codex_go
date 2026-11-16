package agentsoverview

import (
	"strings"
	"testing"
)

// Rust #61742: Cloud tasks keep their marker in the list, narrow truncation,
// and the task-details title.
func TestAgentsOverviewCloudLabelsLikeRust(t *testing.T) {
	rows := []Row{
		{ThreadID: "cloud-failed", Name: "Failed task", Group: GroupNeedsYou, StatusType: "systemError", Unread: true, Cloud: true},
		{ThreadID: "cloud-long", Name: "A longer task title that uses the available column", Group: GroupReady, Cloud: true},
		{ThreadID: "local", Name: "Local task", Group: GroupWorking},
	}
	view := New(rows, "cloud-failed", false)
	narrow := strings.Join(view.Render(50, 24), "\n")
	if !strings.Contains(narrow, "● Error: ☁︎ Failed task") {
		t.Fatalf("narrow cloud row missing marker:\n%s", narrow)
	}
	if !strings.Contains(narrow, "☁︎ A longer task title") || !strings.Contains(narrow, "…") {
		t.Fatalf("narrow cloud truncation missing marker/ellipsis:\n%s", narrow)
	}
	if strings.Contains(narrow, "☁︎ Local task") {
		t.Fatalf("local task gained a cloud marker:\n%s", narrow)
	}

	wide := strings.Join(view.Render(140, 38), "\n")
	if !strings.Contains(wide, "\n☁︎ Failed task\n") {
		t.Fatalf("task details missing cloud marker:\n%s", wide)
	}
}
