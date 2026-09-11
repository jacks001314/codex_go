package state

// Rust parity: codex-rs/state/src/runtime/memory_versions.rs (#43797). Memory
// jobs and outputs are scoped to the selected memory version, while the source
// thread catalog is shared. The v2 store is created on demand.

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
)

// MemoryStore is a memory-version-scoped view over the runtime: it reads and
// writes memory jobs/outputs through the version's memories database while
// sharing the state database (thread catalog).
type MemoryStore = StateRuntime

// MemoryStoreForVersion returns the memory store for a version. An empty or
// `v1` version returns the runtime itself; `v2` lazily opens the isolated v2
// memories database and shares the thread catalog.
func (r *StateRuntime) MemoryStoreForVersion(ctx context.Context, version string) (*MemoryStore, error) {
	if r == nil {
		return nil, errors.New("state runtime is unavailable")
	}
	if !strings.EqualFold(strings.TrimSpace(version), "v2") {
		return r, nil
	}
	db, err := r.memoriesV2Database(ctx)
	if err != nil {
		return nil, err
	}
	return &StateRuntime{
		sqlite:          r.sqlite,
		defaultProvider: r.defaultProvider,
		stateDB:         r.stateDB,
		memoriesDB:      db,
		metrics:         r.metrics,
	}, nil
}

func (r *StateRuntime) memoriesV2Database(ctx context.Context) (*sql.DB, error) {
	r.memoriesV2Mu.Lock()
	defer r.memoriesV2Mu.Unlock()
	if r.memoriesV2DB != nil {
		return r.memoriesV2DB, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	db, err := r.sqlite.OpenMemoriesV2DB(ctx)
	if err != nil {
		return nil, err
	}
	r.memoriesV2DB = db
	return db, nil
}

// HasMemoriesV2Database reports whether the isolated v2 memories database has
// been created on disk; reset and thread deletion cover it only when it exists.
func (r *StateRuntime) HasMemoriesV2Database() bool {
	if r == nil {
		return false
	}
	_, err := os.Stat(r.sqlite.MemoriesV2DBPath())
	return err == nil
}

// ClearAllMemoryData clears memory rows for every existing version without
// creating a v2 database that was never used (Rust memory_versions.rs
// clear_all_memory_data, #43797).
func (r *StateRuntime) ClearAllMemoryData(ctx context.Context) error {
	if r == nil {
		return errors.New("state runtime is unavailable")
	}
	if err := r.ClearMemoryData(ctx); err != nil {
		return err
	}
	if !r.HasMemoriesV2Database() {
		return nil
	}
	store, err := r.MemoryStoreForVersion(ctx, "v2")
	if err != nil {
		return err
	}
	return store.ClearMemoryData(ctx)
}

// DeleteVersionedThreadMemory removes one thread's memory rows from every
// existing version (Rust delete_versioned_thread_memory, #43797).
func (r *StateRuntime) DeleteVersionedThreadMemory(ctx context.Context, threadID string) error {
	if r == nil {
		return errors.New("state runtime is unavailable")
	}
	if err := r.deleteThreadMemory(ctx, threadID); err != nil {
		return err
	}
	if !r.HasMemoriesV2Database() {
		return nil
	}
	store, err := r.MemoryStoreForVersion(ctx, "v2")
	if err != nil {
		return err
	}
	return store.deleteThreadMemory(ctx, threadID)
}
