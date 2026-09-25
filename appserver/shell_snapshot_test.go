package appserver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/sandbox"
	"codex_go/session"
	"codex_go/state"
	"codex_go/telemetry"
	"codex_go/tool"
)

// snapshotTestCaptureStream is the capture stream a bash capture produces.
func snapshotTestCaptureStream() []byte {
	return []byte(strings.Join([]string{
		"# Snapshot file\nalias probe='echo probe'\n",
		"export FOO='bar'\n",
		"FOO", "declare -x FOO=\"bar\"",
		// The empty key record ends the declarations, and the record after it
		// starts the environment.
		"", "",
	}, "\x00"))
}

// stubShellSnapshotRunner answers captures with a canned stream so the app
// server's snapshot lifecycle can be tested without a POSIX shell.
func stubShellSnapshotRunner(t *testing.T) {
	t.Helper()
	previous := shellSnapshotCaptureRunner
	shellSnapshotCaptureRunner = func(
		_ context.Context,
		command []string,
		_ string,
		_ map[string]string,
		_ *sandbox.PermissionProfile,
		_ string,
	) ([]byte, error) {
		if len(command) >= 3 && strings.HasPrefix(command[2], "set -e; . ") {
			return nil, nil
		}
		return snapshotTestCaptureStream(), nil
	}
	t.Cleanup(func() { shellSnapshotCaptureRunner = previous })
}

// newShellSnapshotRouter starts a thread in a temp codex home whose config can
// enable or disable the shell_snapshot feature.
func newShellSnapshotRouter(t *testing.T, configBody string) (*RuntimeRouter, string, string) {
	t.Helper()
	home := t.TempDir()
	if configBody != "" {
		if err := os.WriteFile(config.ConfigPath(home), []byte(configBody), 0o600); err != nil {
			t.Fatalf("write config error = %v", err)
		}
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(t.TempDir())),
		Config:       config.NewConfigService(home),
	})
	cwd := t.TempDir()
	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: cwd}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	return router, threadStart.Result.(*ThreadStartResponse).Thread.ID, cwd
}

// TestShellSnapshotProviderCapturesAndReleasesLikeRust covers the session
// lifecycle: the snapshot is captured once for the session's own directory,
// reused afterwards, and removed when the thread unloads.
func TestShellSnapshotProviderCapturesAndReleasesLikeRust(t *testing.T) {
	stubShellSnapshotRunner(t)
	router, threadID, cwd := newShellSnapshotRouter(t, "sandbox_mode = \"workspace-write\"\n")
	t.Cleanup(func() { _ = router.Close() })
	cfg := router.effectiveWriteStdinConfig(threadID)
	if cfg == nil {
		t.Fatal("effectiveWriteStdinConfig() = nil")
	}
	provider := router.shellSnapshotProviderForTurn(threadID, cfg)
	if provider == nil {
		t.Fatal("a session with the shell_snapshot feature has no provider")
	}
	request := tool.SnapshotProviderRequest{
		ShellType:           tool.ShellBash,
		ShellPath:           "/bin/bash",
		CWD:                 cwd,
		AllowLoginShell:     true,
		PermissionProfileID: "resolved",
	}
	path := provider(context.Background(), request)
	if path == "" {
		t.Fatal("the session's own directory did not get a snapshot")
	}
	if filepath.Dir(path) != filepath.Join(router.codexHomeForRollout(), "shell_snapshots") {
		t.Fatalf("snapshot path = %q", path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read snapshot error = %v", err)
	}
	if !strings.Contains(string(content), "# Snapshot file") ||
		!strings.Contains(string(content), `declare -x FOO="bar"`) {
		t.Fatalf("snapshot content = %q", content)
	}
	if reused := provider(context.Background(), request); reused != path {
		t.Fatalf("second launch reused %q, want %q", reused, path)
	}

	// Another directory has no captured state, so the launch is left alone.
	other := request
	other.CWD = t.TempDir()
	if got := provider(context.Background(), other); got != "" {
		t.Fatalf("a launch outside the session directory got snapshot %q", got)
	}

	router.markThreadUnloaded(threadID)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("unloading the thread left %s behind (err = %v)", path, err)
	}
}

// TestShellSnapshotProviderFollowsTheFeatureAndLaunchShapeLikeRust pins the
// gates: the feature must be on, and a remote environment, a non-POSIX shell or
// a non-login launch gets no snapshot.
func TestShellSnapshotProviderFollowsTheFeatureAndLaunchShapeLikeRust(t *testing.T) {
	stubShellSnapshotRunner(t)
	router, threadID, cwd := newShellSnapshotRouter(t, "sandbox_mode = \"workspace-write\"\n[features]\nshell_snapshot = false\n")
	t.Cleanup(func() { _ = router.Close() })
	cfg := router.effectiveWriteStdinConfig(threadID)
	if cfg == nil {
		t.Fatal("effectiveWriteStdinConfig() = nil")
	}
	if provider := router.shellSnapshotProviderForTurn(threadID, cfg); provider != nil {
		t.Fatal("a session with shell_snapshot disabled must not have a provider")
	}
	if provider := router.shellSnapshotProviderForTurn("", cfg); provider != nil {
		t.Fatal("a session without a thread id must not have a provider")
	}

	enabled := stubShellSnapshotFeature(t, router, threadID)
	if enabled == nil {
		t.Fatal("enabling shell_snapshot did not produce a provider")
	}
	base := tool.SnapshotProviderRequest{
		ShellType:       tool.ShellBash,
		ShellPath:       "/bin/bash",
		CWD:             cwd,
		AllowLoginShell: true,
	}
	cases := []struct {
		name    string
		mutate  func(request *tool.SnapshotProviderRequest)
		wantGap string
	}{
		{"remote environment", func(request *tool.SnapshotProviderRequest) { request.Remote = true }, "remote"},
		{"PowerShell", func(request *tool.SnapshotProviderRequest) { request.ShellType = tool.ShellPowerShell }, "shell"},
		{"non-login launch", func(request *tool.SnapshotProviderRequest) { request.AllowLoginShell = false }, "login"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			request := base
			testCase.mutate(&request)
			if got := enabled(context.Background(), request); got != "" {
				t.Fatalf("snapshot = %q, want none for the %s gap", got, testCase.wantGap)
			}
		})
	}
}

// stubShellSnapshotFeature turns the feature back on for the thread's config.
func stubShellSnapshotFeature(t *testing.T, router *RuntimeRouter, threadID string) func(context.Context, tool.SnapshotProviderRequest) string {
	t.Helper()
	home := router.codexHomeForRollout()
	if err := os.WriteFile(config.ConfigPath(home), []byte("sandbox_mode = \"workspace-write\"\n[features]\nshell_snapshot = true\n"), 0o600); err != nil {
		t.Fatalf("write config error = %v", err)
	}
	cfg := router.effectiveWriteStdinConfig(threadID)
	if cfg == nil {
		return nil
	}
	return router.shellSnapshotProviderForTurn(threadID, cfg)
}

// TestShellSnapshotRecordsMetricsLikeRust covers Rust's shell-snapshot
// telemetry: one duration sample and one count per capture attempt tagged with
// the version and the outcome, and the count also carrying the failure reason.
func TestShellSnapshotRecordsMetricsLikeRust(t *testing.T) {
	stubShellSnapshotRunner(t)
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte("sandbox_mode = \"workspace-write\"\n"), 0o600); err != nil {
		t.Fatalf("write config error = %v", err)
	}
	metrics := state.NewTaskMetrics()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(t.TempDir())),
		Config:       config.NewConfigService(home),
		TurnMetrics:  metrics,
	})
	t.Cleanup(func() { _ = router.Close() })
	cwd := t.TempDir()
	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: cwd}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	cfg := router.effectiveWriteStdinConfig(threadID)
	provider := router.shellSnapshotProviderForTurn(threadID, cfg)
	if provider == nil {
		t.Fatal("no snapshot provider")
	}
	request := tool.SnapshotProviderRequest{
		ShellType:       tool.ShellBash,
		ShellPath:       "/bin/bash",
		CWD:             cwd,
		AllowLoginShell: true,
	}
	if provider(context.Background(), request) == "" {
		t.Fatal("no snapshot for the session's directory")
	}
	durations := snapshotMetricRecords(metrics, telemetry.ShellSnapshotDurationMetric)
	if len(durations) != 1 {
		t.Fatalf("duration records = %#v", durations)
	}
	if durations[0].Kind != "duration" || durations[0].Tags["version"] != "v1" ||
		durations[0].Tags["success"] != "true" || durations[0].DurationMS < 0 {
		t.Fatalf("duration record = %#v", durations[0])
	}
	counts := snapshotMetricRecords(metrics, telemetry.ShellSnapshotCountMetric)
	if len(counts) != 1 || counts[0].Kind != "counter" || counts[0].Inc != 1 ||
		counts[0].Tags["version"] != "v1" || counts[0].Tags["success"] != "true" ||
		counts[0].Tags["failure_reason"] != "" {
		t.Fatalf("count record = %#v", counts)
	}

	// A capture that cannot be sourced is reported with its reason.
	previous := shellSnapshotCaptureRunner
	shellSnapshotCaptureRunner = func(
		_ context.Context,
		command []string,
		_ string,
		_ map[string]string,
		_ *sandbox.PermissionProfile,
		_ string,
	) ([]byte, error) {
		if len(command) >= 3 && strings.HasPrefix(command[2], "set -e; . ") {
			return nil, errSnapshotValidationFailure
		}
		return snapshotTestCaptureStream(), nil
	}
	t.Cleanup(func() { shellSnapshotCaptureRunner = previous })
	failing := router.shellSnapshotProviderForTurn(threadID+"-failing", cfg)
	if failing == nil {
		t.Fatal("no snapshot provider for the failing thread")
	}
	if path := failing(context.Background(), request); path != "" {
		t.Fatalf("failing capture produced %q", path)
	}
	counts = snapshotMetricRecords(metrics, telemetry.ShellSnapshotCountMetric)
	if len(counts) != 2 {
		t.Fatalf("count records = %#v", counts)
	}
	failed := counts[1]
	if failed.Tags["success"] != "false" || failed.Tags["failure_reason"] != "validation_failed" {
		t.Fatalf("failure record = %#v", failed)
	}
}

// snapshotMetricRecords returns the recorded samples for one metric name.
func snapshotMetricRecords(metrics *state.TaskMetrics, name string) []*state.TaskMetric {
	var out []*state.TaskMetric
	for _, record := range metrics.Records() {
		if record.Name == name {
			out = append(out, record)
		}
	}
	return out
}

// errSnapshotValidationFailure stands in for a snapshot that cannot be sourced.
var errSnapshotValidationFailure = errors.New("validation failed")
