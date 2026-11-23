package agentsoverview

import "testing"

// Rust #62858: a Cloud stream thread/status/changed notification patches the
// listed row, including the status group it sorts and filters by.
func TestSetRowStatusPatchesGroupAndFlagsLikeRust(t *testing.T) {
	view := New([]Row{
		{ThreadID: "cloud-1", Name: "Task", Group: GroupReady, StatusType: "idle"},
	}, "", false)

	if !view.SetRowStatus("cloud-1", "active", true, true, false) {
		t.Fatal("active/approval status did not change the row")
	}
	row := view.Rows[0]
	if row.StatusType != "active" || !row.StatusActive || !row.WaitingOnApproval || row.WaitingOnUserInput {
		t.Fatalf("row = %#v", row)
	}
	if row.Group != GroupNeedsYou {
		t.Fatalf("group = %v, want needs-you", row.Group)
	}
	if view.SetRowStatus("cloud-1", "active", true, true, false) {
		t.Fatal("an identical status reported a change")
	}
	if !view.SetRowStatus("cloud-1", "idle", false, false, false) {
		t.Fatal("idle status did not change the row")
	}
	if got := view.Rows[0]; got.Group != GroupReady || got.StatusActive || got.WaitingOnApproval {
		t.Fatalf("idle row = %#v", got)
	}
	if view.SetRowStatus("unknown", "idle", false, false, false) {
		t.Fatal("an unknown thread reported a change")
	}
}
