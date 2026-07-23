package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSearchMessageOccurrencesFiltersAssistantCommentary(t *testing.T) {
	store := NewStore(t.TempDir())
	now := time.Now().UTC()
	record := &Record{ID: "thread-search", CreatedAt: now, UpdatedAt: now, Items: []Item{
		{ID: "user", Type: "user_message", Role: "user", Text: "Needle user", Data: map[string]any{"turn_id": "turn-1"}},
		{ID: "commentary", Type: "assistant_message", Role: "assistant", Text: "Needle hidden", Data: map[string]any{"turn_id": "turn-1", "phase": "commentary"}},
		{ID: "final", Type: "assistant_message", Role: "assistant", Text: "Needle final", Data: map[string]any{"turn_id": "turn-1", "phase": "final_answer"}},
	}}
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	got, err := store.SearchMessageOccurrences(record.ID, "needle")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ItemID != "user" || got[1].ItemID != "final" {
		t.Fatalf("occurrences = %#v", got)
	}
}

func TestStorePinUpdatePersistsAndFiltersBeforePagination(t *testing.T) {
	store := NewStore(t.TempDir())
	now := fixedTime()
	for i, id := range []ThreadID{"thread-1", "thread-2", "thread-3"} {
		at := now.Add(time.Duration(i) * time.Minute)
		if err := store.Save(&Record{ID: id, CreatedAt: at, UpdatedAt: at, RecencyAt: at}); err != nil {
			t.Fatalf("Save(%s) error = %v", id, err)
		}
	}
	pinned := true
	if _, err := store.UpdateMetadata("thread-2", &MetadataPatch{IsPinned: &pinned}, true); err != nil {
		t.Fatalf("pin thread error = %v", err)
	}
	page, err := store.List(ListOptions{PageSize: 1, IsPinned: &pinned})
	if err != nil {
		t.Fatalf("List(pinned) error = %v", err)
	}
	if got := ids(page.Records); !reflect.DeepEqual(got, []ThreadID{"thread-2"}) || page.NextCursor != "" {
		t.Fatalf("List(pinned) = %v cursor %q, want [thread-2] and no cursor", got, page.NextCursor)
	}
	loaded, err := store.Load("thread-2")
	if err != nil || !loaded.IsPinned {
		t.Fatalf("Load(thread-2) = %#v, %v; want persisted pin", loaded, err)
	}
	pinned = false
	if _, err := store.UpdateMetadata("thread-2", &MetadataPatch{IsPinned: &pinned}, true); err != nil {
		t.Fatalf("unpin thread error = %v", err)
	}
	loaded, err = store.Load("thread-2")
	if err != nil || loaded.IsPinned {
		t.Fatalf("Load(thread-2) after unpin = %#v, %v", loaded, err)
	}
}

func TestStoreSaveLoadRoundTrip(t *testing.T) {
	store := NewStore(t.TempDir())
	now := fixedTime()
	record := &Record{
		ID:        "thread-1",
		SessionID: "session-1",
		Title:     "Build session store",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata: Metadata{
			CWD:           "/repo",
			Model:         "gpt-5",
			ModelProvider: "openai",
			Source:        "cli",
		},
		Items: []Item{
			{ID: "item-1", Type: "user_message", Role: "user", Text: "hello", CreatedAt: now},
		},
	}

	if err := store.Save(record); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, err := store.Load("thread-1")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !reflect.DeepEqual(got, record) {
		t.Fatalf("Load() = %#v, want %#v", got, record)
	}
}

func TestStoreCreateRejectsExistingThread(t *testing.T) {
	store := NewStore(t.TempDir())
	record := &Record{ID: "thread-1", Title: "one"}
	if err := store.Create(record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := store.Create(record); !errors.Is(err, ErrConflict) {
		t.Fatalf("Create() error = %v, want ErrConflict", err)
	}
}

func TestStoreReadHonorsArchiveFlag(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Save(&Record{ID: "thread-1", Archived: true}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, err := store.Read("thread-1", false, true); !errors.Is(err, ErrThreadArchived) {
		t.Fatalf("Read() error = %v, want ErrThreadArchived", err)
	}
	got, err := store.Read("thread-1", true, false)
	if err != nil {
		t.Fatalf("Read(includeArchived) error = %v", err)
	}
	if !got.Archived {
		t.Fatalf("Read(includeArchived).Archived = false, want true")
	}
}

func TestStoreAppendItemsUpdatesRecencyAndPreview(t *testing.T) {
	store := NewStore(t.TempDir())
	now := fixedTime()
	if err := store.Save(&Record{ID: "thread-1", CreatedAt: now, UpdatedAt: now, RecencyAt: now}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	later := now.Add(time.Minute)
	got, err := store.AppendItems("thread-1", []Item{
		{ID: "item-1", Type: "user_message", Text: "first", CreatedAt: now},
		{ID: "item-2", Type: "agent_message", Text: "second", CreatedAt: later},
	})
	if err != nil {
		t.Fatalf("AppendItems() error = %v", err)
	}
	if len(got.Items) != 2 {
		t.Fatalf("AppendItems() len = %d, want 2", len(got.Items))
	}
	if got.Preview != "first" {
		t.Fatalf("AppendItems() Preview = %q, want first", got.Preview)
	}
	if !got.RecencyAt.Equal(later) {
		t.Fatalf("AppendItems() RecencyAt = %s, want %s", got.RecencyAt, later)
	}
}

func TestStoreUpdateMetadataPatchesFields(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Save(&Record{ID: "thread-1"}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	title := "New title"
	cwd := "/new"
	git := map[string]string{"branch": "main"}
	got, err := store.UpdateMetadata("thread-1", &MetadataPatch{
		Title: &title,
		CWD:   &cwd,
		Git:   git,
	}, true)
	if err != nil {
		t.Fatalf("UpdateMetadata() error = %v", err)
	}
	git["branch"] = "mutated"
	if got.Title != title || got.Metadata.CWD != cwd || got.Metadata.Git["branch"] != "main" {
		t.Fatalf("UpdateMetadata() = %#v", got)
	}
}

func TestStoreArchiveUnarchiveAndList(t *testing.T) {
	store := NewStore(t.TempDir())
	now := fixedTime()
	records := []*Record{
		{ID: "thread-1", Title: "first", CreatedAt: now, UpdatedAt: now, RecencyAt: now},
		{ID: "thread-2", Title: "second", CreatedAt: now.Add(time.Minute), UpdatedAt: now.Add(time.Minute), RecencyAt: now.Add(time.Minute)},
	}
	for _, record := range records {
		if err := store.Save(record); err != nil {
			t.Fatalf("Save(%s) error = %v", record.ID, err)
		}
	}
	if err := store.Archive("thread-1"); err != nil {
		t.Fatalf("Archive() error = %v", err)
	}
	active, err := store.List(ListOptions{Archived: false})
	if err != nil {
		t.Fatalf("List(active) error = %v", err)
	}
	if got := ids(active.Records); !reflect.DeepEqual(got, []ThreadID{"thread-2"}) {
		t.Fatalf("List(active) = %v, want [thread-2]", got)
	}
	archived, err := store.List(ListOptions{Archived: true})
	if err != nil {
		t.Fatalf("List(archived) error = %v", err)
	}
	if got := ids(archived.Records); !reflect.DeepEqual(got, []ThreadID{"thread-1"}) {
		t.Fatalf("List(archived) = %v, want [thread-1]", got)
	}
	if _, err := store.Unarchive("thread-1"); err != nil {
		t.Fatalf("Unarchive() error = %v", err)
	}
	active, err = store.List(ListOptions{Archived: false})
	if err != nil {
		t.Fatalf("List(active after unarchive) error = %v", err)
	}
	if got := ids(active.Records); !reflect.DeepEqual(got, []ThreadID{"thread-2", "thread-1"}) {
		t.Fatalf("List(active after unarchive) = %v, want [thread-2 thread-1]", got)
	}
}

func TestStoreListFiltersSortsAndPages(t *testing.T) {
	store := NewStore(t.TempDir())
	now := fixedTime()
	fixtures := []*Record{
		{
			ID:        "thread-1",
			Title:     "alpha",
			Preview:   "first preview",
			CreatedAt: now,
			UpdatedAt: now,
			RecencyAt: now,
			Metadata:  Metadata{CWD: "/repo", ModelProvider: "openai", Source: "cli"},
		},
		{
			ID:        "thread-2",
			Title:     "beta",
			Preview:   "target preview",
			CreatedAt: now.Add(time.Minute),
			UpdatedAt: now.Add(time.Minute),
			RecencyAt: now.Add(time.Minute),
			Metadata:  Metadata{CWD: "/repo", ModelProvider: "azure", Source: "app"},
		},
		{
			ID:        "thread-3",
			Title:     "gamma target",
			CreatedAt: now.Add(2 * time.Minute),
			UpdatedAt: now.Add(2 * time.Minute),
			RecencyAt: now.Add(2 * time.Minute),
			Metadata:  Metadata{CWD: "/else", ModelProvider: "openai", Source: "cli"},
		},
	}
	for _, record := range fixtures {
		if err := store.Save(record); err != nil {
			t.Fatalf("Save(%s) error = %v", record.ID, err)
		}
	}
	page, err := store.List(ListOptions{
		PageSize:      1,
		SortKey:       SortCreatedAt,
		SortDirection: SortAsc,
		Archived:      false,
		Search:        "target",
	})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if got := ids(page.Records); !reflect.DeepEqual(got, []ThreadID{"thread-2"}) {
		t.Fatalf("List(page 1) = %v, want [thread-2]", got)
	}
	if page.NextCursor == "" {
		t.Fatalf("List(page 1) NextCursor = empty, want cursor")
	}
	next, err := store.List(ListOptions{
		Cursor:         page.NextCursor,
		SortKey:        SortCreatedAt,
		SortDirection:  SortAsc,
		Archived:       false,
		Search:         "target",
		IncludeHistory: true,
	})
	if err != nil {
		t.Fatalf("List(page 2) error = %v", err)
	}
	if got := ids(next.Records); !reflect.DeepEqual(got, []ThreadID{"thread-3"}) {
		t.Fatalf("List(page 2) = %v, want [thread-3]", got)
	}
	filtered, err := store.List(ListOptions{
		Archived:       false,
		ModelProviders: []string{"openai"},
		CWDs:           []string{"/repo"},
		Sources:        []string{"cli"},
	})
	if err != nil {
		t.Fatalf("List(filtered) error = %v", err)
	}
	if got := ids(filtered.Records); !reflect.DeepEqual(got, []ThreadID{"thread-1"}) {
		t.Fatalf("List(filtered) = %v, want [thread-1]", got)
	}
	normalizedCWD, err := store.List(ListOptions{
		Archived: false,
		CWDs:     []string{filepath.Join("/repo", "nested", "..")},
		Sources:  []string{"cli"},
	})
	if err != nil {
		t.Fatalf("List(normalized cwd) error = %v", err)
	}
	if got := ids(normalizedCWD.Records); !reflect.DeepEqual(got, []ThreadID{"thread-1"}) {
		t.Fatalf("List(normalized cwd) = %v, want [thread-1]", got)
	}
}

func TestStoreListTimeCursors(t *testing.T) {
	store := NewStore(t.TempDir())
	base := fixedTime()
	records := []*Record{
		{ID: "old", CreatedAt: base, UpdatedAt: base, RecencyAt: base},
		{ID: "watermark", CreatedAt: base.Add(time.Hour), UpdatedAt: base.Add(2 * time.Hour), RecencyAt: base.Add(2 * time.Hour)},
	}
	for _, record := range records {
		if err := store.Save(record); err != nil {
			t.Fatalf("Save(%s) error = %v", record.ID, err)
		}
	}
	page, err := store.List(ListOptions{PageSize: 1, SortKey: SortUpdatedAt, SortDirection: SortDesc})
	if err != nil {
		t.Fatalf("List(desc) error = %v", err)
	}
	if got := ids(page.Records); !reflect.DeepEqual(got, []ThreadID{"watermark"}) {
		t.Fatalf("List(desc) = %v, want [watermark]", got)
	}
	if page.NextCursor != listTimeCursor(base.Add(2*time.Hour)) {
		t.Fatalf("NextCursor = %q", page.NextCursor)
	}
	if page.BackwardsCursor != listBackwardsCursor(base.Add(2*time.Hour), SortDesc) {
		t.Fatalf("BackwardsCursor = %q", page.BackwardsCursor)
	}
	newer := &Record{ID: "newer", CreatedAt: base.Add(3 * time.Hour), UpdatedAt: base.Add(3 * time.Hour), RecencyAt: base.Add(3 * time.Hour)}
	if err := store.Save(newer); err != nil {
		t.Fatalf("Save(newer) error = %v", err)
	}
	delta, err := store.List(ListOptions{Cursor: page.BackwardsCursor, SortKey: SortUpdatedAt, SortDirection: SortAsc})
	if err != nil {
		t.Fatalf("List(delta) error = %v", err)
	}
	if got := ids(delta.Records); !reflect.DeepEqual(got, []ThreadID{"watermark", "newer"}) {
		t.Fatalf("List(delta) = %v, want [watermark newer]", got)
	}
}

func TestStoreListRelationFilters(t *testing.T) {
	store := NewStore(t.TempDir())
	for _, record := range []*Record{
		{ID: "root"},
		{ID: "child-1", ParentThreadID: "root"},
		{ID: "child-2", ParentThreadID: "root"},
		{ID: "grandchild", ParentThreadID: "child-1"},
	} {
		if err := store.Save(record); err != nil {
			t.Fatalf("Save(%s) error = %v", record.ID, err)
		}
	}
	direct, err := store.List(ListOptions{Relation: &RelationFilter{DirectChildrenOf: "root"}})
	if err != nil {
		t.Fatalf("List(direct) error = %v", err)
	}
	if got := ids(direct.Records); !sameIDs(got, []ThreadID{"child-1", "child-2"}) {
		t.Fatalf("List(direct) = %v, want child-1 child-2", got)
	}
	descendants, err := store.List(ListOptions{Relation: &RelationFilter{DescendantsOf: "root"}})
	if err != nil {
		t.Fatalf("List(descendants) error = %v", err)
	}
	if got := ids(descendants.Records); !sameIDs(got, []ThreadID{"child-1", "child-2", "grandchild"}) {
		t.Fatalf("List(descendants) = %v, want child-1 child-2 grandchild", got)
	}
}

func TestStoreForkCopiesRequestedHistory(t *testing.T) {
	store := NewStore(t.TempDir())
	now := fixedTime()
	source := &Record{
		ID:        "source",
		SessionID: "session-1",
		Title:     "Source",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata:  Metadata{ModelProvider: "openai"},
		Items: []Item{
			{ID: "item-1", Type: "user_message", Text: "one", CreatedAt: now},
			{ID: "item-2", Type: "agent_message", Text: "two", CreatedAt: now},
			{ID: "item-3", Type: "user_message", Text: "three", CreatedAt: now},
		},
	}
	if err := store.Save(source); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	forked, err := store.Fork("source", ForkOptions{NewID: "fork", Mode: ForkLastN, LastN: 2, Now: now.Add(time.Hour)})
	if err != nil {
		t.Fatalf("Fork() error = %v", err)
	}
	if forked.ForkedFromID != "source" || forked.ParentThreadID != "source" || forked.SessionID != "fork" {
		t.Fatalf("Fork() lineage = %#v", forked)
	}
	if forked.Title != "Source" {
		t.Fatalf("Fork() title = %q, want Source", forked.Title)
	}
	if got := itemIDs(forked.Items); !reflect.DeepEqual(got, []string{"item-2", "item-3"}) {
		t.Fatalf("Fork() items = %v, want item-2 item-3", got)
	}
	if forked.Metadata.Extra["forked_from_id"] != "source" || forked.Metadata.Extra["fork_mode"] != string(ForkLastN) || forked.Metadata.Extra["fork_item_count"] != 2 {
		t.Fatalf("Fork() snapshot metadata = %#v", forked.Metadata.Extra)
	}
	source.Items[1].Text = "mutated"
	reloaded, err := store.Load("fork")
	if err != nil {
		t.Fatalf("Load(fork) error = %v", err)
	}
	if reloaded.Items[0].Text != "two" {
		t.Fatalf("Fork() did not clone items")
	}
}

func TestStoreForkUsesExplicitSessionID(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Save(&Record{ID: "source", SessionID: "session-1"}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	forked, err := store.Fork("source", ForkOptions{NewID: "fork", SessionID: "custom-session", Mode: ForkNone})
	if err != nil {
		t.Fatalf("Fork() error = %v", err)
	}
	if forked.SessionID != "custom-session" {
		t.Fatalf("Fork() SessionID = %q", forked.SessionID)
	}
}

func TestStoreForkRecordDoesNotRequirePersistedSource(t *testing.T) {
	store := NewStore(t.TempDir())
	source := &Record{
		ID:        "source",
		SessionID: "session-1",
		Items: []Item{
			{ID: "item-1", Type: "message", Role: "user", Text: "hello"},
		},
	}
	forked, err := store.ForkRecord(source, ForkOptions{NewID: "fork", Mode: ForkAll})
	if err != nil {
		t.Fatalf("ForkRecord() error = %v", err)
	}
	if forked.ForkedFromID != "source" || forked.ParentThreadID != "source" {
		t.Fatalf("ForkRecord() lineage = %#v", forked)
	}
	if _, err := store.Load("source"); !errors.Is(err, ErrThreadNotFound) {
		t.Fatalf("Load(source) error = %v, want ErrThreadNotFound", err)
	}
	if _, err := store.Load("fork"); err != nil {
		t.Fatalf("Load(fork) error = %v", err)
	}
}

func TestStoreForkEphemeralDoesNotPersist(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Save(&Record{
		ID:    "source",
		Items: []Item{{ID: "item-1", Type: "user_message", Text: "hello"}},
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	forked, err := store.Fork("source", ForkOptions{NewID: "fork", Mode: ForkAll, Ephemeral: true})
	if err != nil {
		t.Fatalf("Fork() error = %v", err)
	}
	if forked.Metadata.Extra["ephemeral"] != true {
		t.Fatalf("Fork() metadata = %#v", forked.Metadata.Extra)
	}
	if _, err := store.Load("fork"); !errors.Is(err, ErrThreadNotFound) {
		t.Fatalf("Load(ephemeral fork) error = %v, want ErrThreadNotFound", err)
	}
}

func TestStoreForkNoneAndAll(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Save(&Record{
		ID:    "source",
		Items: []Item{{ID: "item-1"}, {ID: "item-2"}},
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	none, err := store.Fork("source", ForkOptions{NewID: "none", Mode: ForkNone})
	if err != nil {
		t.Fatalf("Fork(none) error = %v", err)
	}
	if len(none.Items) != 0 {
		t.Fatalf("Fork(none) len = %d, want 0", len(none.Items))
	}
	all, err := store.Fork("source", ForkOptions{NewID: "all", Mode: ForkAll})
	if err != nil {
		t.Fatalf("Fork(all) error = %v", err)
	}
	if got := itemIDs(all.Items); !reflect.DeepEqual(got, []string{"item-1", "item-2"}) {
		t.Fatalf("Fork(all) = %v, want item-1 item-2", got)
	}
}

func TestStoreForkLastTurnID(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Save(&Record{
		ID: "source",
		Metadata: Metadata{RolloutTurns: []TurnSnapshot{
			{ID: "turn-1", Status: "completed"},
			{ID: "turn-2", Status: "completed"},
			{ID: "turn-3", Status: "inProgress"},
		}},
		Items: []Item{
			{ID: "item-1", Metadata: map[string]any{"turnId": "turn-1"}},
			{ID: "item-2", Metadata: map[string]any{"turnId": "turn-1"}},
			{ID: "item-3", Metadata: map[string]any{"turnId": "turn-2"}},
			{ID: "item-4", Metadata: map[string]any{"turnId": "turn-2"}},
			{ID: "item-5", Metadata: map[string]any{"turnId": "turn-3"}},
		},
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	forked, err := store.Fork("source", ForkOptions{NewID: "fork", Mode: ForkAll, LastTurnID: "turn-2"})
	if err != nil {
		t.Fatalf("Fork(last turn) error = %v", err)
	}
	if got := itemIDs(forked.Items); !reflect.DeepEqual(got, []string{"item-1", "item-2", "item-3", "item-4"}) {
		t.Fatalf("Fork(last turn) items = %v", got)
	}
	if got := forked.Metadata.RolloutTurns; len(got) != 2 || got[0].Status != "completed" || got[1].Status != "completed" {
		t.Fatalf("Fork(last turn) rollout turns = %#v", got)
	}
	lastN, err := store.Fork("source", ForkOptions{NewID: "last-n", Mode: ForkLastN, LastN: 2, LastTurnID: "turn-2"})
	if err != nil {
		t.Fatalf("Fork(last turn,last n) error = %v", err)
	}
	if got := itemIDs(lastN.Items); !reflect.DeepEqual(got, []string{"item-3", "item-4"}) {
		t.Fatalf("Fork(last turn,last n) items = %v", got)
	}
	if got := lastN.Metadata.RolloutTurns; len(got) != 1 || got[0].Status != "completed" {
		t.Fatalf("Fork(last turn,last n) rollout turns = %#v", got)
	}
	if _, err := store.Fork("source", ForkOptions{NewID: "missing", Mode: ForkAll, LastTurnID: "turn-missing"}); !errors.Is(err, ErrInvalidThreadID) || !strings.Contains(err.Error(), "lastTurnId 'turn-missing' was not found in the source thread") {
		t.Fatalf("Fork(missing last turn) error = %v, want ErrInvalidThreadID", err)
	}
	if _, err := store.Fork("source", ForkOptions{NewID: "in-progress", Mode: ForkAll, LastTurnID: "turn-3"}); !errors.Is(err, ErrInvalidThreadID) || !strings.Contains(err.Error(), "lastTurnId 'turn-3' identifies an in-progress turn") {
		t.Fatalf("Fork(in-progress last turn) error = %v, want ErrInvalidThreadID", err)
	}
	if err := store.Save(&Record{ID: "legacy", Items: []Item{{ID: "legacy-item"}}}); err != nil {
		t.Fatalf("Save(legacy) error = %v", err)
	}
	if _, err := store.Fork("legacy", ForkOptions{NewID: "legacy-fork", Mode: ForkAll, LastTurnID: "turn-1"}); !errors.Is(err, ErrInvalidThreadID) || !strings.Contains(err.Error(), "lastTurnId 'turn-1' is not a persisted canonical turn in the source thread") {
		t.Fatalf("Fork(synthetic last turn) error = %v, want ErrInvalidThreadID", err)
	}
}

func TestStoreForkBeforeTurnIDPreservesSourceAndKeepsPrefixLikeRust(t *testing.T) {
	store := NewStore(t.TempDir())
	source := &Record{ID: "source", Metadata: Metadata{RolloutTurns: []TurnSnapshot{{ID: "turn-1", Status: "completed"}, {ID: "turn-2", Status: "completed"}}}, Items: []Item{
		{ID: "u1", Metadata: map[string]any{"turnId": "turn-1"}},
		{ID: "a1", Metadata: map[string]any{"turnId": "turn-1"}},
		{ID: "u2", Metadata: map[string]any{"turnId": "turn-2"}},
		{ID: "a2", Metadata: map[string]any{"turnId": "turn-2"}},
	}}
	if err := store.Save(source); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	forked, err := store.Fork("source", ForkOptions{NewID: "fork", Mode: ForkAll, BeforeTurnID: "turn-2"})
	if err != nil {
		t.Fatalf("Fork(before turn) error = %v", err)
	}
	if got := itemIDs(forked.Items); !reflect.DeepEqual(got, []string{"u1", "a1"}) {
		t.Fatalf("fork items = %v", got)
	}
	loaded, err := store.Read("source", true, true)
	if err != nil {
		t.Fatalf("Read(source) = %v", err)
	}
	if got := itemIDs(loaded.Items); !reflect.DeepEqual(got, []string{"u1", "a1", "u2", "a2"}) {
		t.Fatalf("source mutated = %v", got)
	}
	if _, err := store.Fork("source", ForkOptions{NewID: "missing", BeforeTurnID: "turn-missing"}); !errors.Is(err, ErrInvalidThreadID) || !strings.Contains(err.Error(), "beforeTurnId 'turn-missing' was not found") {
		t.Fatalf("missing error = %v", err)
	}
	if _, err := store.Fork("source", ForkOptions{NewID: "both", LastTurnID: "turn-1", BeforeTurnID: "turn-2"}); !errors.Is(err, ErrInvalidThreadID) || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("mutual exclusion error = %v", err)
	}
}

func TestStoreForkClonesStructuredItemFields(t *testing.T) {
	store := NewStore(t.TempDir())
	detail := "high"
	if err := store.Save(&Record{
		ID: "source",
		Items: []Item{{
			ID:      "item-1",
			Type:    "message",
			Content: []ContentPart{{Type: "input_text", Text: "hello", Detail: &detail}},
			Data:    map[string]any{"text": "hello"},
			Raw:     json.RawMessage(`{"type":"message"}`),
		}},
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	forked, err := store.Fork("source", ForkOptions{NewID: "fork", Mode: ForkAll})
	if err != nil {
		t.Fatalf("Fork() error = %v", err)
	}
	forked.Items[0].Content[0].Text = "mutated"
	forked.Items[0].Data["text"] = "mutated"
	forked.Items[0].Raw[0] = '['
	source, err := store.Read("source", true, true)
	if err != nil {
		t.Fatalf("Read(source) error = %v", err)
	}
	if source.Items[0].Content[0].Text != "hello" || source.Items[0].Data["text"] != "hello" || string(source.Items[0].Raw[:1]) != "{" {
		t.Fatalf("source item was mutated: %#v", source.Items[0])
	}
}

func TestStoreForkClonesMetadataRawFields(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Save(&Record{
		ID: "source",
		Metadata: Metadata{
			DynamicTools:            []json.RawMessage{json.RawMessage(`{"name":"demo"}`)},
			SelectedCapabilityRoots: []json.RawMessage{json.RawMessage(`{"id":"cap-root"}`)},
			ContextWindow:           json.RawMessage(`{"window_id":"window-1"}`),
			TurnContext:             json.RawMessage(`{"turn_id":"turn-1"}`),
			WorldState:              json.RawMessage(`{"full":true}`),
		},
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	forked, err := store.Fork("source", ForkOptions{NewID: "fork", Mode: ForkAll})
	if err != nil {
		t.Fatalf("Fork() error = %v", err)
	}
	forked.Metadata.DynamicTools[0][0] = '['
	forked.Metadata.SelectedCapabilityRoots[0][0] = '['
	forked.Metadata.ContextWindow[0] = '['
	forked.Metadata.TurnContext[0] = '['
	forked.Metadata.WorldState[0] = '['

	source, err := store.Read("source", true, true)
	if err != nil {
		t.Fatalf("Read(source) error = %v", err)
	}
	if string(source.Metadata.DynamicTools[0][:1]) != "{" || string(source.Metadata.SelectedCapabilityRoots[0][:1]) != "{" || string(source.Metadata.ContextWindow[:1]) != "{" || string(source.Metadata.TurnContext[:1]) != "{" || string(source.Metadata.WorldState[:1]) != "{" {
		t.Fatalf("source metadata raw fields were mutated: %#v", source.Metadata)
	}
}

func TestStoreDeleteRemovesThread(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	if err := store.Save(&Record{ID: "thread-1"}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := store.Delete("thread-1"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "thread-1.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat deleted file error = %v, want not exist", err)
	}
	if err := store.Delete("thread-1"); !errors.Is(err, ErrThreadNotFound) {
		t.Fatalf("Delete(missing) error = %v, want ErrThreadNotFound", err)
	}
}

func TestStoreRejectsPathTraversalIDs(t *testing.T) {
	store := NewStore(t.TempDir())
	for _, id := range []ThreadID{"", "../x", "..", "a/b", `a\b`, "a:b"} {
		if err := store.Save(&Record{ID: id}); !errors.Is(err, ErrInvalidThreadID) {
			t.Fatalf("Save(%q) error = %v, want ErrInvalidThreadID", id, err)
		}
	}
}

func fixedTime() time.Time {
	return time.Date(2026, 6, 29, 1, 2, 3, 0, time.UTC)
}

func ids(records []Record) []ThreadID {
	out := make([]ThreadID, len(records))
	for i := range records {
		out[i] = records[i].ID
	}
	return out
}

func itemIDs(items []Item) []string {
	out := make([]string, len(items))
	for i := range items {
		out[i] = items[i].ID
	}
	return out
}

func sameIDs(left []ThreadID, right []ThreadID) bool {
	if len(left) != len(right) {
		return false
	}
	seen := make(map[ThreadID]int, len(left))
	for _, id := range left {
		seen[id]++
	}
	for _, id := range right {
		if seen[id] == 0 {
			return false
		}
		seen[id]--
	}
	return true
}
