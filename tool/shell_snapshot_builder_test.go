package tool

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	first, reason := builder.Snapshot(context.Background(), request)
	if reason != "" {
		t.Fatalf("Snapshot() reason = %q", reason)
	}
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
	second, _ := builder.Snapshot(context.Background(), request)
	if second == nil || second.Path() != first.Path() || runner.captureCalls() != 1 {
		t.Fatalf("second Snapshot() = %v after %d captures", second, runner.captureCalls())
	}
	other := request
	other.CWD = "/elsewhere"
	if created, _ := builder.Snapshot(context.Background(), other); created == nil || runner.captureCalls() != 2 {
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
		name       string
		runner     *snapshotBuilderRunner
		wantReason SnapshotFailureReason
	}{
		{"capture failed", &snapshotBuilderRunner{captureErr: errSnapshotTestFailure}, SnapshotReasonWriteFailed},
		{"invalid capture", &snapshotBuilderRunner{capture: []byte("no banner\x00aliases")}, SnapshotReasonWriteFailed},
		{"validation failed", &snapshotBuilderRunner{capture: snapshotBashCaptureStream(), validateErr: errSnapshotTestFailure}, SnapshotReasonValidationFailed},
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
			snapshot, reason := builder.Snapshot(context.Background(), request)
			if snapshot != nil {
				t.Fatalf("Snapshot() = %v, want no snapshot", snapshot.Path())
			}
			if reason != testCase.wantReason {
				t.Fatalf("Snapshot() reason = %q, want %q", reason, testCase.wantReason)
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
		if created, _ := builder.Snapshot(context.Background(), SnapshotCaptureRequest{
			ShellType:       ShellBash,
			ShellPath:       "/bin/bash",
			CWD:             "/repo",
			AllowLoginShell: allowLogin,
		}); created == nil {
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
	snapshot, reason := builder.Snapshot(context.Background(), SnapshotCaptureRequest{
		ShellType:           ShellBash,
		ShellPath:           bash,
		CWD:                 cwd,
		AllowLoginShell:     false,
		PermissionProfile:   &profile,
		PermissionProfileID: "resolved",
	})
	if reason != "" {
		t.Fatalf("Snapshot() reason = %q", reason)
	}
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

// snapshotCaptureStreamWith builds a bash capture stream from (key, declaration)
// pairs.
func snapshotCaptureStreamWith(exports ...[2]string) []byte {
	records := []string{"# Snapshot file\nalias probe='echo probe'\n", ""}
	for _, export := range exports {
		records = append(records, export[0], export[1])
	}
	// The empty key record ends the declarations; the record after it starts the
	// environment, which this fixture leaves empty.
	records = append(records, "", "")
	return []byte(strings.Join(records, "\x00"))
}

// Rust #48099: a snapshot is rendered under its environment's shell environment
// policy, so filtered exports and the captured original of a policy-set variable
// never reach the file; a launch under another policy captures its own snapshot.
func TestSnapshotBuilderRendersUnderTheEnvironmentPolicyLikeRust(t *testing.T) {
	codexHome := t.TempDir()
	runner := &snapshotBuilderRunner{capture: snapshotCaptureStreamWith(
		[2]string{"FOO", `declare -x FOO="foo-sentinel"`},
		[2]string{"BAR", `declare -x BAR="bar-sentinel"`},
		[2]string{"SECRET_TOKEN", `declare -x SECRET_TOKEN="token-sentinel"`},
		[2]string{"PROFILE_SET", `declare -x PROFILE_SET="original-set-sentinel"`},
	)}
	builder := NewSnapshotBuilder(SnapshotBuilderOptions{CodexHome: codexHome, SessionID: "session-1", Runner: runner.run})
	t.Cleanup(builder.Close)
	policy := map[string]any{
		"ignore_default_excludes": false,
		"include_only":            []any{"FOO", "SECRET_TOKEN", "PROFILE_SET"},
		"set":                     map[string]any{"PROFILE_SET": "dummy"},
	}
	snapshot, reason := builder.Snapshot(context.Background(), SnapshotCaptureRequest{
		ShellType: ShellBash, ShellPath: "/bin/bash", CWD: "/repo", AllowLoginShell: true,
		EnvironmentPolicy: policy,
	})
	if reason != "" || snapshot == nil {
		t.Fatalf("Snapshot() = %v/%q", snapshot, reason)
	}
	content, err := os.ReadFile(snapshot.Path())
	if err != nil {
		t.Fatalf("read snapshot error = %v", err)
	}
	if !strings.Contains(string(content), `declare -x FOO="foo-sentinel"`) {
		t.Fatalf("an admitted export was dropped:\n%s", content)
	}
	for _, dropped := range []string{"bar-sentinel", "token-sentinel", "original-set-sentinel"} {
		if strings.Contains(string(content), dropped) {
			t.Fatalf("the snapshot retains %s:\n%s", dropped, content)
		}
	}

	// Another policy captures its own snapshot instead of replaying this one.
	other, reason := builder.Snapshot(context.Background(), SnapshotCaptureRequest{
		ShellType: ShellBash, ShellPath: "/bin/bash", CWD: "/repo", AllowLoginShell: true,
		EnvironmentPolicy: map[string]any{"include_only": []any{"BAR"}},
	})
	if reason != "" || other == nil {
		t.Fatalf("Snapshot() with another policy = %v/%q", other, reason)
	}
	if other.Path() == snapshot.Path() {
		t.Fatal("a snapshot captured under another policy was reused")
	}
	otherContent, err := os.ReadFile(other.Path())
	if err != nil {
		t.Fatalf("read snapshot error = %v", err)
	}
	if !strings.Contains(string(otherContent), "bar-sentinel") || strings.Contains(string(otherContent), "foo-sentinel") {
		t.Fatalf("the second policy did not filter its own snapshot:\n%s", otherContent)
	}
}

// TestSnapshotBuilderPrunesWithTheRolloutLookupLikeRust covers Rust's
// cleanup_stale_snapshots wiring: the first capture prunes snapshots whose
// session has no rollout, and keeps a live session's.
func TestSnapshotBuilderPrunesWithTheRolloutLookupLikeRust(t *testing.T) {
	codexHome := t.TempDir()
	dir := filepath.Join(codexHome, "shell_snapshots")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	deadPath := filepath.Join(dir, "gone.1.sh")
	livePath := filepath.Join(dir, "live.1.sh")
	for _, path := range []string{deadPath, livePath} {
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", path, err)
		}
	}
	runner := &snapshotBuilderRunner{capture: snapshotBashCaptureStream()}
	builder := NewSnapshotBuilder(SnapshotBuilderOptions{
		CodexHome: codexHome,
		SessionID: "session-1",
		Runner:    runner.run,
		Prune: func(sessionID string) (time.Time, bool) {
			if sessionID == "live" {
				return time.Now().Add(-time.Hour), true
			}
			return time.Time{}, false
		},
	})
	t.Cleanup(builder.Close)
	if created, _ := builder.Snapshot(context.Background(), SnapshotCaptureRequest{
		ShellType: ShellBash, ShellPath: "/bin/bash", CWD: "/repo", AllowLoginShell: true,
	}); created == nil {
		t.Fatal("Snapshot() = nil")
	}
	if _, err := os.Stat(deadPath); !os.IsNotExist(err) {
		t.Fatalf("a dead session's snapshot survived (err = %v)", err)
	}
	if _, err := os.Stat(livePath); err != nil {
		t.Fatalf("a live session's snapshot was pruned: %v", err)
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
