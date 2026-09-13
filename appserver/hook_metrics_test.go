package appserver

import (
	"context"
	"testing"

	"codex_go/state"
	"codex_go/telemetry"
)

// Mirrors Rust's emit_hook_completed_metrics: the counter and duration histogram
// tagged by hook name, source, status, handler type, and execution mode.
func TestEmitHookRunMetricsLikeRust(t *testing.T) {
	sink := state.NewTaskMetrics()
	duration := int64(125)
	emitHookRunMetrics(sink, &HookRunSummary{
		EventName:     HookEventSessionStart,
		Source:        HookSourceProject,
		Status:        HookRunCompleted,
		HandlerType:   HookHandlerCommand,
		ExecutionMode: HookExecutionSync,
		DurationMS:    &duration,
	})
	records := sink.Records()
	if len(records) != 2 {
		t.Fatalf("records = %#v", records)
	}
	counter := records[0]
	if counter.Name != telemetry.HookRunMetric || counter.Kind != "counter" || counter.Inc != 1 {
		t.Fatalf("counter = %#v", counter)
	}
	for key, want := range map[string]string{
		"hook_name":      "SessionStart",
		"source":         "project",
		"status":         "completed",
		"handler_type":   "command",
		"execution_mode": "sync",
	} {
		if counter.Tags[key] != want {
			t.Fatalf("counter tag %s = %q, want %q (%#v)", key, counter.Tags[key], want, counter.Tags)
		}
	}
	if duration := records[1]; duration.Name != telemetry.HookRunDurationMetric || duration.DurationMS != 125 {
		t.Fatalf("duration = %#v", duration)
	}

	// Expanded sources and the SessionEnd/Interrupt labels match Rust's.
	eventNames := map[HookEventName]string{
		HookEventSessionEnd:  "SessionEnd",
		HookEventInterrupt:   "Interrupt",
		HookEventPreToolUse:  "PreToolUse",
		HookEventPostCompact: "PostCompact",
	}
	for event, want := range eventNames {
		if got := hookRunAnalyticsEventName(event); got != want {
			t.Fatalf("hookRunAnalyticsEventName(%q) = %q, want %q", event, got, want)
		}
	}
	sources := map[HookSource]string{
		HookSourceSessionFlags:       "session_flags",
		HookSourceCloudManagedConfig: "cloud_managed_config",
		HookSourceLegacyConfigMDM:    "legacy_managed_config_mdm",
		HookSourcePlugin:             "plugin",
	}
	for source, want := range sources {
		if got := hookRunAnalyticsSource(source); got != want {
			t.Fatalf("hookRunAnalyticsSource(%q) = %q, want %q", source, got, want)
		}
	}

	// A run without a duration records only the counter; nil inputs are no-ops.
	withoutDuration := state.NewTaskMetrics()
	emitHookRunMetrics(withoutDuration, &HookRunSummary{EventName: HookEventStop, Status: HookRunBlocked})
	if records := withoutDuration.Records(); len(records) != 1 {
		t.Fatalf("records = %#v", records)
	}
	emitHookRunMetrics(nil, &HookRunSummary{})
	emitHookRunMetrics(state.NewTaskMetrics(), nil)
}

// The hook runner reports the completed-run metrics through its installed sink.
func TestHookRunnerEmitsHookRunMetricsLikeRust(t *testing.T) {
	metrics := state.NewTaskMetrics()
	runner := NewHookRunner()
	runner.SetMetrics(metrics)
	command := hookRunnerOutputCommand(`{}`, "")
	hook := hookRunnerMetadata("hook-metrics", HookEventSessionStart, "", 0)
	hook.Command = &command

	if _, err := runner.Run(context.Background(), &HookRunRequest{
		ThreadID:  "thread-1",
		CWD:       t.TempDir(),
		EventName: HookEventSessionStart,
		InputJSON: "{}",
		Hooks:     []HookMetadata{hook},
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	hookRuns := 0
	for _, record := range metrics.Records() {
		if record.Name != telemetry.HookRunMetric {
			continue
		}
		hookRuns++
		if record.Tags["hook_name"] != "SessionStart" || record.Tags["status"] != "completed" {
			t.Fatalf("hook run record = %#v", record)
		}
	}
	if hookRuns != 1 {
		t.Fatalf("hook run counters = %d (records %#v)", hookRuns, metrics.Records())
	}
}
