package chatwidget

import "testing"

func TestPendingGuardianReviewStatusAggregatesParallelReviews(t *testing.T) {
	var state PendingGuardianReviewStatus
	state.StartOrUpdate("a", "first")
	state.StartOrUpdate("b", "second")

	got, ok := state.StatusIndicatorState()
	if !ok {
		t.Fatal("StatusIndicatorState ok = false")
	}
	if got.Header != "Reviewing 2 approval requests" {
		t.Fatalf("header = %q", got.Header)
	}
	if got.Details != "- first\n- second" {
		t.Fatalf("details = %q", got.Details)
	}
	if got.DetailsMaxLines != 4 {
		t.Fatalf("details max lines = %d, want 4", got.DetailsMaxLines)
	}

	state.StartOrUpdate("b", "second updated")
	got, ok = state.StatusIndicatorState()
	if !ok || got.Details != "- first\n- second updated" {
		t.Fatalf("updated status = %#v ok=%v", got, ok)
	}
	if !state.Finish("a") {
		t.Fatal("Finish(a) = false")
	}
	got, ok = state.StatusIndicatorState()
	if !ok || got.Header != "Reviewing approval request" || got.Details != "second updated" || got.DetailsMaxLines != 1 {
		t.Fatalf("single status = %#v ok=%v", got, ok)
	}
	if state.Finish("missing") {
		t.Fatal("Finish(missing) = true")
	}
}

func TestPendingGuardianReviewStatusCapsDetails(t *testing.T) {
	var state PendingGuardianReviewStatus
	state.StartOrUpdate("a", "first")
	state.StartOrUpdate("b", "second")
	state.StartOrUpdate("c", "third")
	state.StartOrUpdate("d", "fourth")

	got, ok := state.StatusIndicatorState()
	if !ok {
		t.Fatal("StatusIndicatorState ok = false")
	}
	want := "- first\n- second\n- third\n+1 more"
	if got.Details != want {
		t.Fatalf("details = %q, want %q", got.Details, want)
	}
}

func TestStatusStateRetryStatusHeaderIsTakenOnce(t *testing.T) {
	state := NewStatusState()
	state.CurrentStatus.Header = "Thinking"

	state.RememberRetryStatusHeader()
	got, ok := state.TakeRetryStatusHeader()
	if !ok || got != "Thinking" {
		t.Fatalf("TakeRetryStatusHeader = %q ok=%v", got, ok)
	}
	if got, ok := state.TakeRetryStatusHeader(); ok || got != "" {
		t.Fatalf("second TakeRetryStatusHeader = %q ok=%v", got, ok)
	}
}

func TestStatusIndicatorStateGuardianReview(t *testing.T) {
	if !((StatusIndicatorState{Header: "Reviewing approval request"}).IsGuardianReview()) {
		t.Fatal("single guardian review not detected")
	}
	if !((StatusIndicatorState{Header: "Reviewing 2 approval requests"}).IsGuardianReview()) {
		t.Fatal("multi guardian review not detected")
	}
	if (StatusIndicatorState{Header: "Working"}).IsGuardianReview() {
		t.Fatal("working detected as guardian review")
	}
}
