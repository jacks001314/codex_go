package appserver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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
	stubShellSnapshotRunnerWithStream(t, snapshotTestCaptureStream())
}

// stubShellSnapshotRunnerWithStream answers captures with the given stream.
func stubShellSnapshotRunnerWithStream(t *testing.T, stream []byte) {
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
		return stream, nil
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
	path := provider(context.Background(), request).Path
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
	if reused := provider(context.Background(), request).Path; reused != path {
		t.Fatalf("second launch reused %q, want %q", reused, path)
	}

	// Another directory has no captured state, so the launch is left alone.
	other := request
	other.CWD = t.TempDir()
	if got := provider(context.Background(), other).Path; got != "" {
		t.Fatalf("a launch outside the session directory got snapshot %q", got)
	}

	router.markThreadUnloaded(threadID)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("unloading the thread left %s behind (err = %v)", path, err)
	}
}

// TestShellSnapshotProviderFollowsTheFeatureAndLaunchShapeLikeRust pins the
// gates: the feature must be on, and a non-POSIX shell or non-login launch gets
// no snapshot. Remote environments return an executor-owned snapshot request.
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
	remoteRequest := base
	remoteRequest.Remote = true
	remoteSnapshot := enabled(context.Background(), remoteRequest)
	if remoteSnapshot.Remote == nil || remoteSnapshot.Remote.ScopeID != threadID ||
		remoteSnapshot.Remote.Shell.Path != "/bin/bash" || remoteSnapshot.Path != "" {
		t.Fatalf("remote snapshot request = %#v", remoteSnapshot)
	}
	cases := []struct {
		name    string
		mutate  func(request *tool.SnapshotProviderRequest)
		wantGap string
	}{
		{"PowerShell", func(request *tool.SnapshotProviderRequest) { request.ShellType = tool.ShellPowerShell }, "shell"},
		{"non-login launch", func(request *tool.SnapshotProviderRequest) { request.AllowLoginShell = false }, "login"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			request := base
			testCase.mutate(&request)
			if got := enabled(context.Background(), request).Path; got != "" {
				t.Fatalf("snapshot = %q, want none for the %s gap", got, testCase.wantGap)
			}
		})
	}
}

// Rust #48099: the session snapshot is captured under the thread's
// shell_environment_policy, so exports the policy filters never reach the file.
func TestShellSnapshotPrewarmHonorsTheEnvironmentPolicyLikeRust(t *testing.T) {
	stubShellSnapshotRunnerWithStream(t, []byte(strings.Join([]string{
		"# Snapshot file\nalias probe='echo probe'\n",
		"",
		"FOO", "declare -x FOO=\"foo-sentinel\"",
		"BAR", "declare -x BAR=\"bar-sentinel\"",
		"", "", "",
	}, "\x00")))
	configBody := "sandbox_mode = \"workspace-write\"\n[shell_environment_policy]\ninclude_only = [\"FOO\"]\n"
	router, threadID, cwd := newShellSnapshotRouter(t, configBody)
	t.Cleanup(func() { _ = router.Close() })
	router.services.Environment = NewEnvironmentManager(EnvironmentShellInfo{Name: "bash", Path: "/bin/bash"}, cwd)
	cfg := router.effectiveWriteStdinConfig(threadID)
	if provider := router.shellSnapshotProviderForTurn(threadID, cfg); provider == nil {
		t.Fatal("no snapshot provider")
	}
	snapshotDir := filepath.Join(router.codexHomeForRollout(), "shell_snapshots")
	deadline := time.Now().Add(2 * time.Second)
	var content []byte
	for time.Now().Before(deadline) {
		entries, err := os.ReadDir(snapshotDir)
		if err == nil && len(entries) == 1 {
			content, err = os.ReadFile(filepath.Join(snapshotDir, entries[0].Name()))
			if err == nil && strings.Contains(string(content), "foo-sentinel") {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(string(content), "foo-sentinel") {
		t.Fatalf("the prewarmed snapshot misses the admitted export:\n%s", content)
	}
	if strings.Contains(string(content), "bar-sentinel") {
		t.Fatalf("the prewarmed snapshot ignored the environment policy:\n%s", content)
	}
}

// recordedSnapshotCapture is one capture run the recording runner saw.
type recordedSnapshotCapture struct {
	argv    []string
	profile *sandbox.PermissionProfile
}

// recordingSnapshotRunner records every capture so a test can see whether the
// session prewarmed once, reused it, and captured without a sandbox.
type recordingSnapshotRunner struct {
	mu       sync.Mutex
	captures []recordedSnapshotCapture
}

func (r *recordingSnapshotRunner) run(
	_ context.Context,
	command []string,
	_ string,
	_ map[string]string,
	profile *sandbox.PermissionProfile,
	_ string,
) ([]byte, error) {
	if len(command) >= 3 && strings.HasPrefix(command[2], "set -e; . ") {
		return nil, nil
	}
	r.mu.Lock()
	r.captures = append(r.captures, recordedSnapshotCapture{argv: append([]string(nil), command...), profile: profile})
	r.mu.Unlock()
	return snapshotTestCaptureStream(), nil
}

func (r *recordingSnapshotRunner) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.captures)
}

func (r *recordingSnapshotRunner) capture(index int) recordedSnapshotCapture {
	r.mu.Lock()
	defer r.mu.Unlock()
	if index < 0 || index >= len(r.captures) {
		return recordedSnapshotCapture{}
	}
	return r.captures[index]
}

// Rust prewarms the session snapshot when the environment resolves
// (`start_shell_snapshot_task`): the capture runs in the background with a login
// shell and no sandbox, and every launch in the session's directory reuses it
// instead of capturing again.
func TestShellSnapshotPrewarmsAndReusesLikeRust(t *testing.T) {
	runner := &recordingSnapshotRunner{}
	previous := shellSnapshotCaptureRunner
	shellSnapshotCaptureRunner = runner.run
	t.Cleanup(func() { shellSnapshotCaptureRunner = previous })
	router, threadID, cwd := newShellSnapshotRouter(t, "sandbox_mode = \"workspace-write\"\n")
	t.Cleanup(func() { _ = router.Close() })
	router.services.Environment = NewEnvironmentManager(EnvironmentShellInfo{Name: "bash", Path: "/bin/bash"}, cwd)
	cfg := router.effectiveWriteStdinConfig(threadID)
	provider := router.shellSnapshotProviderForTurn(threadID, cfg)
	if provider == nil {
		t.Fatal("no snapshot provider")
	}
	if !waitForSnapshotCaptures(runner, 1, 2*time.Second) {
		t.Fatalf("the session did not prewarm its snapshot (captures = %d)", runner.count())
	}
	prewarmed := runner.capture(0)
	if len(prewarmed.argv) < 3 || prewarmed.argv[1] != "-lc" {
		t.Fatalf("prewarm argv = %#v, want a login capture", prewarmed.argv)
	}
	if prewarmed.profile != nil {
		t.Fatalf("prewarm profile = %#v, want an unsandboxed capture", prewarmed.profile)
	}

	path := provider(context.Background(), tool.SnapshotProviderRequest{
		ShellType:       tool.ShellBash,
		ShellPath:       "/bin/bash",
		CWD:             cwd,
		AllowLoginShell: true,
	}).Path
	if path == "" {
		t.Fatal("the launch found no snapshot")
	}
	if captures := runner.count(); captures != 1 {
		t.Fatalf("captures = %d, want the prewarmed snapshot to be reused", captures)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("prewarmed snapshot missing: %v", err)
	}
}

// A session with the credential broker configured keeps its snapshots lazy
// (Rust's `should_rebuild_inherited`): protected captures need the command's
// sandbox and credential preparation.
func TestShellSnapshotSkipsPrewarmWithTheCredentialBrokerLikeRust(t *testing.T) {
	runner := &recordingSnapshotRunner{}
	previous := shellSnapshotCaptureRunner
	shellSnapshotCaptureRunner = runner.run
	t.Cleanup(func() { shellSnapshotCaptureRunner = previous })
	brokerConfig := "sandbox_mode = \"workspace-write\"\n[features.network_proxy]\nenabled = true\ncredential_broker = true\n"
	router, threadID, cwd := newShellSnapshotRouter(t, brokerConfig)
	t.Cleanup(func() { _ = router.Close() })
	router.services.Environment = NewEnvironmentManager(EnvironmentShellInfo{Name: "bash", Path: "/bin/bash"}, cwd)
	cfg := router.effectiveWriteStdinConfig(threadID)
	if !shellSnapshotProtected(cfg) {
		t.Fatal("the broker configuration was not detected as protected")
	}
	provider := router.shellSnapshotProviderForTurn(threadID, cfg)
	if provider == nil {
		t.Fatal("no snapshot provider")
	}
	// Give a prewarm a chance to run: a protected session does not start one.
	if waitForSnapshotCaptures(runner, 1, 200*time.Millisecond) {
		t.Fatalf("a protected session prewarmed a snapshot (captures = %d)", runner.count())
	}
	if path := provider(context.Background(), tool.SnapshotProviderRequest{
		ShellType:       tool.ShellBash,
		ShellPath:       "/bin/bash",
		CWD:             cwd,
		AllowLoginShell: true,
	}).Path; path == "" {
		t.Fatal("the launch found no snapshot")
	}
	if captures := runner.count(); captures != 1 {
		t.Fatalf("captures = %d, want exactly the launch's capture", captures)
	}
}

// waitForSnapshotCaptures reports whether the runner reaches want captures within
// a short window, so a test can assert that no capture was started.
func waitForSnapshotCaptures(runner *recordingSnapshotRunner, want int, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if runner.count() >= want {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return runner.count() >= want
}

// TestShellSnapshotPruneLookupReadsTheRolloutLikeRust covers the state-db half of
// Rust's cleanup: a session with a stored rollout reports its age, and an unknown
// session reports nothing so its snapshots are pruned.
func TestShellSnapshotPruneLookupReadsTheRolloutLikeRust(t *testing.T) {
	router, threadID, _ := newShellSnapshotRouter(t, "")
	t.Cleanup(func() { _ = router.Close() })
	lookup := router.shellSnapshotPruneLookup()
	if lookup == nil {
		t.Fatal("shellSnapshotPruneLookup() = nil")
	}
	modified, ok := lookup(threadID)
	if !ok || modified.IsZero() {
		t.Fatalf("lookup(%s) = %v/%v, want the stored rollout's age", threadID, modified, ok)
	}
	if _, ok := lookup("01a0d700-0000-7000-8000-000000000000"); ok {
		t.Fatal("an unknown session reported a rollout")
	}
	if _, ok := lookup("  "); ok {
		t.Fatal("an empty session id reported a rollout")
	}
}

// stubShellSnapshotFeature turns the feature back on for the thread's config.
func stubShellSnapshotFeature(t *testing.T, router *RuntimeRouter, threadID string) func(context.Context, tool.SnapshotProviderRequest) tool.SnapshotProviderResult {
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
	if provider(context.Background(), request).Path == "" {
		t.Fatal("no snapshot for the session's directory")
	}
	durations := snapshotMetricRecords(metrics, telemetry.ShellSnapshotDurationMetric)
	v1Durations := snapshotMetricVersion(durations, "v1")
	v2Durations := snapshotMetricVersion(durations, "v2")
	if len(v1Durations) != 1 || len(v2Durations) != 1 {
		t.Fatalf("duration records = %#v", durations)
	}
	for _, duration := range []*state.TaskMetric{v1Durations[0], v2Durations[0]} {
		if duration.Kind != "duration" || duration.Tags["success"] != "true" || duration.DurationMS < 0 {
			t.Fatalf("duration record = %#v", duration)
		}
	}
	counts := snapshotMetricRecords(metrics, telemetry.ShellSnapshotCountMetric)
	v1Counts := snapshotMetricVersion(counts, "v1")
	v2Counts := snapshotMetricVersion(counts, "v2")
	if len(v1Counts) != 1 || len(v2Counts) != 1 {
		t.Fatalf("count record = %#v", counts)
	}
	for _, count := range []*state.TaskMetric{v1Counts[0], v2Counts[0]} {
		if count.Kind != "counter" || count.Inc != 1 || count.Tags["success"] != "true" ||
			count.Tags["failure_reason"] != "" {
			t.Fatalf("count record = %#v", count)
		}
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
	if path := failing(context.Background(), request).Path; path != "" {
		t.Fatalf("failing capture produced %q", path)
	}
	counts = snapshotMetricRecords(metrics, telemetry.ShellSnapshotCountMetric)
	v1Counts = snapshotMetricVersion(counts, "v1")
	v2Counts = snapshotMetricVersion(counts, "v2")
	if len(v1Counts) != 2 || len(v2Counts) != 2 {
		t.Fatalf("count records = %#v", counts)
	}
	failed := v1Counts[1]
	if failed.Tags["success"] != "false" || failed.Tags["failure_reason"] != "validation_failed" {
		t.Fatalf("failure record = %#v", failed)
	}
	failedExecServer := v2Counts[1]
	if failedExecServer.Tags["success"] != "false" || failedExecServer.Tags["failure_reason"] != "capture_failed" {
		t.Fatalf("exec-server failure record = %#v", failedExecServer)
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

func snapshotMetricVersion(records []*state.TaskMetric, version string) []*state.TaskMetric {
	out := make([]*state.TaskMetric, 0, len(records))
	for _, record := range records {
		if record.Tags["version"] == version {
			out = append(out, record)
		}
	}
	return out
}

// errSnapshotValidationFailure stands in for a snapshot that cannot be sourced.
var errSnapshotValidationFailure = errors.New("validation failed")
