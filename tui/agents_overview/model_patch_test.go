package agentsoverview

import (
	"strings"
	"testing"
)

// TestSetRowModelPatchesListedTask covers Rust #44957's immediate model patch:
// the listed row's model changes in place and model grouping follows.
func TestSetRowModelPatchesListedTask(t *testing.T) {
	rows := sampleRows()
	rows[0].Model = "gpt-5.2-codex"
	view := New(rows, "", false)
	view.ToggleGrouping() // project -> status
	view.ToggleGrouping() // status -> model

	if !view.SetRowModel("t-1", "gpt-6-astra") {
		t.Fatal("the listed task's model should change")
	}
	if view.Rows[0].Model != "gpt-6-astra" {
		t.Fatalf("row model = %q", view.Rows[0].Model)
	}
	if view.SetRowModel("t-1", "gpt-6-astra") {
		t.Fatal("an unchanged model should report no change")
	}
	if view.SetRowModel("missing", "gpt-6-astra") {
		t.Fatal("an unknown task must not report a change")
	}
	joined := strings.Join(view.Render(120, 24), "\n")
	if !strings.Contains(joined, "gpt-6-astra") && !strings.Contains(joined, "GPT-6 Astra") {
		t.Fatalf("model grouping did not follow the patched model:\n%s", joined)
	}
}
