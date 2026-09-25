package tool

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/envutil"
	"codex_go/sandbox"
)

// errSnapshotTestFailure stands in for a capture or validation failure.
var errSnapshotTestFailure = errors.New("snapshot command failed")

// snapshotBashCaptureStream is a capture stream in the shape the bash capture
// script emits: state from the banner on, aliases, one export declaration, then
// the environment records.
func snapshotBashCaptureStream() []byte {
	return []byte(strings.Join([]string{
		"# Snapshot file\n# Functions\nprobe() {\n  echo probe\n}\n\nalias probe_alias='echo alias'\n",
		"export FOO='bar'\n",
		"FOO", "declare -x FOO=\"bar\"",
		"",
		"FOO=bar",
	}, "\x00"))
}

// snapshotBuilderRunner records every capture run and answers with a canned
// stream, so the builder's cache, file lifecycle and failure handling can be
// tested without a shell.
type snapshotBuilderRunner struct {
	calls        [][]string
	environments []map[string]string
	capture      []byte
	captureErr   error
	validateErr  error
}

func (r *snapshotBuilderRunner) run(
	_ context.Context,
	command []string,
	cwd string,
	env map[string]string,
	_ *sandbox.PermissionProfile,
	_ string,
) ([]byte, error) {
	r.calls = append(r.calls, append([]string(nil), command...))
	r.environments = append(r.environments, env)
	if len(command) >= 3 && strings.HasPrefix(command[2], "set -e; . ") {
		return nil, r.validateErr
	}
	return r.capture, r.captureErr
}

func (r *snapshotBuilderRunner) captureCalls() int {
	count := 0
	for _, call := range r.calls {
		if len(call) >= 3 && !strings.HasPrefix(call[2], "set -e; . ") {
			count++
		}
	}
	return count
}

func TestSnapshotBuilderCapturesOnceAndReusesLikeRust(t *testing.T) {
	codexHome := t.TempDir()
	runner := &snapshotBuilderRunner{capture: snapshotBashCaptureStream()}
	builder := NewSnapshotBuilder(SnapshotBuilderOptions{
		CodexHome: codexHome,
		SessionID: "session-1",
		Runner:    runner.run,
	})
	if builder == nil {
		t.Fatal("NewSnapshotBuilder() = nil")
	}
	t.Cleanup(builder.Close)
	request := SnapshotCaptureRequest{
		ShellType:       ShellBash,
		ShellPath:       "/bin/bash",
		CWD:             "/repo",
		AllowLoginShell: true,
	}
	first := builder.Snapshot(context.Background(), request)
	if first == nil {
		t.Fatal("Snapshot() = nil")
	}
	if filepath.Dir(first.Path()) != filepath.Join(codexHome, "shell_snapshots") {
		t.Fatalf("snapshot path = %q", first.Path())
	}
	content, err := os.ReadFile(first.Path())
	if err != nil {
		t.Fatalf("read snapshot error = %v", err)
	}
	const wantContent = "# Snapshot file\n# Functions\nprobe() {\n  echo probe\n}\n\nalias probe_alias='echo alias'\n" +
		"export FOO='bar'\n" +
		"# exports (native declarations)\n" +
		"declare -x FOO=\"bar\""
	if string(content) != wantContent {
		t.Fatalf("snapshot content = %q, want %q", content, wantContent)
	}
	// The capture ran as a login shell and was validated by sourcing.
	if len(runner.calls) != 2 || runner.calls[0][1] != "-lc" {
		t.Fatalf("capture calls = %#v", runner.calls)
	}
	if !strings.Contains(runner.calls[0][2], "# Snapshot file") {
		t.Fatalf("capture script = %q", runner.calls[0][2])
	}
	if runner.calls[1][1] != "-lc" || !strings.HasPrefix(runner.calls[1][2], "set -e; . ") {
		t.Fatalf("validation call = %#v", runner.calls[1])
	}
	// No temporary file is left behind once the snapshot is in place.
	entries, err := os.ReadDir(filepath.Join(codexHome, "shell_snapshots"))
	if err != nil {
		t.Fatalf("read snapshot dir error = %v", err)
	}
	if len(entries) != 1 || strings.Contains(entries[0].Name(), ".tmp-") {
		t.Fatalf("snapshot dir = %#v", entries)
	}

	// The same launch reuses the snapshot; another directory captures its own.
	second := builder.Snapshot(context.Background(), request)
	if second == nil || second.Path() != first.Path() || runner.captureCalls() != 1 {
		t.Fatalf("second Snapshot() = %v after %d captures", second, runner.captureCalls())
	}
	other := request
	other.CWD = "/elsewhere"
	if builder.Snapshot(context.Background(), other) == nil || runner.captureCalls() != 2 {
		t.Fatalf("captures = %d, want one per directory", runner.captureCalls())
	}

	builder.Close()
	for _, path := range []string{first.Path()} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("close left %s behind (err = %v)", path, err)
		}
	}
}

func TestSnapshotBuilderFailsOpenLikeRust(t *testing.T) {
	snapshotDirEntries := func(t *testing.T, codexHome string) []string {
		t.Helper()
		entries, err := os.ReadDir(filepath.Join(codexHome, "shell_snapshots"))
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			t.Fatalf("read snapshot dir error = %v", err)
		}
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		return names
	}
	request := SnapshotCaptureRequest{ShellType: ShellBash, ShellPath: "/bin/bash", CWD: "/repo", AllowLoginShell: true}
	cases := []struct {
		name   string
		runner *snapshotBuilderRunner
	}{
		{"capture failed", &snapshotBuilderRunner{captureErr: errSnapshotTestFailure}},
		{"invalid capture", &snapshotBuilderRunner{capture: []byte("no banner\x00aliases")}},
		{"validation failed", &snapshotBuilderRunner{capture: snapshotBashCaptureStream(), validateErr: errSnapshotTestFailure}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			codexHome := t.TempDir()
			builder := NewSnapshotBuilder(SnapshotBuilderOptions{
				CodexHome: codexHome,
				SessionID: "session-1",
				Runner:    testCase.runner.run,
			})
			t.Cleanup(builder.Close)
			if snapshot := builder.Snapshot(context.Background(), request); snapshot != nil {
				t.Fatalf("Snapshot() = %v, want no snapshot", snapshot.Path())
			}
			if left := snapshotDirEntries(t, codexHome); len(left) != 0 {
				t.Fatalf("failed capture left %#v behind", left)
			}
		})
	}
}

// TestSnapshotBuilderCaptureShapeLikeRust pins what the capture runs with: the
// login flag from the launch, the capture script, and an environment without the
// launch-context variables Rust scrubs.
func TestSnapshotBuilderCaptureShapeLikeRust(t *testing.T) {
	t.Setenv(envutil.CodexExecServerNoiseAuthTokenEnvVar, "noise-token")
	for _, allowLogin := range []bool{true, false} {
		runner := &snapshotBuilderRunner{capture: snapshotBashCaptureStream()}
		builder := NewSnapshotBuilder(SnapshotBuilderOptions{
			CodexHome: t.TempDir(),
			SessionID: "session-1",
			Runner:    runner.run,
		})
		t.Cleanup(builder.Close)
		if builder.Snapshot(context.Background(), SnapshotCaptureRequest{
			ShellType:       ShellBash,
			ShellPath:       "/bin/bash",
			CWD:             "/repo",
			AllowLoginShell: allowLogin,
		}) == nil {
			t.Fatal("Snapshot() = nil")
		}
		wantFlag := "-c"
		if allowLogin {
			wantFlag = "-lc"
		}
		if runner.calls[0][0] != "/bin/bash" || runner.calls[0][1] != wantFlag {
			t.Fatalf("capture argv = %#v, want flag %s", runner.calls[0], wantFlag)
		}
		if script := runner.calls[0][2]; !strings.Contains(script, "# Snapshot file") ||
			!strings.Contains(script, "declare -xp") {
			t.Fatalf("capture script = %q", script)
		}
		if allowLogin && !strings.Contains(runner.calls[0][2], "$HOME/.bashrc") {
			t.Fatalf("login capture does not seed the user's startup:\n%s", runner.calls[0][2])
		}
		if env := runner.environments[0]; env[envutil.CodexExecServerNoiseAuthTokenEnvVar] != "" {
			t.Fatalf("capture environment leaked a launch-context variable: %#v", env)
		}
	}
	if snapshotBuilder := NewSnapshotBuilder(SnapshotBuilderOptions{SessionID: "session-1"}); snapshotBuilder != nil {
		t.Fatal("a session without a codex home must not build snapshots")
	}
	if snapshotBuilder := NewSnapshotBuilder(SnapshotBuilderOptions{CodexHome: t.TempDir()}); snapshotBuilder != nil {
		t.Fatal("a session without an id must not build snapshots")
	}
}

// TestSnapshotBuilderCapturesWithTheHostShellLikeRust runs the default capture
// runner against a real shell under the launch's permission profile, so the
// sandboxed capture path is exercised end to end.
func TestSnapshotBuilderCapturesWithTheHostShellLikeRust(t *testing.T) {
	bash := hostBashPath()
	if bash == "" {
		t.Skip("no bash on this host")
	}
	t.Setenv("SNAPSHOT_PROBE_MARKER", "probe-value")
	codexHome := t.TempDir()
	profile := sandbox.WorkspaceWritePermissionProfile()
	builder := NewSnapshotBuilder(SnapshotBuilderOptions{CodexHome: codexHome, SessionID: "session-1"})
	if builder == nil {
		t.Fatal("NewSnapshotBuilder() = nil")
	}
	t.Cleanup(builder.Close)
	cwd := t.TempDir()
	snapshot := builder.Snapshot(context.Background(), SnapshotCaptureRequest{
		ShellType:           ShellBash,
		ShellPath:           bash,
		CWD:                 cwd,
		AllowLoginShell:     false,
		PermissionProfile:   &profile,
		PermissionProfileID: "resolved",
	})
	if snapshot == nil {
		t.Fatal("the host shell produced no snapshot")
	}
	content, err := os.ReadFile(snapshot.Path())
	if err != nil {
		t.Fatalf("read snapshot error = %v", err)
	}
	if !strings.Contains(string(content), "# Snapshot file") {
		t.Fatalf("snapshot = %q", content)
	}
	// The captured declarations come from the real shell, including the
	// environment this test set.
	if !strings.Contains(string(content), `declare -x SNAPSHOT_PROBE_MARKER="probe-value"`) {
		t.Fatalf("snapshot misses the captured export:\n%s", content)
	}
}

// hostBashPath finds a bash binary for the capture round trips.
func hostBashPath() string {
	if path, err := exec.LookPath("bash"); err == nil {
		return path
	}
	for _, candidate := range []string{
		`C:\Program Files\Git\bin\bash.exe`,
		`C:\Program Files\Git\usr\bin\bash.exe`,
		"/bin/bash",
		"/usr/bin/bash",
	} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}
