package session

import (
	"errors"
	"testing"
	"time"
)

func listObservedRecords() []Record {
	createdAt := time.Unix(1_700_000_000, 0).UTC()
	records := make([]Record, 0, 6)
	for index, spec := range []struct {
		id    string
		title string
	}{
		{"thread-1", "needle one"},
		{"thread-2", "unrelated"},
		{"thread-3", "needle three"},
		{"thread-4", "needle four"},
		{"thread-5", "unrelated too"},
		{"thread-6", "needle six"},
	} {
		records = append(records, Record{
			ID: ThreadID(spec.id), SessionID: spec.id,
			Title:     spec.title,
			CreatedAt: createdAt.Add(time.Duration(index) * time.Second),
			UpdatedAt: createdAt.Add(time.Duration(index) * time.Second),
			RecencyAt: createdAt.Add(time.Duration(index) * time.Second),
		})
	}
	return records
}

// Rust #62132: an observed listing reports verified matches in discovery order
// before the authoritative page exists, and the page still decides membership
// and ordering.
func TestListRecordsObservedReportsDiscoveryOrderMatchesLikeRust(t *testing.T) {
	records := listObservedRecords()
	options := ListOptions{PageSize: 2, SortKey: SortCreatedAt, SortDirection: SortDesc, Search: "needle"}
	var reported []Record
	var batches int
	page, err := ListRecordsObserved(records, options, &ListObserver{
		Report: func(batch []Record) bool {
			batches++
			reported = append(reported, batch...)
			return true
		},
	})
	if err != nil {
		t.Fatalf("ListRecordsObserved error = %v", err)
	}
	var reportedIDs []ThreadID
	for _, record := range reported {
		reportedIDs = append(reportedIDs, record.ID)
	}
	want := []ThreadID{"thread-1", "thread-3", "thread-4", "thread-6"}
	if len(reportedIDs) != len(want) {
		t.Fatalf("reported ids = %v, want %v", reportedIDs, want)
	}
	for index := range want {
		if reportedIDs[index] != want[index] {
			t.Fatalf("reported ids = %v, want discovery order %v", reportedIDs, want)
		}
	}
	if batches != 1 {
		t.Fatalf("batches = %d, want one batch for four matches", batches)
	}

	// The authoritative page is untouched by the observer.
	plain, err := ListRecords(records, options)
	if err != nil {
		t.Fatalf("ListRecords error = %v", err)
	}
	if len(page.Records) != len(plain.Records) || page.NextCursor != plain.NextCursor {
		t.Fatalf("observed page = %#v, plain page = %#v", page, plain)
	}
	for index := range plain.Records {
		if page.Records[index].ID != plain.Records[index].ID {
			t.Fatalf("observed page order = %#v, plain = %#v", page.Records, plain.Records)
		}
	}
}

func TestListRecordsObservedCapsReportedRowsAndStopsQuietlyLikeRust(t *testing.T) {
	records := listObservedRecords()
	options := ListOptions{PageSize: 4, SortKey: SortCreatedAt, SortDirection: SortDesc, Search: "needle"}
	var reported []Record
	page, err := ListRecordsObserved(records, options, &ListObserver{
		MaxRows:   2,
		BatchSize: 1,
		Report: func(batch []Record) bool {
			reported = append(reported, batch...)
			return true
		},
	})
	if err != nil {
		t.Fatalf("ListRecordsObserved error = %v", err)
	}
	if len(reported) != 2 {
		t.Fatalf("reported rows = %d, want the MaxRows cap", len(reported))
	}
	if len(page.Records) != 4 {
		t.Fatalf("page records = %d, want the uncapped page size", len(page.Records))
	}

	// A report that returns false stops further rows without stopping the scan.
	reported = nil
	page, err = ListRecordsObserved(records, options, &ListObserver{
		BatchSize: 1,
		Report: func(batch []Record) bool {
			reported = append(reported, batch...)
			return false
		},
	})
	if err != nil {
		t.Fatalf("ListRecordsObserved error = %v", err)
	}
	if len(reported) != 1 || len(page.Records) != 4 {
		t.Fatalf("reported = %d page = %d, want one row and a complete page", len(reported), len(page.Records))
	}
}

// Rust #62132: cancellation ends the scan with a sentinel the caller can map to
// its own protocol error.
func TestListRecordsObservedStopsWhenCancelledLikeRust(t *testing.T) {
	records := listObservedRecords()
	options := ListOptions{PageSize: 4, SortKey: SortCreatedAt, SortDirection: SortDesc, Search: "needle"}
	scanned := 0
	_, err := ListRecordsObserved(records, options, &ListObserver{
		Stopped: func() bool {
			scanned++
			return scanned > 2
		},
	})
	if !errors.Is(err, ErrListCancelled) {
		t.Fatalf("ListRecordsObserved error = %v, want ErrListCancelled", err)
	}
}
