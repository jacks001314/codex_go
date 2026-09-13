package telemetry

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
// the turn completion path uses (#44656).
type TurnMetricSink interface {
	Counter(name string, inc int, tags map[string]string)
	Histogram(name string, value int, tags map[string]string)
}
