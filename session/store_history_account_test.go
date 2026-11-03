package session

import (
	"testing"
	"time"
)

// currentAccountOnly keeps untagged threads and ones matching each recorded
// creator ID (Rust #58918).
func TestListRecordsScopesToHistoryAccount(t *testing.T) {
	now := time.Now().UTC()
	records := []Record{
		{ID: "mine", CreatedAt: now, Metadata: Metadata{CreatorUserID: "user-1", CreatorAccountID: "account-1"}},
		{ID: "other", CreatedAt: now.Add(-time.Second), Metadata: Metadata{CreatorUserID: "user-2", CreatorAccountID: "account-2"}},
		{ID: "untagged", CreatedAt: now.Add(-2 * time.Second)},
		{ID: "other-workspace", CreatedAt: now.Add(-3 * time.Second), Metadata: Metadata{CreatorUserID: "user-1", CreatorAccountID: "account-9"}},
	}
	page, err := ListRecords(records, ListOptions{
		PageSize:       10,
		SortKey:        SortCreatedAt,
		SortDirection:  SortDesc,
		HistoryAccount: &HistoryAccount{UserID: "user-1", AccountID: "account-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, record := range page.Records {
		got[string(record.ID)] = true
	}
	if !got["mine"] || !got["untagged"] || got["other"] || got["other-workspace"] {
		t.Fatalf("scoped records = %#v", got)
	}
}
