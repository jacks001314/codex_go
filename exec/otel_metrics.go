package exec

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"codex_go/config"
	"codex_go/doctor"
	"codex_go/otelinit"
	"codex_go/telemetry"
	"codex_go/turn"
)

// configureOtelProvider mirrors the exec crate's startup wiring
// (codex-rs/exec/src/lib.rs): build an OtelProvider from the effective config
// with the process originator as the service name and analytics enabled by
// default, record the process start once, and keep the provider so its metrics
// client can be installed on the model runner and shut down at the end of the
// run. Metrics stay inert unless an OTLP metrics exporter is configured (the
// default Statsig route is off in development builds).
func (r *Runner) configureOtelProvider(cfg *config.Config, req *Request) {
	if r == nil || cfg == nil {
		return
	}
	provider, err := otelinit.BuildProvider(otelinit.Options{
		Config:                  cfg,
		ServiceName:             execAgentOriginator(req),
		ServiceVersion:          doctor.Version(),
		DefaultAnalyticsEnabled: true,
	})
	if err != nil {
		slog.Warn("failed to build the OTEL provider", "error", err)
		return
	}
	if provider == nil || provider.Metrics() == nil {
		return
	}
	r.otelProvider = provider
	// Rust's exec records the process-start counter with the "codex_exec"
	// originator.
	telemetry.RecordProcessStartOnce(provider.Metrics(), "codex_exec")
}

// otelMetricsSink returns the run's metrics client when OTEL metrics are
// enabled.
func (r *Runner) otelMetricsSink() *telemetry.MetricsClient {
	if r == nil {
		return nil
	}
	return r.otelProvider.Metrics()
}

// shutdownOtelProvider flushes and stops the run's provider.
func (r *Runner) shutdownOtelProvider(ctx context.Context) {
	if r == nil {
		return
	}
	provider := r.otelProvider
	if provider == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	_ = provider.Shutdown(ctx)
}

// emitTurnMetrics mirrors the session metrics Rust records when a turn
// completes (codex-otel's SessionTelemetry via the turn-completion path): the
// per-model token-usage histogram, the per-turn tool-call count, the
// per-tool-call counter/duration pair, and the unified-exec running-process
// count. The end-to-end duration is emitted separately because it also covers
// failed and interrupted turns.
//
// Go's exec has no memories or managed-network-proxy subsystem, so Rust's
// codex.turn.memory and codex.turn.network_proxy counters are not emitted here.
func (r *Runner) emitTurnMetrics(result *turn.AgentLoopResult, threadID string, modelID string, memoryToolEnabled bool) {
	sink := r.otelMetricsSink()
	if sink == nil || result == nil {
		return
	}
	telemetry.EmitTurnTokenUsageMetrics(sink, result.ModelResponses(), modelID, memoryToolEnabled)
	telemetry.EmitTurnToolCallMetric(sink, len(result.ToolExecutions), memoryToolEnabled)
	for index := range result.ToolExecutions {
		telemetry.EmitToolCallMetric(sink, &result.ToolExecutions[index])
	}
	telemetry.EmitTurnRunningProcessesMetric(sink, r.runningUnifiedExecProcesses(threadID))
}

// emitTurnE2EDuration records the turn task's wall-clock duration (recorded for
// every terminal outcome, like Rust's turn timer).
func (r *Runner) emitTurnE2EDuration(startedAt time.Time) {
	sink := r.otelMetricsSink()
	if sink == nil {
		return
	}
	telemetry.EmitTurnE2EDurationMetric(sink, time.Since(startedAt).Milliseconds())
}

func (r *Runner) runningUnifiedExecProcesses(threadID string) int {
	if r == nil || r.UnifiedExec == nil {
		return 0
	}
	return len(r.UnifiedExec.ListProcesses(strings.TrimSpace(threadID)))
}
