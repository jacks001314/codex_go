package tool

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"

	"codex_go/execserver"
)

func localSnapshotEchoCommand(text string) []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/c", "echo", text}
	}
	return []string{"/bin/sh", "-c", "echo " + text}
}

// Rust #39957: a local unified-exec launch asks the in-process executor for its
// scope's shell snapshot and runs the rewritten argv, while the recorded command
// stays the model's original one (the rewrite is executor-side, as it is for a
// remote executor).
func TestUnifiedExecLocalLaunchUsesExecutorShellSnapshotLikeRust(t *testing.T) {
	previous := prepareLocalShellSnapshot
	t.Cleanup(func() { prepareLocalShellSnapshot = previous })

	snapshot := &execserver.ShellSnapshotRequest{ScopeID: "thread-1:local", Shell: execserver.ShellInfo{Name: "bash", Path: "/bin/bash"}}
	original := localSnapshotEchoCommand("original")
	var received *execserver.ExecParams
	prepareLocalShellSnapshot = func(_ context.Context, params *execserver.ExecParams, captureEnv map[string]string) (*execserver.ExecParams, error) {
		received = params
		rewritten := *params
		rewritten.Argv = localSnapshotEchoCommand("snapshot-warmed")
		env := map[string]string{}
		for key, value := range params.Env {
			env[key] = value
		}
		env["CODEX_SHELL_SNAPSHOT_MARKER"] = "1"
		rewritten.Env = env
		return &rewritten, nil
	}

	manager := NewUnifiedExecManager()
	cwd := t.TempDir()
	result, err := manager.Exec(context.Background(), &ShellRequest{
		Command:            original,
		CWD:                cwd,
		LocalShellSnapshot: snapshot,
		YieldTimeMS:        10_000,
	}, "call-1")
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if received == nil {
		t.Fatal("the local executor never saw the launch")
	}
	if received.ShellSnapshot != snapshot {
		t.Fatalf("snapshot request = %#v", received.ShellSnapshot)
	}
	if strings.Join(received.Argv, "\x00") != strings.Join(original, "\x00") {
		t.Fatalf("executor argv = %#v, want the model's command", received.Argv)
	}
	if received.CWD != cwd {
		t.Fatalf("executor cwd = %q, want %q", received.CWD, cwd)
	}
	if !strings.Contains(result.Stdout, "snapshot-warmed") {
		t.Fatalf("stdout = %q, want the rewritten launch output", result.Stdout)
	}
}

// A rewrite the executor rejects fails the launch, mirroring the exec server
// returning the process/start error to the caller.
func TestUnifiedExecLocalLaunchFailsWhenSnapshotPreparationFails(t *testing.T) {
	previous := prepareLocalShellSnapshot
	t.Cleanup(func() { prepareLocalShellSnapshot = previous })

	boom := errors.New("snapshot prepare failed")
	prepareLocalShellSnapshot = func(context.Context, *execserver.ExecParams, map[string]string) (*execserver.ExecParams, error) {
		return nil, boom
	}
	manager := NewUnifiedExecManager()
	_, err := manager.Exec(context.Background(), &ShellRequest{
		Command:            localSnapshotEchoCommand("original"),
		CWD:                t.TempDir(),
		LocalShellSnapshot: &execserver.ShellSnapshotRequest{ScopeID: "thread-1:local"},
		YieldTimeMS:        10_000,
	}, "call-2")
	if !errors.Is(err, boom) {
		t.Fatalf("Exec() error = %v, want %v", err, boom)
	}
}

// Rust #42137: the turn prewarm captures through the same launch shape a later
// command uses — the shell's login invocation with an empty script, the
// environment's directory, and the thread metadata.
func TestPrewarmLocalShellSnapshotUsesLoginInvocationLikeRust(t *testing.T) {
	previous := prewarmLocalShellSnapshot
	var got *execserver.ExecParams
	prewarmLocalShellSnapshot = func(_ context.Context, params *execserver.ExecParams, captureEnv map[string]string) error {
		got = params
		return nil
	}
	t.Cleanup(func() { prewarmLocalShellSnapshot = previous })

	err := PrewarmLocalShellSnapshot(context.Background(), &ShellRequest{
		CWD:      "/repo",
		ThreadID: "thread-1",
		Shell: ShellInvocation{
			Shell:         &Shell{Type: ShellBash, Path: "/bin/bash"},
			UseLoginShell: true,
		},
		LocalShellSnapshot: &execserver.ShellSnapshotRequest{
			ScopeID: "thread-1:local",
			Shell:   execserver.ShellInfo{Name: "bash", Path: "/bin/bash"},
		},
	})
	if err != nil {
		t.Fatalf("PrewarmLocalShellSnapshot() error = %v", err)
	}
	if got == nil {
		t.Fatal("the executor never received the prewarm")
	}
	if strings.Join(got.Argv, "\x00") != strings.Join([]string{"/bin/bash", "-lc", ""}, "\x00") {
		t.Fatalf("prewarm argv = %#v, want the login invocation with an empty script", got.Argv)
	}
	if got.CWD != "/repo" {
		t.Fatalf("prewarm cwd = %q", got.CWD)
	}
	if got.Env["CODEX_THREAD_ID"] != "thread-1" {
		t.Fatalf("prewarm env = %#v, want the thread metadata", got.Env)
	}
	if !strings.HasPrefix(got.ProcessID, "shell-snapshot-warmup-") {
		t.Fatalf("prewarm process id = %q", got.ProcessID)
	}
	if got.ShellSnapshot == nil || got.ShellSnapshot.ScopeID != "thread-1:local" {
		t.Fatalf("prewarm snapshot = %#v", got.ShellSnapshot)
	}
}
