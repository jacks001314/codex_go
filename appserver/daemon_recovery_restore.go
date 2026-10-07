package appserver

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"

	"codex_go/daemonrecovery"
)

// Managed daemon recovery: the startup-time consumer and thread restorer.
//
// Rust app-server/src/daemon_thread_recovery.rs:32 `start_recovery(path,
// processor)` runs before the server serves requests: it reads the snapshot,
// removes the file unconditionally ("Even malformed or temporarily unreadable
// snapshots belong to this generation only"), and returns a background task
// that calls `MessageProcessor::restore_daemon_threads`
// (message_processor.rs:778). That loop, for every loaded thread, takes a
// turn-admission permit and resumes the thread with `exclude_turns: true`,
// warning and continuing on failure. Only a managed daemon does any of this
// (app-server/src/lib.rs:1017).
//
// Structural difference from Rust (accepted as 方案 ②): Rust restores into the
// single daemon-level MessageProcessor/ThreadManager. Go builds a RuntimeRouter
// per connection, so Phase B creates one daemon-lifetime RuntimeRouter that
// owns the restored threads; a client that resumes one of them attaches to the
// shared on-disk state instead of to this router. Making the connection routers
// share this daemon-level owner is the registered "daemon-level thread owner /
// cross-connection shared router" structural item; Rust's
// `turn_admission.admit()` bound has no Go equivalent, so the restore loop is
// sequential (concurrency 1) as its own bound.

// daemonRecoveryConsume mirrors the read+remove half of Rust `start_recovery`
// (app-server/src/daemon_thread_recovery.rs:32): read the snapshot, then remove
// the file regardless of the read outcome. A missing file is an empty snapshot;
// the remove error is returned only when it is not ErrNotExist, and a read
// error is returned after the file has already been cleared. Either error means
// this generation restores nothing.
func daemonRecoveryConsume(codexHome string) (daemonrecovery.Snapshot, error) {
	path := daemonrecovery.FilePath(codexHome)
	snapshot, readErr := daemonrecovery.ReadSnapshot(path)
	// Even malformed or temporarily unreadable snapshots belong to this
	// generation only (Rust daemon_thread_recovery.rs:32), so the file is
	// removed even when the read failed.
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return daemonrecovery.Snapshot{}, fmt.Errorf("clear daemon recovery snapshot: %w", err)
	}
	if readErr != nil {
		return daemonrecovery.Snapshot{}, fmt.Errorf("read daemon recovery snapshot: %w", readErr)
	}
	return snapshot, nil
}

// daemonRecoveryRestore owns the daemon-lifetime thread owner a managed daemon
// restores the previous generation's loaded threads into.
type daemonRecoveryRestore struct {
	router *RuntimeRouter
	wg     sync.WaitGroup
}

// startDaemonRecoveryRestoreForAccess consumes the handoff and begins the
// background restore for a managed daemon. Every other server gets nil: it
// reads nothing, removes nothing and restores nothing, so an unmanaged
// app-server leaves the previous generation's file untouched.
//
// The consume happens synchronously (Rust consumes the handoff before serving),
// while the resume loop runs in the background so a slow restore never delays
// readiness (Rust daemon_thread_recovery.rs:32 spawns the restore task).
func startDaemonRecoveryRestoreForAccess(
	access DaemonShutdownAccess,
	codexHome string,
	newRouter func() *RuntimeRouter,
) *daemonRecoveryRestore {
	if access != DaemonShutdownManaged {
		return nil
	}
	restore := &daemonRecoveryRestore{}
	snapshot, err := daemonRecoveryConsume(codexHome)
	if err != nil {
		slog.Warn("failed to consume daemon recovery snapshot", "error", err, "path", daemonrecovery.FilePath(codexHome))
		return restore
	}
	if len(snapshot.Loaded) == 0 {
		return restore
	}
	restore.router = newRouter()
	restore.wg.Add(1)
	go func() {
		defer restore.wg.Done()
		restore.router.restoreDaemonThreads(snapshot)
	}()
	return restore
}

// close waits for the background restore, then releases the daemon-lifetime
// router. A nil receiver (unmanaged server) is a no-op.
func (r *daemonRecoveryRestore) close() {
	if r == nil {
		return
	}
	r.wg.Wait()
	if r.router != nil {
		_ = r.router.Close()
		r.router = nil
	}
}

// restoreDaemonThreads mirrors Rust `MessageProcessor::restore_daemon_threads`
// (app-server/src/message_processor.rs:778): resume every saved thread with
// `exclude_turns: true`. The loop is sequential, which replaces Rust's
// turn-admission permit as the bound, and a single thread's failure only warns
// and continues.
func (r *RuntimeRouter) restoreDaemonThreads(snapshot daemonrecovery.Snapshot) {
	if r == nil {
		return
	}
	for _, threadID := range snapshot.Loaded {
		threadID = strings.TrimSpace(threadID)
		if threadID == "" {
			continue
		}
		if _, err := r.resumeDaemonThread(threadID); err != nil {
			slog.Warn("failed to restore saved daemon thread", "thread_id", threadID, "error", err)
		}
	}
}

// resumeDaemonThread is one iteration of the restore loop: the same internal
// resume path as the RPC, with `exclude_turns` so the restored thread is loaded
// without re-materializing its turns (the interrupted turn continues in Phase C,
// Rust request_processors/thread_processor.rs:3690/4015).
func (r *RuntimeRouter) resumeDaemonThread(threadID string) (*ThreadResumeResponse, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil, fmt.Errorf("%w: thread id is empty", ErrInvalidRequest)
	}
	params := &ThreadResumeParams{ThreadID: threadID, ExcludeTurns: true}
	request, err := internalRequest(MethodThreadResume, "daemon-recovery-"+threadID, params)
	if err != nil {
		return nil, err
	}
	response := r.Handle(request)
	if response == nil {
		return nil, errors.New("daemon recovery resume produced no response")
	}
	if response.Error != nil {
		return nil, fmt.Errorf("daemon recovery resume failed: %s (code %d)", response.Error.Message, response.Error.Code)
	}
	resumed, _ := response.Result.(*ThreadResumeResponse)
	return resumed, nil
}
