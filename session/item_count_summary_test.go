package session

import "testing"

func TestCountJSONArrayElements(t *testing.T) {
	cases := map[string]int{
		`[]`:                     0,
		`[1]`:                    1,
		`[1,2,3]`:                3,
		`[{"a":"x,y"},"b",[]]`:   3,
		`[ "a]b" , null, true ]`: 3,
		`["a,b",{"nested":{"k":[1,2]}},{"escaped":"\"}"}]`: 3,
		" \n[\n{\"id\":\"1\"},\n{\"id\":\"2\"}\n]\n":       2,
		`{"items":[1,2,3]}`:                                0,
	}
	for input, want := range cases {
		if got := countJSONArrayElements([]byte(input)); got != want {
			t.Errorf("countJSONArrayElements(%s) = %d, want %d", input, got, want)
		}
	}
}

func TestStoreListCountsItemsWithoutMaterializingHistory(t *testing.T) {
	store := NewStore(t.TempDir())
	now := fixedTime()
	record := &Record{
		ID:        "thread-1",
		Title:     "alpha",
		Preview:   "first preview",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata:  Metadata{CWD: "/repo", ModelProvider: "openai", Source: "cli"},
		Items: []Item{
			{ID: "item-1", Type: "message", Role: "user", Text: "hello", CreatedAt: now},
			{ID: "item-2", Type: "message", Role: "assistant", Text: "hi", CreatedAt: now},
			{ID: "item-3", Type: "message", Role: "user", Text: "again", CreatedAt: now},
		},
	}
	if err := store.Save(record); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	page, err := store.List(ListOptions{SortKey: SortRecencyAt, SortDirection: SortDesc})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(page.Records) != 1 {
		t.Fatalf("List() records = %d, want 1", len(page.Records))
	}
	if page.Records[0].Items != nil {
		t.Fatalf("List() materialized %d items, want none", len(page.Records[0].Items))
	}
	if page.Records[0].ItemCount != 3 {
		t.Fatalf("List() ItemCount = %d, want 3", page.Records[0].ItemCount)
	}

	history, err := store.List(ListOptions{SortKey: SortRecencyAt, SortDirection: SortDesc, IncludeHistory: true})
	if err != nil {
		t.Fatalf("List(IncludeHistory) error = %v", err)
	}
	if len(history.Records) != 1 || len(history.Records[0].Items) != 3 {
		t.Fatalf("List(IncludeHistory) items = %d, want 3", len(history.Records[0].Items))
	}
}
