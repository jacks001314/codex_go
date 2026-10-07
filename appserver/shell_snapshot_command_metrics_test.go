package appserver

import (
	"context"
	"os"
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

// Rust #51347 (`588f616e8b`): `codex.shell_snapshot.command` and
// `codex.shell_snapshot.wait_ms` observe every eligible command preparation in
// the unified exec runtime, tagged by version, the snapshot availability state
// (protected / prewarm_ready / prewarm_pending / unavailable) and whether the
// replay was selected (`used`) or normal shell startup was used (`fallback`).
// Rust's `provisioned_environment_waits_for_offline_executor_on_the_same_handle`
// sibling suite is unrelated; these cases parallel `snapshot_metrics.rs` plus
// the approvals-suite check that a login-shell command reports one `protected`
// observation.

// snapshotCommandMetricsExporter is the in-memory sink behind the OTEL metrics
// client seam (state.TaskMetricsExporter), so the test asserts what the metrics
// client would export rather than an internal record.
type snapshotCommandMetricsExporter struct {
	mu        sync.Mutex
	counters  []snapshotCommandSample
	durations []snapshotCommandSample
}

type snapshotCommandSample struct {
	name  string
	inc   int
	value time.Duration
	tags  map[string]string
}

func (e *snapshotCommandMetricsExporter) Counter(name string, inc int, tags map[string]string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.counters = append(e.counters, snapshotCommandSample{name: name, inc: inc, tags: cloneSnapshotCommandTags(tags)})
}

func (e *snapshotCommandMetricsExporter) Histogram(string, int, map[string]string) {}

func (e *snapshotCommandMetricsExporter) HistogramWithBounds(string, int, []float64, map[string]string) {
}

func (e *snapshotCommandMetricsExporter) RecordDuration(name string, value time.Duration, tags map[string]string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.durations = append(e.durations, snapshotCommandSample{name: name, value: value, tags: cloneSnapshotCommandTags(tags)})
}

// samples returns the counters and durations exported for one metric name.
func (e *snapshotCommandMetricsExporter) samples(name string) ([]snapshotCommandSample, []snapshotCommandSample) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var counters []snapshotCommandSample
	for _, sample := range e.counters {
		if sample.name == name {
			counters = append(counters, sample)
		}
	}
	var durations []snapshotCommandSample
	for _, sample := range e.durations {
		if sample.name == name {
			durations = append(durations, sample)
		}
	}
	return counters, durations
}

func cloneSnapshotCommandTags(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

// snapshotCommandShellRunner records the launch the executor produced.
type snapshotCommandShellRunner struct {
	mu       sync.Mutex
	launched *tool.ShellRequest
}

func (r *snapshotCommandShellRunner) Run(_ context.Context, req *tool.ShellRequest) (*tool.ShellResult, error) {
	r.mu.Lock()
	r.launched = req
	r.mu.Unlock()
	return &tool.ShellResult{ExitCode: 0, Stdout: "ok\n"}, nil
}

func (r *snapshotCommandShellRunner) command() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.launched == nil {
		return nil
	}
	return append([]string(nil), r.launched.Command...)
}

// newShellSnapshotCommandRouter starts a thread whose metrics export into an
// in-memory sink, mirroring the OTEL metrics client wiring.
func newShellSnapshotCommandRouter(t *testing.T, configBody string) (*RuntimeRouter, string, string, *snapshotCommandMetricsExporter) {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte(configBody), 0o600); err != nil {
		t.Fatalf("write config error = %v", err)
	}
	metrics := state.NewTaskMetrics()
	exporter := &snapshotCommandMetricsExporter{}
	metrics.SetExporter(exporter)
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
	return router, threadStart.Result.(*ThreadStartResponse).Thread.ID, cwd, exporter
}

// runSnapshotCommandLaunch drives one real shell-executor launch through the
// app-server's snapshot provider, so the observation travels the production
// path (provider -> tool wrap decision -> observation hook -> metrics).
func runSnapshotCommandLaunch(t *testing.T, router *RuntimeRouter, threadID string, cwd string, mode tool.UnifiedExecShellMode) *snapshotCommandShellRunner {
	t.Helper()
	cfg := router.effectiveWriteStdinConfig(threadID)
	provider := router.shellSnapshotProviderForTurn(threadID, cfg)
	if provider == nil {
		t.Fatal("no snapshot provider")
	}
	profile := sandbox.WorkspaceWritePermissionProfile()
	runner := &snapshotCommandShellRunner{}
	executor := tool.NewShellExecutor(&tool.ShellExecutorOptions{
		Runner: runner,
		Shell:  &tool.Shell{Type: tool.ShellBash, Path: "/bin/bash"},
		Validation: tool.ShellValidationOptions{
			ApprovalPolicy:      sandbox.ApprovalOnRequest,
			AllowLoginShell:     true,
			CWD:                 cwd,
			DefaultTimeoutMS:    5000,
			PermissionProfile:   &profile,
			PermissionProfileID: "resolved",
			ShellMode:           mode,
		},
		SnapshotProvider: provider,
	})
	if _, err := executor.Execute(context.Background(), &tool.Invocation{
		CallID:   "call-snapshot-command",
		ToolName: tool.PlainName(tool.DefaultExecCommandToolName),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"cmd":"echo hi"}`},
	}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	return runner
}

func assertSnapshotCommandObservation(t *testing.T, exporter *snapshotCommandMetricsExporter, index int, state string, outcome string) {
	t.Helper()
	counters, _ := exporter.samples(telemetry.ShellSnapshotCommandMetric)
	if len(counters) <= index {
		t.Fatalf("command counters = %#v, want an observation at %d", counters, index)
	}
	if counters[index].inc != 1 || counters[index].tags["version"] != "v1" ||
		counters[index].tags["state"] != state || counters[index].tags["outcome"] != outcome {
		t.Fatalf("command counter = %#v, want version=v1 state=%s outcome=%s", counters[index], state, outcome)
	}
	waitCounters, waits := exporter.samples(telemetry.ShellSnapshotCommandWaitMetric)
	if len(waitCounters) != 0 {
		t.Fatalf("wait metric recorded %d counters, want durations", len(waitCounters))
	}
	if len(waits) <= index {
		t.Fatalf("command waits = %#v, want an observation at %d", waits, index)
	}
	if waits[index].value < 0 || waits[index].tags["version"] != "v1" ||
		waits[index].tags["state"] != state || waits[index].tags["outcome"] != outcome {
		t.Fatalf("command wait = %#v, want version=v1 state=%s outcome=%s", waits[index], state, outcome)
	}
}

// TestShellSnapshotCommandMetricsLikeRust covers the metric denominator and the
// `used` outcome: a launch that replays the session snapshot reports `used`, and
// a second launch in the same session sees the now-cached snapshot as
// `prewarm_ready`.
func TestShellSnapshotCommandMetricsLikeRust(t *testing.T) {
	stubShellSnapshotRunner(t)
	// No environment is registered, so the session's prewarm cannot resolve a
	// shell and does not run: the first launch finds an empty cache
	// (`prewarm_pending`) and captures on demand, the second finds it cached
	// (`prewarm_ready`).
	router, threadID, cwd, exporter := newShellSnapshotCommandRouter(t, "sandbox_mode = \"workspace-write\"\n")

	first := runSnapshotCommandLaunch(t, router, threadID, cwd, tool.UnifiedExecShellModeDirect)
	command := first.command()
	if len(command) != 3 || command[1] != "-c" || !strings.Contains(command[2], "exec '/bin/bash'") {
		t.Fatalf("launch command = %#v, want the snapshot replay wrapper", command)
	}
	assertSnapshotCommandObservation(t, exporter, 0, "prewarm_pending", "used")

	second := runSnapshotCommandLaunch(t, router, threadID, cwd, tool.UnifiedExecShellModeDirect)
	if got := second.command(); len(got) != 3 || got[1] != "-c" {
		t.Fatalf("second launch command = %#v, want the snapshot replay wrapper", got)
	}
	assertSnapshotCommandObservation(t, exporter, 1, "prewarm_ready", "used")

	counters, _ := exporter.samples(telemetry.ShellSnapshotCommandMetric)
	if len(counters) != 2 {
		t.Fatalf("command counters = %#v, want exactly two observations", counters)
	}
}

// TestShellSnapshotCommandMetricsReportProtectedAndUnavailableLikeRust covers
// the remaining availability states: a credential-broker session reports
// `protected`, and a snapshot that cannot be captured reports `unavailable`
// with a `fallback` outcome.
func TestShellSnapshotCommandMetricsReportProtectedAndUnavailableLikeRust(t *testing.T) {
	t.Run("protected", func(t *testing.T) {
		stubShellSnapshotRunner(t)
		// [features.network_proxy] credential_broker makes the session's
		// snapshots protected (Rust `should_rebuild_inherited`).
		router, threadID, cwd, exporter := newShellSnapshotCommandRouter(t,
			"sandbox_mode = \"workspace-write\"\n[features.network_proxy]\nenabled = true\ncredential_broker = true\n")
		runSnapshotCommandLaunch(t, router, threadID, cwd, tool.UnifiedExecShellModeDirect)
		assertSnapshotCommandObservation(t, exporter, 0, "protected", "used")
	})

	t.Run("unavailable", func(t *testing.T) {
		// A capture stream the parser cannot use fails the snapshot, so the
		// launch falls back to normal shell startup.
		stubShellSnapshotRunnerWithStream(t, []byte("not a snapshot"))
		router, threadID, cwd, exporter := newShellSnapshotCommandRouter(t, "sandbox_mode = \"workspace-write\"\n")
		command := runSnapshotCommandLaunch(t, router, threadID, cwd, tool.UnifiedExecShellModeDirect).command()
		if len(command) != 3 || command[1] != "-lc" {
			t.Fatalf("launch command = %#v, want the unwrapped command", command)
		}
		assertSnapshotCommandObservation(t, exporter, 0, "unavailable", "fallback")
	})
}

// TestShellSnapshotCommandMetricsSkipNonDirectLaunchesLikeRust pins Rust's
// `shell_mode == Direct` gate: a zsh-fork launch is outside the command-metric
// denominator (it still replays the snapshot, but reports no observation).
func TestShellSnapshotCommandMetricsSkipNonDirectLaunchesLikeRust(t *testing.T) {
	stubShellSnapshotRunner(t)
	router, threadID, cwd, exporter := newShellSnapshotCommandRouter(t, "sandbox_mode = \"workspace-write\"\n")

	command := runSnapshotCommandLaunch(t, router, threadID, cwd, tool.UnifiedExecShellModeZshFork).command()
	if len(command) != 3 || command[1] != "-c" {
		t.Fatalf("zsh-fork launch command = %#v, want the snapshot replay wrapper", command)
	}
	if counters, _ := exporter.samples(telemetry.ShellSnapshotCommandMetric); len(counters) != 0 {
		t.Fatalf("non-Direct launch reported %#v, want no observation", counters)
	}
	// The capture itself is still measured; only the command observation is gated.
	if counters, _ := exporter.samples(telemetry.ShellSnapshotCountMetric); len(counters) == 0 {
		t.Fatal("the capture was not measured")
	}
}
