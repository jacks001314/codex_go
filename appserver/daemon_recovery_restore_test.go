package appserver

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"codex_go/daemonrecovery"
	"codex_go/session"
)

// Rust app-server/src/daemon_thread_recovery.rs:32 start_recovery consumes the
// handoff before serving requests: it reads the snapshot and removes the file
// regardless of the read outcome ("Even malformed or temporarily unreadable
// snapshots belong to this generation only"). MessageProcessor::
// restore_daemon_threads (message_processor.rs:778) then resumes every loaded
// thread with `exclude_turns: true`. Only a managed daemon does this
// (app-server/src/lib.rs:1017).

// seedDaemonRecoveryThread materializes one root thread with a rollout on disk
// under home, so a later generation can resume it by id.
func seedDaemonRecoveryThread(t *testing.T, home string) string {
	t.Helper()
	store := session.NewStore(filepath.Join(home, "sessions"))
	router := NewRouter(store)
	now := fixedTime()
	threadID := "thread-daemon-recovery"
	record := &session.Record{
		ID:        session.ThreadID(threadID),
		SessionID: threadID,
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata: session.Metadata{
			CWD:           t.TempDir(),
			SessionPrefix: session.PrefixForSessionID(threadID),
		},
		// One recorded user message, so a resume that loads history has a turn to
		// load and `exclude_turns` is observably different.
		Items: []session.Item{{ID: "user-turn-1", Type: "message", Role: "user", Text: "hello"}},
	}
	if err := store.Create(record); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}
	if err := router.createThreadRollout(record, now); err != nil {
		t.Fatalf("createThreadRollout() error = %v", err)
	}
	return threadID
}

func daemonRecoveryHomeRouter(home string) *RuntimeRouter {
	return NewUnixSocketRouterWithOptions(home, nil)
}

func TestDaemonRecoveryConsumeReadsAndRemovesSnapshotLikeRust(t *testing.T) {
	home := t.TempDir()
	path := daemonrecovery.FilePath(home)
	if err := daemonrecovery.WriteSnapshot(path, daemonrecovery.Snapshot{Loaded: []string{"thread-a", "thread-b"}}); err != nil {
		t.Fatalf("WriteSnapshot() error = %v", err)
	}

	snapshot, err := daemonRecoveryConsume(home)
	if err != nil {
		t.Fatalf("daemonRecoveryConsume() error = %v", err)
	}
	if len(snapshot.Loaded) != 2 || snapshot.Loaded[0] != "thread-a" || snapshot.Loaded[1] != "thread-b" {
		t.Fatalf("Loaded = %#v, want both candidates", snapshot.Loaded)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recovery file stat error = %v, want the file to be consumed", err)
	}
}

func TestDaemonRecoveryConsumeRemovesMalformedSnapshotLikeRust(t *testing.T) {
	home := t.TempDir()
	path := daemonrecovery.FilePath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	snapshot, err := daemonRecoveryConsume(home)
	if err == nil {
		t.Fatal("daemonRecoveryConsume() error = nil, want a read error")
	}
	if len(snapshot.Loaded) != 0 {
		t.Fatalf("Loaded = %#v, want no candidates after a malformed read", snapshot.Loaded)
	}
	// Malformed snapshots still belong to this generation only.
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("recovery file stat error = %v, want the malformed file removed", statErr)
	}
}

func TestDaemonRecoveryConsumeMissingFileIsEmptyLikeRust(t *testing.T) {
	home := t.TempDir()
	snapshot, err := daemonRecoveryConsume(home)
	if err != nil {
		t.Fatalf("daemonRecoveryConsume() error = %v", err)
	}
	if len(snapshot.Loaded) != 0 {
		t.Fatalf("Loaded = %#v, want no candidates for a missing file", snapshot.Loaded)
	}
}

// A missing snapshot resumes nothing: no daemon router is built.
func TestDaemonRecoveryRestoreMissingFileIsNoopLikeRust(t *testing.T) {
	home := t.TempDir()
	restore := startDaemonRecoveryRestoreForAccess(DaemonShutdownManaged, home, func() *RuntimeRouter {
		t.Fatal("built a router for an empty candidate set")
		return nil
	})
	if restore == nil {
		t.Fatal("managed restore = nil, want a restore owner")
	}
	if restore.router != nil {
		t.Fatal("restore router = non-nil, want no resume for a missing snapshot")
	}
	restore.close()
}

// Only a managed daemon consumes the handoff: an unmanaged app-server reads and
// removes nothing, so the file is left for the managed owner.
func TestDaemonRecoveryRestoreForAccessOnlyManagedLikeRust(t *testing.T) {
	home := t.TempDir()
	path := daemonrecovery.FilePath(home)
	if err := daemonrecovery.WriteSnapshot(path, daemonrecovery.Snapshot{Loaded: []string{"thread-a"}}); err != nil {
		t.Fatalf("WriteSnapshot() error = %v", err)
	}

	if restore := startDaemonRecoveryRestoreForAccess(DaemonShutdownDisabled, home, func() *RuntimeRouter {
		return daemonRecoveryHomeRouter(home)
	}); restore != nil {
		t.Fatalf("unmanaged restore = %#v, want nil", restore)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("recovery file stat error = %v, want an unmanaged server to leave it untouched", err)
	}

	restore := startDaemonRecoveryRestoreForAccess(DaemonShutdownManaged, home, func() *RuntimeRouter {
		return daemonRecoveryHomeRouter(home)
	})
	if restore == nil {
		t.Fatal("managed restore = nil, want a restore owner")
	}
	restore.wg.Wait()
	restore.close()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recovery file stat error = %v, want the managed owner to consume it", err)
	}
}

// A managed daemon resumes each saved thread into its daemon-lifetime router.
func TestDaemonRecoveryRestoreResumesSavedThreadsLikeRust(t *testing.T) {
	home := t.TempDir()
	threadID := seedDaemonRecoveryThread(t, home)
	path := daemonrecovery.FilePath(home)
	if err := daemonrecovery.WriteSnapshot(path, daemonrecovery.Snapshot{Loaded: []string{threadID}}); err != nil {
		t.Fatalf("WriteSnapshot() error = %v", err)
	}

	restore := startDaemonRecoveryRestoreForAccess(DaemonShutdownManaged, home, func() *RuntimeRouter {
		return daemonRecoveryHomeRouter(home)
	})
	if restore == nil || restore.router == nil {
		t.Fatal("restore router = nil, want a daemon-lifetime owner")
	}
	restore.wg.Wait()
	if !restore.router.threads.HasLiveThread(session.ThreadID(threadID)) {
		t.Fatal("saved thread was not restored into the daemon router")
	}
	restore.close()
}

// The restore resumes with `exclude_turns: true`, so the restored thread keeps
// its metadata without re-materializing its turns.
func TestDaemonRecoveryResumeUsesExcludeTurnsLikeRust(t *testing.T) {
	home := t.TempDir()
	threadID := seedDaemonRecoveryThread(t, home)
	router := daemonRecoveryHomeRouter(home)
	defer router.Close()

	excluded, err := router.resumeDaemonThread(threadID)
	if err != nil {
		t.Fatalf("resumeDaemonThread() error = %v", err)
	}
	if excluded == nil || excluded.Thread == nil {
		t.Fatalf("resume result = %#v, want a resumed thread", excluded)
	}
	if len(excluded.Thread.Turns) != 0 {
		t.Fatalf("resumed turns = %d, want exclude_turns to load none", len(excluded.Thread.Turns))
	}

	// Control: without exclude_turns the same thread loads its turns, proving the
	// assertion above is anchored on the flag and not on an empty rollout.
	full := router.Handle(requestWithParams(t, IntID(99), MethodThreadResume, ThreadResumeParams{ThreadID: threadID}))
	if full.Error != nil {
		t.Fatalf("full resume error = %v", full.Error)
	}
	fullResumed, _ := full.Result.(*ThreadResumeResponse)
	if fullResumed == nil || fullResumed.Thread == nil || len(fullResumed.Thread.Turns) == 0 {
		t.Fatalf("control resume turns = %#v, want the recorded turn to load", fullResumed)
	}
}
