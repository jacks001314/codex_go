package session

import (
	"testing"
)

// TestPreparedForkInheritsRootSessionIDLikeRust mirrors Rust #62314: a prepared
// fork that references the source's stored history keeps the root session id,
// while a copied fork starts its own session.
func TestPreparedForkInheritsRootSessionIDLikeRust(t *testing.T) {
	store := NewStore(t.TempDir())
	source := paginatedForkSourceFixture(t, store)

	referencing, err := store.PrepareFork(source, PrepareForkParams{Mode: ForkAll})
	if err != nil {
		t.Fatalf("PrepareFork(referencing) error = %v", err)
	}
	referencing.WithSourceRevalidation()
	child, err := store.FinalizePreparedFork(source, referencing, ForkOptions{NewID: "child-ref", Mode: ForkAll})
	if err != nil {
		t.Fatalf("FinalizePreparedFork(referencing) error = %v", err)
	}
	if child.SessionID != source.SessionID {
		t.Fatalf("referencing fork session = %q, want the root session %q", child.SessionID, source.SessionID)
	}

	copied, err := store.PrepareFork(source, PrepareForkParams{Mode: ForkAll, CopyHistory: true})
	if err != nil {
		t.Fatalf("PrepareFork(copied) error = %v", err)
	}
	if !copied.CopiedHistory || copied.HistoryBase != nil {
		t.Fatalf("copied fork = %#v, want its own copied history", copied)
	}
	child, err = store.FinalizePreparedFork(source, copied, ForkOptions{NewID: "child-copy", Mode: ForkAll})
	if err != nil {
		t.Fatalf("FinalizePreparedFork(copied) error = %v", err)
	}
	if child.SessionID != "child-copy" {
		t.Fatalf("copied fork session = %q, want its own id", child.SessionID)
	}

	explicit, err := store.PrepareFork(source, PrepareForkParams{Mode: ForkAll})
	if err != nil {
		t.Fatalf("PrepareFork(explicit) error = %v", err)
	}
	explicit.WithSourceRevalidation()
	child, err = store.FinalizePreparedFork(source, explicit, ForkOptions{NewID: "child-explicit", SessionID: "session-explicit", Mode: ForkAll})
	if err != nil {
		t.Fatalf("FinalizePreparedFork(explicit) error = %v", err)
	}
	if child.SessionID != "session-explicit" {
		t.Fatalf("explicit session = %q, want the caller's session", child.SessionID)
	}
}
