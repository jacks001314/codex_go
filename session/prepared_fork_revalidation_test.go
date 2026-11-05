package session

import (
	"errors"
	"testing"
	"time"
)

type forkReservationFunc func()

func (f forkReservationFunc) Release() {
	f()
}

func paginatedForkSourceFixture(t *testing.T, store *Store) *Record {
	t.Helper()
	now := fixedTime()
	source := &Record{
		ID:        "source",
		SessionID: "session-1",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata: Metadata{
			HistoryMode:  "paginated",
			RolloutTurns: []TurnSnapshot{{ID: "turn-1", Status: "completed"}},
		},
		Items: []Item{{
			ID: "item-1", Type: "user_message", Role: "user", Text: "hello", CreatedAt: now,
			Metadata: map[string]any{"turnId": "turn-1"},
		}},
	}
	if err := store.Create(source); err != nil {
		t.Fatalf("Create(source) error = %v", err)
	}
	return source
}

func TestFinalizePreparedForkRevalidatesDeletedSource(t *testing.T) {
	store := NewStore(t.TempDir())
	source := paginatedForkSourceFixture(t, store)
	prepared, err := store.PrepareFork(source, PrepareForkParams{Mode: ForkAll})
	if err != nil {
		t.Fatalf("PrepareFork() error = %v", err)
	}
	if prepared.HistoryBase == nil {
		t.Fatal("prepared fork has no source history reference")
	}
	prepared.WithSourceRevalidation()
	if err := store.Delete(source.ID); err != nil {
		t.Fatalf("Delete(source) error = %v", err)
	}
	if _, err := store.FinalizePreparedFork(source, prepared, ForkOptions{NewID: "child", Mode: ForkAll}); !errors.Is(err, ErrThreadNotFound) {
		t.Fatalf("FinalizePreparedFork(deleted source) error = %v, want ErrThreadNotFound", err)
	}
	if _, err := store.Load("child"); !errors.Is(err, ErrThreadNotFound) {
		t.Fatalf("Load(child) error = %v, want ErrThreadNotFound", err)
	}
}

func TestFinalizePreparedForkRejectsRecreatedSource(t *testing.T) {
	store := NewStore(t.TempDir())
	source := paginatedForkSourceFixture(t, store)
	prepared, err := store.PrepareFork(source, PrepareForkParams{Mode: ForkAll})
	if err != nil {
		t.Fatalf("PrepareFork() error = %v", err)
	}
	prepared.WithSourceRevalidation()
	if err := store.Delete(source.ID); err != nil {
		t.Fatalf("Delete(source) error = %v", err)
	}
	recreated := *source
	recreated.CreatedAt = source.CreatedAt.Add(time.Minute)
	recreated.UpdatedAt = recreated.CreatedAt
	recreated.RecencyAt = recreated.CreatedAt
	if err := store.Create(&recreated); err != nil {
		t.Fatalf("Create(recreated source) error = %v", err)
	}
	if _, err := store.FinalizePreparedFork(source, prepared, ForkOptions{NewID: "child", Mode: ForkAll}); !errors.Is(err, ErrConflict) {
		t.Fatalf("FinalizePreparedFork(recreated source) error = %v, want ErrConflict", err)
	}
}

func TestFinalizePreparedForkReleasesSourceReservationAfterDurableChild(t *testing.T) {
	store := NewStore(t.TempDir())
	source := paginatedForkSourceFixture(t, store)
	prepared, err := store.PrepareFork(source, PrepareForkParams{Mode: ForkAll})
	if err != nil {
		t.Fatalf("PrepareFork() error = %v", err)
	}
	releasedAfterDurableChild := false
	prepared.WithSourceReservation(forkReservationFunc(func() {
		if _, err := store.Load("child"); err == nil {
			releasedAfterDurableChild = true
		}
	}))
	forked, err := store.FinalizePreparedFork(source, prepared, ForkOptions{NewID: "child", Mode: ForkAll})
	if err != nil {
		t.Fatalf("FinalizePreparedFork() error = %v", err)
	}
	if forked == nil || forked.HistoryBase == nil || forked.HistoryBase.ThreadID != source.ID {
		t.Fatalf("forked history base = %#v", forked)
	}
	if !releasedAfterDurableChild {
		t.Fatal("source reservation released before the child became durable")
	}
}

func TestFinalizePreparedForkRequiresSourceProtection(t *testing.T) {
	store := NewStore(t.TempDir())
	source := paginatedForkSourceFixture(t, store)
	prepared, err := store.PrepareFork(source, PrepareForkParams{Mode: ForkAll})
	if err != nil {
		t.Fatalf("PrepareFork() error = %v", err)
	}
	if _, err := store.FinalizePreparedFork(source, prepared, ForkOptions{NewID: "child", Mode: ForkAll}); !errors.Is(err, ErrInvalidThreadID) {
		t.Fatalf("FinalizePreparedFork() error = %v, want ErrInvalidThreadID", err)
	}
}
