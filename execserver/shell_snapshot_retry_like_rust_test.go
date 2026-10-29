package execserver

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"codex_go/shell"
	"codex_go/utils"
)

func snapshotRetryParams(t *testing.T, scope string) *ExecParams {
	t.Helper()
	cwd, err := utils.FromHostNativePath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &ExecParams{
		ProcessID: "snapshot-retry",
		Argv:      []string{"/bin/bash", "-lc", "echo ok"},
		CWD:       cwd.String(),
		ShellSnapshot: &ShellSnapshotRequest{
			ScopeID: scope,
			Shell:   ShellInfo{Name: "bash", Path: "/bin/bash"},
		},
		Env: map[string]string{"PATH": "/bin"},
	}
}

func snapshotRetryCapture(calls *atomic.Int64, failFirst int64) func(context.Context, []string, string, map[string]string) ([]byte, error) {
	return func(context.Context, []string, string, map[string]string) ([]byte, error) {
		if calls.Add(1) <= failFirst {
			return nil, errors.New("transient profile failure")
		}
		return []byte("# Snapshot file\nexport TOKEN=\"value\"\n\x00\x00\x00"), nil
	}
}

// TestShellSnapshotRetriesWithBackoffAndCachesFinalFailureLikeRust covers Rust
// #40447's bounded retry, one-second backoff, and cached exhaustion.
func TestShellSnapshotRetriesWithBackoffAndCachesFinalFailureLikeRust(t *testing.T) {
	previous := shellSnapshotPlatformSupported
	shellSnapshotPlatformSupported = true
	t.Cleanup(func() { shellSnapshotPlatformSupported = previous })

	now := time.Unix(100, 0)
	var calls atomic.Int64
	manager := newShellSnapshotManager()
	manager.now = func() time.Time { return now }
	manager.capture = snapshotRetryCapture(&calls, 2)
	t.Cleanup(manager.close)
	params := snapshotRetryParams(t, "retry-recovers")

	if _, err := manager.snapshotEntry(context.Background(), params, shell.ShellBash, params.Env, nil); err == nil {
		t.Fatal("first capture unexpectedly succeeded")
	}
	if _, err := manager.snapshotEntry(context.Background(), params, shell.ShellBash, params.Env, nil); err == nil {
		t.Fatal("failure during backoff unexpectedly retried")
	}
	if calls.Load() != 1 {
		t.Fatalf("captures during backoff = %d, want one", calls.Load())
	}
	now = now.Add(shellSnapshotRetryBackoff)
	if _, err := manager.snapshotEntry(context.Background(), params, shell.ShellBash, params.Env, nil); err == nil {
		t.Fatal("second attempt unexpectedly succeeded")
	}
	now = now.Add(shellSnapshotRetryBackoff)
	entry, err := manager.snapshotEntry(context.Background(), params, shell.ShellBash, params.Env, nil)
	if err != nil || entry == nil || entry.state == "" {
		t.Fatalf("third attempt = %#v, %v", entry, err)
	}
	if calls.Load() != 3 {
		t.Fatalf("captures = %d, want three", calls.Load())
	}
	if _, err := manager.snapshotEntry(context.Background(), params, shell.ShellBash, params.Env, nil); err != nil {
		t.Fatalf("cached success error = %v", err)
	}
	if calls.Load() != 3 {
		t.Fatalf("cached success recaptured: %d", calls.Load())
	}

	var exhaustedCalls atomic.Int64
	exhausted := newShellSnapshotManager()
	exhausted.now = func() time.Time { return now }
	exhausted.capture = snapshotRetryCapture(&exhaustedCalls, 10)
	t.Cleanup(exhausted.close)
	exhaustedParams := snapshotRetryParams(t, "retry-exhausted")
	for attempt := 0; attempt < maxShellSnapshotAttempts; attempt++ {
		if _, err := exhausted.snapshotEntry(context.Background(), exhaustedParams, shell.ShellBash, exhaustedParams.Env, nil); err == nil {
			t.Fatalf("attempt %d unexpectedly succeeded", attempt)
		}
		now = now.Add(shellSnapshotRetryBackoff)
	}
	if exhaustedCalls.Load() != maxShellSnapshotAttempts {
		t.Fatalf("exhausted captures = %d", exhaustedCalls.Load())
	}
	if _, err := exhausted.snapshotEntry(context.Background(), exhaustedParams, shell.ShellBash, exhaustedParams.Env, nil); err == nil {
		t.Fatal("exhausted failure was not cached")
	}
	if exhaustedCalls.Load() != maxShellSnapshotAttempts {
		t.Fatalf("exhausted failure recaptured: %d", exhaustedCalls.Load())
	}
}

// TestShellSnapshotConcurrentRequestsShareAttemptLikeRust covers single-flight
// capture sharing.
func TestShellSnapshotConcurrentRequestsShareAttemptLikeRust(t *testing.T) {
	previous := shellSnapshotPlatformSupported
	shellSnapshotPlatformSupported = true
	t.Cleanup(func() { shellSnapshotPlatformSupported = previous })

	var calls atomic.Int64
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	manager := newShellSnapshotManager()
	manager.capture = func(context.Context, []string, string, map[string]string) ([]byte, error) {
		calls.Add(1)
		entered <- struct{}{}
		<-release
		return []byte("# Snapshot file\nexport TOKEN=\"value\"\n\x00\x00\x00"), nil
	}
	t.Cleanup(manager.close)
	params := snapshotRetryParams(t, "single-flight")

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := manager.snapshotEntry(context.Background(), params, shell.ShellBash, params.Env, nil)
			errs <- err
		}()
	}
	<-entered
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent snapshot error = %v", err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("concurrent captures = %d, want one", calls.Load())
	}
}
