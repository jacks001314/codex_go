package tui

import (
	"errors"
	"testing"
)

func catalogStringPointer(value string) *string {
	return &value
}

// TestThreadCatalogPagesBackendsIndependentlyLikeRust mirrors Rust #56543 and
// #57215: pages advance and retire per source, errors retain that source's
// cursor, and successful rows are tagged with the source that listed them.
func TestThreadCatalogPagesBackendsIndependentlyLikeRust(t *testing.T) {
	catalog := NewThreadCatalog()
	slow := NewBackendID()
	failed := NewBackendID()
	healthy := NewBackendID()

	slowRequest, ok := catalog.BeginPage(slow, SessionPageLoadStoreDefault)
	if !ok {
		t.Fatal("slow source did not begin its first page")
	}

	failedRequest, ok := catalog.BeginPage(failed, SessionPageLoadStoreDefault)
	if !ok {
		t.Fatal("failed source did not begin its first page")
	}
	if failedRequest.Cursor != nil {
		t.Fatalf("first failed cursor = %v, want nil", failedRequest.Cursor)
	}
	if _, _, accepted := catalog.FinishPage(failedRequest, SessionPage{
		Rows:       []SessionSummary{{ThreadID: "failed-first"}},
		NextCursor: catalogStringPointer("failed-next"),
	}, nil); !accepted {
		t.Fatal("first failed-source page was rejected")
	}

	failingRequest, ok := catalog.BeginPage(failed, SessionPageLoadStoreDefault)
	if !ok {
		t.Fatal("failed source did not begin its next page")
	}
	if failingRequest.Cursor == nil || *failingRequest.Cursor != "failed-next" {
		t.Fatalf("retry cursor = %v, want failed-next", failingRequest.Cursor)
	}
	wantErr := errors.New("source unavailable")
	if _, gotErr, accepted := catalog.FinishPage(failingRequest, SessionPage{}, wantErr); !accepted || !errors.Is(gotErr, wantErr) {
		t.Fatalf("failed page result = (%v, %v), want accepted error", accepted, gotErr)
	}

	healthyRequest, ok := catalog.BeginPage(healthy, SessionPageLoadStoreDefault)
	if !ok {
		t.Fatal("healthy source did not begin")
	}
	healthyPage, err, accepted := catalog.FinishPage(healthyRequest, SessionPage{
		Rows: []SessionSummary{{ThreadID: "healthy-row"}},
	}, nil)
	if err != nil || !accepted {
		t.Fatalf("healthy page = (%v, %v), want accepted success", accepted, err)
	}
	if got := healthyPage.Rows[0].BackendID; got != healthy {
		t.Fatalf("healthy row backend = %v, want %v", got, healthy)
	}
	if _, ok := catalog.BeginPage(healthy, SessionPageLoadStoreDefault); ok {
		t.Fatal("exhausted source began another page")
	}
	if _, ok := catalog.BeginPage(slow, SessionPageLoadStoreDefault); ok {
		t.Fatal("pending source began a second page")
	}

	retryRequest, ok := catalog.BeginPage(failed, SessionPageLoadStoreDefault)
	if !ok {
		t.Fatal("failed source did not retry the retained cursor")
	}
	if retryRequest.Cursor == nil || *retryRequest.Cursor != "failed-next" {
		t.Fatalf("retry-after-error cursor = %v, want failed-next", retryRequest.Cursor)
	}
	if _, _, accepted := catalog.FinishPage(retryRequest, SessionPage{}, nil); !accepted {
		t.Fatal("failed source retry was rejected")
	}
	if _, ok := catalog.BeginPage(failed, SessionPageLoadStoreDefault); ok {
		t.Fatal("failed source began a page after exhausting its retry")
	}

	if _, _, accepted := catalog.FinishPage(slowRequest, SessionPage{}, nil); !accepted {
		t.Fatal("slow source completion was rejected")
	}
	if _, ok := catalog.BeginPage(slow, SessionPageLoadStoreDefault); ok {
		t.Fatal("slow source began a page after exhaustion")
	}
	if catalog.HasMore(nil) {
		t.Fatal("catalog still reports more pages after every source exhausted")
	}
}

// TestThreadCatalogResetRejectsStaleCompletionsLikeRust mirrors Rust #56543:
// a reset invalidates old tokens without finishing a request from the new
// listing.
func TestThreadCatalogResetRejectsStaleCompletionsLikeRust(t *testing.T) {
	catalog := NewThreadCatalog()
	backendID := NewBackendID()
	oldRequest, ok := catalog.BeginPage(backendID, SessionPageLoadStoreDefault)
	if !ok {
		t.Fatal("initial page did not begin")
	}

	catalog.Reset()
	currentRequest, ok := catalog.BeginPage(backendID, SessionPageLoadStoreDefault)
	if !ok {
		t.Fatal("page after reset did not begin")
	}
	if _, _, accepted := catalog.FinishPage(oldRequest, SessionPage{}, nil); accepted {
		t.Fatal("stale completion was accepted after reset")
	}
	if _, ok := catalog.BeginPage(backendID, SessionPageLoadStoreDefault); ok {
		t.Fatal("stale completion finished the current page")
	}

	page, err, accepted := catalog.FinishPage(currentRequest, SessionPage{
		Rows: []SessionSummary{{ThreadID: "current-row"}},
	}, nil)
	if err != nil || !accepted {
		t.Fatalf("current completion = (%v, %v), want accepted success", accepted, err)
	}
	if got := page.Rows[0].BackendID; got != backendID {
		t.Fatalf("current row backend = %v, want %v", got, backendID)
	}
}

// TestThreadCatalogHasMoreExcludesUnavailableSourcesLikeRust verifies that a
// source that failed permanently does not keep the picker loading forever.
func TestThreadCatalogHasMoreExcludesUnavailableSourcesLikeRust(t *testing.T) {
	catalog := NewThreadCatalog()
	healthy := NewBackendID()
	disconnected := NewBackendID()
	healthyRequest, ok := catalog.BeginPage(healthy, SessionPageLoadStoreDefault)
	if !ok {
		t.Fatal("healthy source did not begin")
	}
	disconnectedRequest, ok := catalog.BeginPage(disconnected, SessionPageLoadStoreDefault)
	if !ok {
		t.Fatal("disconnected source did not begin")
	}
	if !catalog.HasMore(nil) {
		t.Fatal("pending sources should report more pages")
	}
	catalog.FinishPage(healthyRequest, SessionPage{NextCursor: catalogStringPointer("next")}, nil)
	catalog.FinishPage(disconnectedRequest, SessionPage{}, errors.New("disconnected"))
	if !catalog.HasMore(nil) {
		t.Fatal("remaining healthy cursor should report more pages")
	}
	if !catalog.HasMore(map[BackendID]bool{healthy: true}) {
		t.Fatal("a retryable failed source should still report more pages")
	}
	if catalog.HasMore(map[BackendID]bool{healthy: true, disconnected: true}) {
		t.Fatal("excluded unavailable sources still reported more pages")
	}
}

// TestSessionPickerSelectionUsesRowBackendLikeRust mirrors Rust #56543:
// selecting a row routes resume/fork/archive actions to the source that
// listed it, not merely to the picker owner.
func TestSessionPickerSelectionUsesRowBackendLikeRust(t *testing.T) {
	pickerOwner := NewBackendID()
	rowOwner := NewBackendID()
	picker := NewSessionPickerStateForBackend(pickerOwner, SessionPickerResume, []SessionSummary{{
		BackendID: rowOwner,
		ThreadID:  "thread-row",
	}}, "")
	selection, ok := picker.Selection()
	if !ok {
		t.Fatal("Selection() = false")
	}
	if selection.Target.BackendID != rowOwner {
		t.Fatalf("selection backend = %v, want row owner %v", selection.Target.BackendID, rowOwner)
	}
}

// TestSessionPickerSelectionFallsBackToPickerBackendLikeRust keeps existing
// single-source rows working when they do not carry a backend tag.
func TestSessionPickerSelectionFallsBackToPickerBackendLikeRust(t *testing.T) {
	pickerOwner := NewBackendID()
	picker := NewSessionPickerStateForBackend(pickerOwner, SessionPickerResume, []SessionSummary{{
		ThreadID: "thread-untagged",
	}}, "")
	selection, ok := picker.Selection()
	if !ok {
		t.Fatal("Selection() = false")
	}
	if selection.Target.BackendID != pickerOwner {
		t.Fatalf("selection backend = %v, want picker owner %v", selection.Target.BackendID, pickerOwner)
	}
}

// TestStampSessionSummariesBackendPreservesSourcesLikeRust verifies merged
// picker rows keep the backend that listed them.
func TestStampSessionSummariesBackendPreservesSourcesLikeRust(t *testing.T) {
	first := NewBackendID()
	second := NewBackendID()
	items := StampSessionSummariesBackend([]SessionSummary{
		{ThreadID: "untagged"},
		{ThreadID: "tagged", BackendID: second},
	}, first)
	if items[0].BackendID != first {
		t.Fatalf("untagged row backend = %v, want %v", items[0].BackendID, first)
	}
	if items[1].BackendID != second {
		t.Fatalf("tagged row backend = %v, want preserved %v", items[1].BackendID, second)
	}
}
