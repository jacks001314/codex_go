package exec

import (
	"context"
	"log/slog"

	"codex_go/config"
	"codex_go/doctor"
	"codex_go/otelinit"
	"codex_go/telemetry"
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
