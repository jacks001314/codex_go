package execserver

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
)

// Rust #39957/#42137: the local executor keeps captured state in memory keyed by
// the scope request, cwd, environment policy and sandbox, so a prewarm serves a
// later launch even though that launch carries its own tool-call metadata.
func TestLocalShellSnapshotExecutorPrewarmsAndPreparesLikeRust(t *testing.T) {
	previous := shellSnapshotPlatformSupported
	shellSnapshotPlatformSupported = true
	t.Cleanup(func() { shellSnapshotPlatformSupported = previous })

	executor := NewLocalShellSnapshotExecutor()
	var calls atomic.Int64
	executor.snapshots.capture = func(context.Context, []string, string, map[string]string) ([]byte, error) {
		calls.Add(1)
		return []byte("# Snapshot file\nexport FOO=bar\n\x00\x00\x00"), nil
	}
	snapshot := &ShellSnapshotRequest{ScopeID: "thread-1:local", Shell: ShellInfo{Name: "bash", Path: "/bin/bash"}}
	launch := func(callID string) *ExecParams {
		return &ExecParams{
			ProcessID:     "process-1",
			Argv:          []string{"/bin/bash", "-lc", "echo hi"},
			CWD:           "file:///repo",
			ShellSnapshot: snapshot,
			Env:           map[string]string{"CODEX_TOOL_CALL_ID": callID, "PATH": "/bin"},
		}
	}

	// Rust prewarms with the login shell's empty script; the cache lookup must not
	// depend on the argv, only on the request/cwd/policy/sandbox identity.
	warmup := launch("warmup")
	warmup.Argv = []string{"/bin/bash", "-lc", ""}
	if err := executor.Prewarm(context.Background(), warmup, map[string]string{"PATH": "/bin"}); err != nil {
		t.Fatalf("Prewarm() error = %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("captures after prewarm = %d, want 1", calls.Load())
	}

	rewritten, err := executor.Prepare(context.Background(), launch("call-1"), map[string]string{"PATH": "/bin"})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("captures after launch = %d, want the prewarmed capture", calls.Load())
	}
	if len(rewritten.Argv) != 3 || rewritten.Argv[1] != "-pc" {
		t.Fatalf("rewritten argv = %#v, want a bash -pc launch", rewritten.Argv)
	}
	if !strings.Contains(rewritten.Argv[2], "set +o privileged") || !strings.Contains(rewritten.Argv[2], "echo hi") {
		t.Fatalf("rewritten script = %q", rewritten.Argv[2])
	}
	if rewritten.Env["__CODEX_SHELL_SNAPSHOT_STATE_0"] == "" {
		t.Fatalf("rewritten env = %#v, want an in-memory replay chunk", rewritten.Env)
	}
	if rewritten.Env["CODEX_TOOL_CALL_ID"] != "call-1" {
		t.Fatalf("rewritten env lost the launch metadata: %#v", rewritten.Env)
	}

	// The sandbox is part of the cache identity; a different sandbox captures.
	sandboxed := launch("call-2")
	sandboxed.Sandbox = json.RawMessage(`{"cwd":"file:///repo"}`)
	if _, err := executor.Prepare(context.Background(), sandboxed, map[string]string{"PATH": "/bin"}); err != nil {
		t.Fatalf("Prepare() with sandbox error = %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("captures after a different sandbox = %d, want 2", calls.Load())
	}

	invalid := launch("call-3")
	invalid.ShellSnapshot = &ShellSnapshotRequest{ScopeID: "", Shell: ShellInfo{Name: "bash", Path: "/bin/bash"}}
	if err := executor.Prewarm(context.Background(), invalid, nil); err == nil {
		t.Fatal("Prewarm() accepted an empty scope")
	}
	invalid = launch("call-4")
	invalid.Argv = []string{"/bin/bash", "-lc"}
	if err := executor.Prewarm(context.Background(), invalid, nil); err == nil {
		t.Fatal("Prewarm() accepted a launch without a command script")
	}

	executor.Close()
	if _, err := executor.Prepare(context.Background(), launch("call-5"), nil); err == nil {
		t.Fatal("Prepare() on a closed executor succeeded")
	}
}
