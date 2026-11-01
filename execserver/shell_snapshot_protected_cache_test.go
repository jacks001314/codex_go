package execserver

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"codex_go/network"
	"codex_go/utils"
)

// Mirrors Rust ShellSnapshotCache::prepare: a protected capture is prepared for
// one launch and never enters the cache, because its replay carries that
// launch's broker dummies. Reusing it under another broker state would restore
// stale placeholders instead of the current ones.
func TestShellSnapshotProtectedCaptureIsNotReusedLikeRust(t *testing.T) {
	previous := shellSnapshotPlatformSupported
	shellSnapshotPlatformSupported = true
	t.Cleanup(func() { shellSnapshotPlatformSupported = previous })
	var calls atomic.Int64
	manager := newShellSnapshotManager()
	manager.capture = func(context.Context, []string, string, map[string]string) ([]byte, error) {
		calls.Add(1)
		return []byte("# Snapshot file\nexport TOKEN='sk-real-secret'\n\x00\x00\x00"), nil
	}
	params := protectedSnapshotParams(t)

	first, err := manager.rewrite(context.Background(), params, params.Env, []network.CredentialMaskPair{{Real: "sk-real-secret", Dummy: "dummy-one"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	firstState := first.Env["__CODEX_SHELL_SNAPSHOT_STATE_0"]
	if !strings.Contains(firstState, "dummy-one") || strings.Contains(firstState, "sk-real-secret") {
		t.Fatalf("first replay state = %q", firstState)
	}
	second, err := manager.rewrite(context.Background(), params, params.Env, []network.CredentialMaskPair{{Real: "sk-real-secret", Dummy: "dummy-two"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	secondState := second.Env["__CODEX_SHELL_SNAPSHOT_STATE_0"]
	if !strings.Contains(secondState, "dummy-two") || strings.Contains(secondState, "dummy-one") {
		t.Fatalf("second replay state reused the earlier broker dummy: %q", secondState)
	}
	if calls.Load() != 2 {
		t.Fatalf("protected captures = %d, want one capture per launch", calls.Load())
	}

	// Unprotected captures keep the shared cache.
	if _, err := manager.rewrite(context.Background(), params, params.Env, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.rewrite(context.Background(), params, params.Env, nil, nil); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatalf("captures after two unprotected launches = %d, want the first capture reused", calls.Load())
	}
	manager.close()
}

// A protected failure must not poison the shared cache either: Rust never
// stores it, so the next unprotected launch captures immediately instead of
// inheriting the retry backoff.
func TestShellSnapshotProtectedFailureDoesNotPoisonCacheLikeRust(t *testing.T) {
	previous := shellSnapshotPlatformSupported
	shellSnapshotPlatformSupported = true
	t.Cleanup(func() { shellSnapshotPlatformSupported = previous })
	var calls atomic.Int64
	manager := newShellSnapshotManager()
	manager.capture = func(context.Context, []string, string, map[string]string) ([]byte, error) {
		if calls.Add(1) == 1 {
			return nil, errors.New("capture failed")
		}
		return []byte("# Snapshot file\nexport TOKEN='sk-real-secret'\n\x00\x00\x00"), nil
	}
	params := protectedSnapshotParams(t)
	fallback, err := manager.rewrite(context.Background(), params, params.Env, []network.CredentialMaskPair{{Real: "sk-real-secret", Dummy: "dummy-one"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fallback.Argv[2], "Credential masking is unavailable") {
		t.Fatalf("fallback script = %q", fallback.Argv[2])
	}
	rewritten, err := manager.rewrite(context.Background(), params, params.Env, nil, nil)
	if err != nil {
		t.Fatalf("the unprotected launch inherited a cached protected failure: %v", err)
	}
	if _, ok := rewritten.Env["__CODEX_SHELL_SNAPSHOT_STATE_0"]; !ok {
		t.Fatalf("unprotected launch did not capture a snapshot: %#v", rewritten.Env)
	}
	if calls.Load() != 2 {
		t.Fatalf("captures = %d, want the failure retried without backoff", calls.Load())
	}
	manager.close()
}

func protectedSnapshotParams(t *testing.T) *ExecParams {
	t.Helper()
	cwd, err := utils.FromHostNativePath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &ExecParams{
		ProcessID: "snapshot-protected-process",
		Argv:      []string{"/bin/bash", "-lc", "echo ok"},
		CWD:       cwd.String(),
		ShellSnapshot: &ShellSnapshotRequest{
			ScopeID: "attachment-protected",
			Shell:   ShellInfo{Name: "bash", Path: "/bin/bash"},
		},
		Env: map[string]string{"PATH": "/bin"},
	}
}
