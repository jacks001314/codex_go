package session

import (
	"testing"
	"time"
)

// Rust #61181: files created after the first inventory scan must still expose
// another page when listing ascending recency with a provider filter.
func TestAscendingRecencyFindsRolloutsCreatedAfterInitialScan(t *testing.T) {
	root := t.TempDir()
	reader := NewStore(root)
	options := ListOptions{PageSize: 1, SortKey: SortRecencyAt, SortDirection: SortAsc, ModelProviders: []string{"openai"}}
	empty, err := reader.List(options)
	if err != nil || len(empty.Records) != 0 {
		t.Fatalf("initial page=%#v err=%v", empty, err)
	}
	writer := NewStore(root)
	now := time.Now().UTC().Truncate(time.Second)
	for i, id := range []ThreadID{"oldest", "middle-filtered", "newest"} {
		provider := "openai"
		if i == 1 {
			provider = "other"
		}
		at := now.Add(time.Duration(i) * time.Minute)
		record := &Record{ID: id, CreatedAt: at, UpdatedAt: at, RecencyAt: at, Metadata: Metadata{ModelProvider: provider}}
		if err := writer.Save(record); err != nil {
			t.Fatal(err)
		}
	}
	first, err := reader.List(options)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Records) != 1 || first.Records[0].ID != "oldest" || first.NextCursor == "" {
		t.Fatalf("first page=%#v", first)
	}
	options.Cursor = first.NextCursor
	second, err := reader.List(options)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Records) != 1 || second.Records[0].ID != "newest" || second.NextCursor != "" {
		t.Fatalf("second page=%#v", second)
	}
}
