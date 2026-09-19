package telemetry

import "time"

// Turn-scoped OTel-style metric names live in metric_names.go
// (codex-rs/otel/src/metrics/names.rs).

// Turn metric tag keys.
const (
	TurnTokenTypeTag = "token_type"
	TurnTmpMemoryTag = "tmp_mem_enabled"
)

// Token usage tag values, in the order Rust emits them
// (codex-rs/core/src/state/turn_token_usage.rs).
const (
	TurnTokenTypeTotal           = "total"
	TurnTokenTypeInput           = "input"
	TurnTokenTypeCachedInput     = "cached_input"
	TurnTokenTypeCacheWriteInput = "cache_write_input"
	TurnTokenTypeOutput          = "output"
	TurnTokenTypeReasoningOutput = "reasoning_output"
)

// TurnMetricSink receives turn-scoped metrics. state.TaskMetrics implements it
// for the local process; it mirrors the subset of codex-otel's SessionTelemetry
// the turn completion path uses (#44656), including the explicit-boundary
// histogram the memory consolidation pipeline reports through the same session
// telemetry (#45956).
type TurnMetricSink interface {
	Counter(name string, inc int, tags map[string]string)
	Histogram(name string, value int, tags map[string]string)
	// HistogramWithBounds records a histogram value with explicit bucket
	// boundaries (codex-otel's histogram_with_boundaries).
	HistogramWithBounds(name string, value int, boundaries []float64, tags map[string]string)
	// RecordDuration records Rust's millisecond duration histogram (codex-otel's
	// `record_duration`, with the millisecond unit and bucket boundaries).
	RecordDuration(name string, duration time.Duration, tags map[string]string)
}

// CounterDurationSink receives counters and duration observations without the
// histogram the full TurnMetricSink takes. It is the shape the session-scoped
// emitters need (the skill-injection counters, the voice session lifecycle, and
// the memory-usage reader), so a caller that only has one of those can still
// emit through state.TaskMetrics.
type CounterDurationSink interface {
	Counter(name string, inc int, tags map[string]string)
	RecordDuration(name string, duration time.Duration, tags map[string]string)
}
