package bottompane

import (
	"reflect"
	"strings"
	"testing"
)

func TestPendingThreadApprovalsEmptyAndSetThreadsMatchRust(t *testing.T) {
	widget := NewPendingThreadApprovals()
	if !widget.IsEmpty() || widget.DesiredHeight(40) != 0 {
		t.Fatalf("empty widget state rows=%#v height=%d", widget.Rows(40), widget.DesiredHeight(40))
	}
	if !widget.SetThreads([]string{"Robie [explorer]"}) {
		t.Fatal("first SetThreads should report changed")
	}
	if widget.SetThreads([]string{"Robie [explorer]"}) {
		t.Fatal("same SetThreads should report unchanged")
	}
	got := widget.Threads()
	got[0] = "mutated"
	if !reflect.DeepEqual(widget.Threads(), []string{"Robie [explorer]"}) {
		t.Fatalf("Threads should return a copy, got %#v", widget.Threads())
	}
}

func TestPendingThreadApprovalsRowsMatchRustShape(t *testing.T) {
	widget := NewPendingThreadApprovals()
	widget.SetThreads([]string{
		"Main [default]",
		"Robie [explorer]",
		"Inspector",
		"Extra agent",
	})
	rows := widget.Rows(44)
	rendered := strings.Join(rows, "\n")
	for _, want := range []string{
		"  ! Approval needed in Main [default]",
		"  ! Approval needed in Robie [explorer]",
		"  ! Approval needed in Inspector",
		"    ...",
		"    /agent to switch threads",
	} {
		if !bottomPaneContainsRow(rows, want) {
			t.Fatalf("rows missing %q:\n%s", want, rendered)
		}
	}
	if bottomPaneContainsRow(rows, "Extra agent") {
		t.Fatalf("rows should cap visible thread entries at three:\n%s", rendered)
	}
}

func TestPendingThreadApprovalsWrapAndApprovalCompatibility(t *testing.T) {
	widget := NewPendingThreadApprovals()
	changed := widget.SetApprovals([]PendingThreadApproval{
		{ID: "thread-1", Summary: "Very long worker thread name"},
		{ID: "thread-2"},
	})
	if !changed {
		t.Fatal("SetApprovals should report changed")
	}
	rows := widget.Rows(18)
	rendered := strings.Join(rows, "\n")
	for _, want := range []string{
		"  ! Approval",
		"    needed in Very",
		"    long worker",
		"    thread name",
		"  ! Approval",
		"    needed in",
		"    thread-2",
		"    /agent to switch threads",
	} {
		if !bottomPaneContainsRow(rows, want) {
			t.Fatalf("wrapped rows missing %q:\n%s", want, rendered)
		}
	}
}
