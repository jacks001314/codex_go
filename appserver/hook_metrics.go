package appserver

import (
	"time"

	"codex_go/telemetry"
)

// emitHookRunMetrics mirrors Rust's emit_hook_completed_metrics
// (codex-rs/core/src/hook_runtime.rs): one codex.hooks.run counter per completed
// hook run, tagged by the hook name, source, status, handler type, and execution
// mode, plus the codex.hooks.run.duration_ms histogram when the run recorded a
// duration.
func emitHookRunMetrics(sink telemetry.TurnMetricSink, run *HookRunSummary) {
	if sink == nil || run == nil {
		return
	}
	tags := map[string]string{
		"hook_name":      hookRunAnalyticsEventName(run.EventName),
		"source":         hookRunAnalyticsSource(run.Source),
		"status":         string(run.Status),
		"handler_type":   string(run.HandlerType),
		"execution_mode": string(run.ExecutionMode),
	}
	sink.Counter(telemetry.HookRunMetric, 1, tags)
	if run.DurationMS != nil {
		duration := *run.DurationMS
		if duration < 0 {
			duration = 0
		}
		sink.RecordDuration(telemetry.HookRunDurationMetric, time.Duration(duration)*time.Millisecond, tags)
	}
}
