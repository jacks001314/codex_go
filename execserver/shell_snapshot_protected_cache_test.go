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

	first, err := manager.rewrite(context.Background(), params, params.Env, []network.CredentialMaskPair{{Real: "sk-real-secret", Dummy: "dummy-one"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	firstState := first.Env["__CODEX_SHELL_SNAPSHOT_STATE_0"]
	if !strings.Contains(firstState, "dummy-one") || strings.Contains(firstState, "sk-real-secret") {
		t.Fatalf("first replay state = %q", firstState)
	}
	second, err := manager.rewrite(context.Background(), params, params.Env, []network.CredentialMaskPair{{Real: "sk-real-secret", Dummy: "dummy-two"}}, nil, nil)
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
	if _, err := manager.rewrite(context.Background(), params, params.Env, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.rewrite(context.Background(), params, params.Env, nil, nil, nil); err != nil {
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
	fallback, err := manager.rewrite(context.Background(), params, params.Env, []network.CredentialMaskPair{{Real: "sk-real-secret", Dummy: "dummy-one"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fallback.Argv[2], "Credential masking is unavailable") {
		t.Fatalf("fallback script = %q", fallback.Argv[2])
	}
	rewritten, err := manager.rewrite(context.Background(), params, params.Env, nil, nil, nil)
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

// Rust restores credentials through credentials::restore_fallback, which
// restores every brokered placeholder the proxy knows about and disables
// brokering for that child only. A child whose protected capture fails must not
// keep the dummies minted for an earlier launch (#54432).
func TestShellSnapshotFallbackRestoresAllBrokeredCredentialsLikeRust(t *testing.T) {
	previous := shellSnapshotPlatformSupported
	shellSnapshotPlatformSupported = true
	t.Cleanup(func() { shellSnapshotPlatformSupported = previous })
	manager := newShellSnapshotManager()
	defer manager.close()
	manager.capture = func(context.Context, []string, string, map[string]string) ([]byte, error) {
		return nil, errors.New("capture failed")
	}
	params := protectedSnapshotParams(t)
	params.Env = map[string]string{
		"PATH":                               "/bin",
		"TOKEN_A":                            "dummy-alpha",
		"TOKEN_B":                            "prefix-dummy-beta-suffix",
		network.CredentialBrokerActiveEnvKey: "1",
		network.BrokeredCredentialsEnvKey:    "dummy-alpha,dummy-beta",
	}
	maskedPairs := []network.CredentialMaskPair{{Real: "stale-alpha", Dummy: "dummy-alpha"}}

	var restoreCalls atomic.Int64
	restoreFallback := func(env map[string]string) {
		restoreCalls.Add(1)
		network.RestoreSnapshotCredentials(env, []network.CredentialMaskPair{
			{Real: "real-alpha", Dummy: "dummy-alpha"},
			{Real: "real-beta", Dummy: "dummy-beta"},
		})
	}

	rewritten, err := manager.rewrite(context.Background(), params, params.Env, maskedPairs, nil, restoreFallback)
	if err != nil {
		t.Fatal(err)
	}
	if restoreCalls.Load() != 1 {
		t.Fatalf("restore fallback calls = %d, want 1", restoreCalls.Load())
	}
	if !strings.Contains(rewritten.Argv[2], "Credential masking is unavailable") {
		t.Fatalf("fallback script = %q", rewritten.Argv[2])
	}
	if got := rewritten.Env["TOKEN_A"]; got != "real-alpha" {
		t.Fatalf("TOKEN_A = %q, want the proxy fallback to win over the masked pairs", got)
	}
	if got := rewritten.Env["TOKEN_B"]; got != "prefix-real-beta-suffix" {
		t.Fatalf("TOKEN_B = %q, want every brokered placeholder restored, not only masked pairs", got)
	}
	for _, key := range []string{network.CredentialBrokerActiveEnvKey, network.BrokeredCredentialsEnvKey} {
		if _, ok := rewritten.Env[key]; ok {
			t.Fatalf("%s survived the fallback: %#v", key, rewritten.Env)
		}
	}
	if params.Env["TOKEN_B"] != "prefix-dummy-beta-suffix" || params.Env[network.BrokeredCredentialsEnvKey] == "" {
		t.Fatalf("the fallback mutated the caller's environment: %#v", params.Env)
	}
}
