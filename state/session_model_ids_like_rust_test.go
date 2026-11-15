package state

import (
	"context"
	"sync"
	"testing"

	"codex_go/modelfacingid"
)

func TestConcurrentSessionModelIDAssignmentsShareADurableAliasLikeRust(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	config := mustSQLiteConfig(t, home)
	first, err := InitStateRuntime(ctx, config, "test")
	if err != nil {
		t.Fatalf("InitStateRuntime(first) error = %v", err)
	}
	defer first.Close()
	second, err := InitStateRuntime(ctx, config, "test")
	if err != nil {
		t.Fatalf("InitStateRuntime(second) error = %v", err)
	}
	defer second.Close()

	session := "session-model-ids"
	key, err := modelfacingid.NewModelIDKey(modelfacingid.RegisteredEnvironmentID("first"))
	if err != nil {
		t.Fatalf("NewModelIDKey() error = %v", err)
	}
	var left, right modelfacingid.ID
	var leftErr, rightErr error
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		left, leftErr = first.GetOrAssign(ctx, session, key)
	}()
	go func() {
		defer wait.Done()
		right, rightErr = second.GetOrAssign(ctx, session, key)
	}()
	wait.Wait()
	if leftErr != nil || rightErr != nil {
		t.Fatalf("concurrent GetOrAssign() errors = %v, %v", leftErr, rightErr)
	}
	if left != right {
		t.Fatalf("concurrent writers assigned %v and %v", left, right)
	}

	otherKey, err := modelfacingid.NewModelIDKey(modelfacingid.RegisteredEnvironmentID("second"))
	if err != nil {
		t.Fatalf("NewModelIDKey(second) error = %v", err)
	}
	next, err := second.GetOrAssign(ctx, session, otherKey)
	if err != nil {
		t.Fatalf("GetOrAssign(second) error = %v", err)
	}
	if next == left {
		t.Fatal("different external keys received the same alias")
	}
	lookedUp, err := first.Lookup(ctx, session, key)
	if err != nil || lookedUp == nil || *lookedUp != left {
		t.Fatalf("Lookup(first) = %v, %v, want %v", lookedUp, err, left)
	}
	maps, err := first.Snapshot(ctx, session)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if len(maps) != 1 || len(maps[0].Keys) != 2 || maps[0].Keys[0] != key.Key || maps[0].Keys[1] != otherKey.Key {
		t.Fatalf("Snapshot() = %#v", maps)
	}

	if err := first.Initialize(ctx, "forked", maps); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	forked, err := second.Lookup(ctx, "forked", otherKey)
	if err != nil || forked == nil || *forked != next {
		t.Fatalf("forked Lookup() = %v, %v, want %v", forked, err, next)
	}
}

func TestDeleteThreadsRemovesSessionModelIDsLikeRust(t *testing.T) {
	ctx := context.Background()
	runtime, err := InitStateRuntime(ctx, mustSQLiteConfig(t, t.TempDir()), "test")
	if err != nil {
		t.Fatalf("InitStateRuntime() error = %v", err)
	}
	defer runtime.Close()
	key, err := modelfacingid.NewModelIDKey(modelfacingid.RegisteredEnvironmentID("first"))
	if err != nil {
		t.Fatalf("NewModelIDKey() error = %v", err)
	}
	if _, err := runtime.GetOrAssign(ctx, "deleted-session", key); err != nil {
		t.Fatalf("GetOrAssign() error = %v", err)
	}
	if _, err := runtime.DeleteThreadsStrict(ctx, []string{"deleted-session"}); err != nil {
		t.Fatalf("DeleteThreadsStrict() error = %v", err)
	}
	lookedUp, err := runtime.Lookup(ctx, "deleted-session", key)
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if lookedUp != nil {
		t.Fatalf("deleted session kept alias %v", lookedUp)
	}
}

func TestMemorySessionModelIDStoreMatchesSQLiteSemantics(t *testing.T) {
	ctx := context.Background()
	store := NewMemorySessionModelIDStore()
	key, err := modelfacingid.NewModelIDKey(modelfacingid.RegisteredEnvironmentID("first"))
	if err != nil {
		t.Fatalf("NewModelIDKey() error = %v", err)
	}
	assigned, err := store.GetOrAssign(ctx, "session", key)
	if err != nil {
		t.Fatalf("GetOrAssign() error = %v", err)
	}
	repeated, err := store.GetOrAssign(ctx, "session", key)
	if err != nil || repeated != assigned {
		t.Fatalf("repeated GetOrAssign() = %v, %v, want %v", repeated, err, assigned)
	}
	maps, err := store.Snapshot(ctx, "session")
	if err != nil || len(maps) != 1 || len(maps[0].Keys) != 1 || maps[0].Keys[0] != key.Key {
		t.Fatalf("Snapshot() = %#v, %v", maps, err)
	}
	if err := store.Initialize(ctx, "forked", maps); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	forked, err := store.Lookup(ctx, "forked", key)
	if err != nil || forked == nil || *forked != assigned {
		t.Fatalf("forked Lookup() = %v, %v, want %v", forked, err, assigned)
	}
	store.Remove("session")
	removed, err := store.Lookup(ctx, "session", key)
	if err != nil || removed != nil {
		t.Fatalf("removed Lookup() = %v, %v", removed, err)
	}
}
