package execserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"codex_go/network"
	"codex_go/shell"
	"codex_go/utils"
)

func TestShellSnapshotRequestRoundTripAndCapability(t *testing.T) {
	params := ExecParams{
		ProcessID: "snapshot-process",
		Argv:      []string{"/bin/zsh", "-lc", "echo ok"},
		CWD:       "file:///repo",
		ShellSnapshot: &ShellSnapshotRequest{
			ScopeID: "attachment-1",
			Shell:   ShellInfo{Name: "zsh", Path: "/bin/zsh"},
		},
		Env: map[string]string{},
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"shellSnapshot":{"scopeId":"attachment-1"`) {
		t.Fatalf("shell snapshot wire form = %s", encoded)
	}
	var decoded ExecParams
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ShellSnapshot == nil || decoded.ShellSnapshot.ScopeID != "attachment-1" {
		t.Fatalf("decoded shell snapshot = %#v", decoded.ShellSnapshot)
	}
	if got, want := localEnvironmentInfo().Capabilities.ShellSnapshotV2, runtime.GOOS != "windows"; got != want {
		t.Fatalf("local shellSnapshotV2 capability = %v, want %v (Rust #40376)", got, want)
	}
}

func TestShellSnapshotManagerRewritesAndCachesLikeRust(t *testing.T) {
	previous := shellSnapshotPlatformSupported
	shellSnapshotPlatformSupported = true
	t.Cleanup(func() { shellSnapshotPlatformSupported = previous })
	var calls atomic.Int64
	manager := newShellSnapshotManager()
	manager.capture = func(context.Context, []string, string, map[string]string) ([]byte, error) {
		calls.Add(1)
		return []byte("# Snapshot file\nexport TOKEN=\"sk-real-secret\"\n\x00\x00\x00"), nil
	}
	cwd, err := utils.FromHostNativePath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	params := &ExecParams{
		ProcessID: "snapshot-process",
		Argv:      []string{"/bin/zsh", "-lc", "echo ok"},
		CWD:       cwd.String(),
		ShellSnapshot: &ShellSnapshotRequest{
			ScopeID: "attachment-1",
			Shell:   ShellInfo{Name: "zsh", Path: "/bin/zsh"},
		},
		Env: map[string]string{
			"PATH": "/bin", "HTTP_PROXY": "http://127.0.0.1:8080",
			network.CredentialBrokerActiveEnvKey: "1", "TOKEN": "sk-dummy-value",
			network.BrokeredCredentialsEnvKey: `[["TOKEN","sk-dummy-value"]]`,
		},
	}
	pairs := []network.CredentialMaskPair{{Real: "sk-real-secret", Dummy: "sk-dummy-value"}}
	rewritten, reader, err := manager.rewrite(context.Background(), params, params.Env, pairs, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	script := rewritten.Argv[2]
	if rewritten.Argv[1] != "-fc" {
		t.Fatalf("zsh snapshot launch flag = %q, want -fc", rewritten.Argv[1])
	}
	if !strings.HasPrefix(script, "setopt RCS\nif ! ") || !strings.HasSuffix(script, "echo ok") {
		t.Fatalf("rewritten script = %q", script)
	}
	if reader == nil && !shellSnapshotReplaysThroughState(script) {
		t.Fatalf("rewritten script = %q, want a snapshot replay", script)
	}
	state := shellSnapshotReplayState(t, rewritten, reader)
	if !strings.Contains(state, "sk-dummy-value") || strings.Contains(state, "sk-real-secret") {
		t.Fatalf("in-memory snapshot state = %q", state)
	}
	if !strings.Contains(script, "\\builtin export HTTP_PROXY='http://127.0.0.1:8080'") ||
		!strings.Contains(script, "\\builtin export "+network.CredentialBrokerActiveEnvKey+"='1'") ||
		!strings.Contains(script, "\\builtin export TOKEN='sk-dummy-value'") {
		t.Fatalf("launch environment was not restored after snapshot: %q", script)
	}
	again, againReader, err := manager.rewrite(context.Background(), params, params.Env, pairs, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// A protected capture belongs to one launch (Rust never caches it), so the
	// repeat capture yields the same replay text instead of a cache hit.
	if calls.Load() != 2 || again.Argv[2] != script {
		t.Fatalf("protected snapshot calls=%d script=%q", calls.Load(), again.Argv[2])
	}
	if againState := shellSnapshotReplayState(t, again, againReader); againState != state {
		t.Fatalf("cached snapshot state = %q, want %q", againState, state)
	}
	manager.close()
	manager.mu.Lock()
	cached := len(manager.cache)
	manager.mu.Unlock()
	if cached != 0 {
		t.Fatalf("snapshot cache survived close: %d entries", cached)
	}
}

func TestShellSnapshotCaptureFailureRestoresCredentialsLikeRust(t *testing.T) {
	previous := shellSnapshotPlatformSupported
	shellSnapshotPlatformSupported = true
	t.Cleanup(func() { shellSnapshotPlatformSupported = previous })
	manager := newShellSnapshotManager()
	manager.capture = func(context.Context, []string, string, map[string]string) ([]byte, error) {
		return nil, errors.New("capture failed")
	}
	params := &ExecParams{
		ProcessID: "fallback-process",
		Argv:      []string{"/bin/zsh", "-lc", "echo ok"},
		CWD:       "file:///repo",
		ShellSnapshot: &ShellSnapshotRequest{
			ScopeID: "attachment-1",
			Shell:   ShellInfo{Name: "zsh", Path: "/bin/zsh"},
		},
		Env: map[string]string{"TOKEN": "sk-dummy-value"},
	}
	pairs := []network.CredentialMaskPair{{Real: "sk-real-secret", Dummy: "sk-dummy-value"}}
	rewritten, _, err := manager.rewrite(context.Background(), params, params.Env, pairs, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rewritten.Env["TOKEN"] != "sk-real-secret" {
		t.Fatalf("fallback env = %#v", rewritten.Env)
	}
	if !strings.Contains(rewritten.Argv[2], "Credential masking is unavailable") || !strings.HasSuffix(rewritten.Argv[2], "echo ok") {
		t.Fatalf("fallback script = %q", rewritten.Argv[2])
	}
}

// TestShellSnapshotCacheIsBoundedLikeRust covers Rust's MAX_CACHED_SNAPSHOTS:
// the executor keeps at most 16 captures and removes the file a file-backed
// capture materialized when it evicts that entry (#39957).
func TestShellSnapshotCacheIsBoundedLikeRust(t *testing.T) {
	previous := shellSnapshotPlatformSupported
	shellSnapshotPlatformSupported = true
	t.Cleanup(func() { shellSnapshotPlatformSupported = previous })
	manager := newShellSnapshotManager()
	t.Cleanup(manager.close)
	// The state exceeds the 512 KiB environment replay budget, so every capture
	// is replayed through an unnamed file rather than the environment.
	output := []byte("# Snapshot file\n" + strings.Repeat("export BIG=\"value\"\n", 48*1024) + "\x00\x00\x00")
	manager.capture = func(context.Context, []string, string, map[string]string) ([]byte, error) {
		return output, nil
	}
	cwd, err := utils.FromHostNativePath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var oldestState string
	for index := 0; index < maxCachedShellSnapshots+1; index++ {
		params := &ExecParams{
			ProcessID: "bounded-snapshot",
			Argv:      []string{"/bin/bash", "-lc", "echo ok"},
			CWD:       cwd.String(),
			ShellSnapshot: &ShellSnapshotRequest{
				ScopeID: fmt.Sprintf("scope-%d", index),
				Shell:   ShellInfo{Name: "bash", Path: "/bin/bash"},
			},
			Env: map[string]string{"PATH": "/bin"},
		}
		entry, err := manager.snapshotEntry(context.Background(), params, shell.ShellBash, params.Env, nil, nil)
		if err != nil {
			t.Fatalf("capture %d: %v", index, err)
		}
		if entry == nil || len(entry.state) == 0 {
			t.Fatalf("capture %d did not keep the captured state: %#v", index, entry)
		}
		if index == 0 {
			oldestState = entry.state
		}
	}
	manager.mu.Lock()
	cached := len(manager.cache)
	manager.mu.Unlock()
	if cached != maxCachedShellSnapshots {
		t.Fatalf("cached snapshots = %d, want %d", cached, maxCachedShellSnapshots)
	}
	if oldestState == "" {
		t.Fatal("the oldest capture did not carry state")
	}
}

// TestShellSnapshotBashReplayUsesPrivilegedFlagLikeRust covers Rust's bash
// startup suppression: the restored snapshot launches with -pc and clears the
// privileged mode so automatic startup files do not re-run (#39957).
func TestShellSnapshotBashReplayUsesPrivilegedFlagLikeRust(t *testing.T) {
	previous := shellSnapshotPlatformSupported
	shellSnapshotPlatformSupported = true
	t.Cleanup(func() { shellSnapshotPlatformSupported = previous })
	manager := newShellSnapshotManager()
	manager.capture = func(context.Context, []string, string, map[string]string) ([]byte, error) {
		return []byte("# Snapshot file\nexport TOKEN=\"value\"\n\x00\x00\x00"), nil
	}
	cwd, err := utils.FromHostNativePath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	params := &ExecParams{
		ProcessID: "bash-snapshot",
		Argv:      []string{"/bin/bash", "-lc", "echo ok"},
		CWD:       cwd.String(),
		ShellSnapshot: &ShellSnapshotRequest{
			ScopeID: "bash-scope",
			Shell:   ShellInfo{Name: "bash", Path: "/bin/bash"},
		},
		Env: map[string]string{"PATH": "/bin"},
	}
	rewritten, _, err := manager.rewrite(context.Background(), params, params.Env, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rewritten.Argv[1] != "-pc" {
		t.Fatalf("bash snapshot launch flag = %q, want -pc", rewritten.Argv[1])
	}
	if !strings.HasPrefix(rewritten.Argv[2], "set +o privileged\nif ! ") {
		t.Fatalf("bash snapshot script = %q", rewritten.Argv[2])
	}
}

// TestValidateMaskedShellSnapshotLikeRust covers Rust #54432's credential
// re-check for executor-owned snapshots: masking must leave no real broker
// value in the replay script, even one an alias rebuilds from quoted
// fragments.
func TestValidateMaskedShellSnapshotLikeRust(t *testing.T) {
	pairs := []network.CredentialMaskPair{{Real: "sk-real-secret", Dummy: "sk-dummy-value"}}
	unsafe := func(text string) bool { return strings.Contains(text, "sk-real-secret") }
	if err := validateMaskedShellSnapshot("export TOKEN='sk-dummy-value'\n", shell.ShellBash, pairs, nil); err != nil {
		t.Fatalf("masked snapshot rejected: %v", err)
	}
	if err := validateMaskedShellSnapshot("export TOKEN='sk-real-secret'\n", shell.ShellBash, pairs, nil); err == nil {
		t.Fatal("verbatim credential accepted")
	}
	if err := validateMaskedShellSnapshot("export TOKEN='sk-re''al-secret'\n", shell.ShellBash, pairs, nil); err == nil {
		t.Fatal("reassembled credential accepted")
	}
	if err := validateMaskedShellSnapshot("export TOKEN='sk-re''al-secret'\n", shell.ShellBash, nil, nil); err != nil {
		t.Fatalf("unprotected snapshot checked for credentials: %v", err)
	}
	if err := validateMaskedShellSnapshot("export DROP='sk-real-secret'\n", shell.ShellBash, nil, unsafe); err == nil {
		t.Fatal("broker text check was not consulted")
	}
	if err := validateMaskedShellSnapshot("export DROP='sk-re''al-secret'\n", shell.ShellBash, nil, unsafe); err == nil {
		t.Fatal("broker text check missed a reassembled credential")
	}
	if err := validateMaskedShellSnapshot("export DROP='sk-dummy-value'\n", shell.ShellBash, nil, unsafe); err != nil {
		t.Fatalf("clean snapshot rejected by the broker check: %v", err)
	}
}

// TestShellSnapshotReassembledCredentialFallsBackLikeRust pulls the check
// through the manager: a capture whose replay text still reconstructs the real
// credential is discarded, and the launch keeps the unmasked environment with
// the explicit warning instead of replaying a leaky snapshot.
func TestShellSnapshotReassembledCredentialFallsBackLikeRust(t *testing.T) {
	previous := shellSnapshotPlatformSupported
	shellSnapshotPlatformSupported = true
	t.Cleanup(func() { shellSnapshotPlatformSupported = previous })
	manager := newShellSnapshotManager()
	t.Cleanup(manager.close)
	manager.capture = func(context.Context, []string, string, map[string]string) ([]byte, error) {
		return []byte("# Snapshot file\nexport TOKEN=\"sk-re\"\"al-secret\"\n\x00\x00\x00"), nil
	}
	params := &ExecParams{
		ProcessID: "reassembled-process",
		Argv:      []string{"/bin/bash", "-lc", "echo ok"},
		CWD:       "file:///repo",
		ShellSnapshot: &ShellSnapshotRequest{
			ScopeID: "attachment-1",
			Shell:   ShellInfo{Name: "bash", Path: "/bin/bash"},
		},
		Env: map[string]string{"TOKEN": "sk-dummy-value"},
	}
	pairs := []network.CredentialMaskPair{{Real: "sk-real-secret", Dummy: "sk-dummy-value"}}
	rewritten, _, err := manager.rewrite(context.Background(), params, params.Env, pairs, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rewritten.Env["TOKEN"] != "sk-real-secret" {
		t.Fatalf("fallback env = %#v", rewritten.Env)
	}
	if !strings.Contains(rewritten.Argv[2], "Credential masking is unavailable") {
		t.Fatalf("fallback script = %q", rewritten.Argv[2])
	}
	manager.mu.Lock()
	cached := len(manager.cache)
	manager.mu.Unlock()
	if cached != 0 {
		t.Fatalf("rejected capture was cached: %d entries", cached)
	}
}

// TestShellSnapshotBrokerTextCheckFallsBackLikeRust covers the executor's use
// of the broker's own text check (Rust's proxy.virtualize_brokered_text): a
// credential the broker recognizes by pattern, but which is not in the mask
// pairs, still disqualifies the capture.
func TestShellSnapshotBrokerTextCheckFallsBackLikeRust(t *testing.T) {
	previous := shellSnapshotPlatformSupported
	shellSnapshotPlatformSupported = true
	t.Cleanup(func() { shellSnapshotPlatformSupported = previous })
	manager := newShellSnapshotManager()
	t.Cleanup(manager.close)
	manager.capture = func(context.Context, []string, string, map[string]string) ([]byte, error) {
		return []byte("# Snapshot file\nexport DROP=\"ghp_patterncredential\"\n\x00\x00\x00"), nil
	}
	params := &ExecParams{
		ProcessID: "pattern-process",
		Argv:      []string{"/bin/bash", "-lc", "echo ok"},
		CWD:       "file:///repo",
		ShellSnapshot: &ShellSnapshotRequest{
			ScopeID: "attachment-1",
			Shell:   ShellInfo{Name: "bash", Path: "/bin/bash"},
		},
		Env: map[string]string{"TOKEN": "sk-dummy-value"},
	}
	unsafe := func(text string) bool { return strings.Contains(text, "ghp_patterncredential") }
	rewritten, _, err := manager.rewrite(context.Background(), params, params.Env, nil, unsafe, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rewritten.Argv[2], "Credential masking is unavailable") {
		t.Fatalf("fallback script = %q", rewritten.Argv[2])
	}
}
