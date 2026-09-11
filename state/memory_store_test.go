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
