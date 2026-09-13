package appserver

import (
	"time"

	"codex_go/telemetry"
)

// Rust parity: codex-rs/otel/src/events/session_telemetry.rs
// (record_startup_phase) and core/src/session_startup_prewarm.rs's prewarm
// metrics.

// recordStartupPhase mirrors SessionTelemetry::record_startup_phase: the phase
// duration with a "phase" tag and, when the phase resolved, a "status" tag.
func (r *RuntimeRouter) recordStartupPhase(phase string, duration time.Duration, status string) {
	if r == nil || r.services.TurnMetrics == nil || phase == "" {
		return
	}
	if duration < 0 {
		duration = 0
	}
	tags := map[string]string{"phase": phase}
	if status != "" {
		tags["status"] = status
	}
	r.services.TurnMetrics.RecordDuration(telemetry.StartupPhaseDurationMetric, duration, tags)
}

// recordStartupPrewarmDuration mirrors the prewarm task's
// STARTUP_PREWARM_DURATION_METRIC sample (status "ready"/"failed").
func (r *RuntimeRouter) recordStartupPrewarmDuration(status string, duration time.Duration) {
	if r == nil || r.services.TurnMetrics == nil {
		return
	}
	if duration < 0 {
		duration = 0
	}
	r.services.TurnMetrics.RecordDuration(telemetry.StartupPrewarmDurationMetric, duration, map[string]string{
		"status": status,
	})
}

// recordStartupPrewarmAgeAtFirstTurn mirrors the resolve path's
// STARTUP_PREWARM_AGE_AT_FIRST_TURN_METRIC sample (status "consumed" or the
// unavailable status).
func (r *RuntimeRouter) recordStartupPrewarmAgeAtFirstTurn(status string, duration time.Duration) {
	if r == nil || r.services.TurnMetrics == nil {
		return
	}
	if duration < 0 {
		duration = 0
	}
	r.services.TurnMetrics.RecordDuration(telemetry.StartupPrewarmAgeAtFirstTurnMetric, duration, map[string]string{
		"status": status,
	})
}
