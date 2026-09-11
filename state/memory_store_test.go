package state

import (
	"context"
	"testing"
	"time"
)

// Mirrors Rust memory_versions_tests: the v2 memory store is created lazily,
// isolates jobs/outputs, and shares the source thread catalog.
func TestMemoryStoreForVersionIsolatesStateAndSharesThreadCatalog(t *testing.T) {
	ctx := context.Background()
	runtime := newBackfillTestRuntime(t)
	updated := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	insertMemoryThread(t, runtime, "shared-thread", updated, "enabled", "cli", false, "remember this")

	if runtime.HasMemoriesV2Database() {
		t.Fatal("v2 memories database must not exist before first use")
	}
	if v1, err := runtime.MemoryStoreForVersion(ctx, "v1"); err != nil || v1 != runtime {
		t.Fatalf("v1 store = %p, %v; want the runtime itself", v1, err)
	}

	store, err := runtime.MemoryStoreForVersion(ctx, "v2")
	if err != nil {
		t.Fatalf("MemoryStoreForVersion(v2) error = %v", err)
	}
	if store == runtime {
		t.Fatal("v2 store must not be the v1 runtime")
	}
	if store.MemoriesDB() == runtime.MemoriesDB() {
		t.Fatal("v2 store must use an isolated memories database")
	}
	if store.StateDB() != runtime.StateDB() {
		t.Fatal("v2 store must share the thread catalog state database")
	}
	if !runtime.HasMemoriesV2Database() {
		t.Fatal("v2 memories database must exist after first use")
	}

	// The shared catalog makes the thread claimable, but its jobs/outputs live
	// in the isolated v2 store.
	claims, err := store.ClaimStage1JobsForStartup(ctx, "current-thread", Stage1StartupClaimParams{
		ScanLimit: 10, MaxClaimed: 4, MaxAgeDays: 10, MinRolloutIdleHours: 1, LeaseSeconds: 3600,
	})
	if err != nil {
		t.Fatalf("v2 claim error = %v", err)
	}
	if len(claims) != 1 || claims[0].Thread.ID != "shared-thread" {
		t.Fatalf("v2 claims = %#v", claims)
	}
	if updated, err := store.MarkStage1JobSucceeded(ctx, "shared-thread", claims[0].OwnershipToken, updated.Unix(), "raw", "summary", nil); err != nil || !updated {
		t.Fatalf("v2 mark succeeded = %v, %v", updated, err)
	}
	assertMemoryTableCount(t, store.MemoriesDB(), "stage1_outputs", "thread_id", "shared-thread", 1)
	assertMemoryTableCount(t, runtime.MemoriesDB(), "stage1_outputs", "thread_id", "shared-thread", 0)
	assertMemoryTableCount(t, store.MemoriesDB(), "jobs", "job_key", "shared-thread", 1)
	assertMemoryTableCount(t, runtime.MemoriesDB(), "jobs", "job_key", "shared-thread", 0)

	// A store view shares the owning runtime's handles, so closing it is a no-op
	// and the runtime stays usable.
	if err := store.Close(); err != nil {
		t.Fatalf("store.Close() error = %v", err)
	}
	if runtime.StateDB() == nil || runtime.MemoriesDB() == nil {
		t.Fatal("closing a store view must not close the shared databases")
	}
	if _, err := runtime.MemoryStoreForVersion(ctx, "v2"); err != nil {
		t.Fatalf("v2 store after view close error = %v", err)
	}
}

func TestClearAllMemoryDataCoversEveryVersion(t *testing.T) {
	ctx := context.Background()
	runtime := newBackfillTestRuntime(t)
	insertMemoryThread(t, runtime, "versioned-thread", time.Now().UTC(), "enabled", "cli", false, "preview")
	store, err := runtime.MemoryStoreForVersion(ctx, "v2")
	if err != nil {
		t.Fatalf("v2 store error = %v", err)
	}
	insertMemoryOutput(t, runtime, "versioned-thread", 100, "raw", "summary", 0, nil, false)
	insertMemoryOutput(t, store, "versioned-thread", 100, "raw", "summary", 0, nil, false)
	assertMemoryTableCount(t, runtime.MemoriesDB(), "stage1_outputs", "thread_id", "versioned-thread", 1)
	assertMemoryTableCount(t, store.MemoriesDB(), "stage1_outputs", "thread_id", "versioned-thread", 1)

	if err := runtime.ClearAllMemoryData(ctx); err != nil {
		t.Fatalf("ClearAllMemoryData() error = %v", err)
	}
	assertMemoryTableCount(t, runtime.MemoriesDB(), "stage1_outputs", "thread_id", "versioned-thread", 0)
	assertMemoryTableCount(t, store.MemoriesDB(), "stage1_outputs", "thread_id", "versioned-thread", 0)
	if !runtime.HasMemoriesV2Database() {
		t.Fatal("clearing memories must not remove the v2 database file")
	}

	// Resetting a runtime that never used v2 must not create the v2 database.
	fresh := newBackfillTestRuntime(t)
	if err := fresh.ClearAllMemoryData(ctx); err != nil {
		t.Fatalf("fresh ClearAllMemoryData() error = %v", err)
	}
	if fresh.HasMemoriesV2Database() {
		t.Fatal("reset must not create a v2 memories database")
	}
}

func TestDeleteVersionedThreadMemoryCoversEveryVersion(t *testing.T) {
	ctx := context.Background()
	runtime := newBackfillTestRuntime(t)
	insertMemoryThread(t, runtime, "delete-thread", time.Now().UTC(), "enabled", "cli", false, "preview")
	store, err := runtime.MemoryStoreForVersion(ctx, "v2")
	if err != nil {
		t.Fatalf("v2 store error = %v", err)
	}
	insertMemoryOutput(t, runtime, "delete-thread", 100, "raw", "summary", 0, nil, false)
	insertMemoryOutput(t, store, "delete-thread", 100, "raw", "summary", 0, nil, false)
	assertMemoryTableCount(t, runtime.MemoriesDB(), "stage1_outputs", "thread_id", "delete-thread", 1)
	assertMemoryTableCount(t, store.MemoriesDB(), "stage1_outputs", "thread_id", "delete-thread", 1)

	if err := runtime.DeleteVersionedThreadMemory(ctx, "delete-thread"); err != nil {
		t.Fatalf("DeleteVersionedThreadMemory() error = %v", err)
	}
	assertMemoryTableCount(t, runtime.MemoriesDB(), "stage1_outputs", "thread_id", "delete-thread", 0)
	assertMemoryTableCount(t, store.MemoriesDB(), "stage1_outputs", "thread_id", "delete-thread", 0)

	fresh := newBackfillTestRuntime(t)
	if err := fresh.DeleteVersionedThreadMemory(ctx, "missing-thread"); err != nil {
		t.Fatalf("fresh DeleteVersionedThreadMemory() error = %v", err)
	}
	if fresh.HasMemoriesV2Database() {
		t.Fatal("deleting thread memory must not create a v2 memories database")
	}
}
