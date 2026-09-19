package memories

import (
	"log/slog"
	"math"
	"time"

	"codex_go/config"
)

// Rust parity: codex-rs/memories/write/src/metrics.rs plus the metric methods of
// `MemoryStartupContext` in codex-rs/memories/write/src/runtime.rs.

const (
	// MemoryStartupMetric counts startup outcomes that stop the pipeline before
	// phase one (Rust MEMORY_STARTUP).
	MemoryStartupMetric = "codex.memory.startup"
	// MemoryPhaseOneJobsMetric counts stage-one jobs by status.
	MemoryPhaseOneJobsMetric = "codex.memory.phase1"
	// MemoryPhaseOneE2EMetric times a whole phase-one run.
	MemoryPhaseOneE2EMetric = "codex.memory.phase1.e2e_ms"
	// MemoryPhaseOneOutputMetric counts stage-one jobs that produced output.
	MemoryPhaseOneOutputMetric = "codex.memory.phase1.output"
	// MemoryPhaseTwoJobsMetric counts phase-two (consolidation) jobs by status.
	MemoryPhaseTwoJobsMetric = "codex.memory.phase2"
	// MemoryPhaseTwoE2EMetric times a whole phase-two run.
	MemoryPhaseTwoE2EMetric = "codex.memory.phase2.e2e_ms"
	// MemoryPhaseTwoInputMetric counts the raw memories a consolidation ran on.
	MemoryPhaseTwoInputMetric = "codex.memory.phase2.input"
	// MemoryStorageBytesMetric reports the memory root's on-disk size after a
	// successful consolidation, including a run that changed nothing
	// (Rust MEMORY_STORAGE_BYTES, #45956).
	MemoryStorageBytesMetric = "codex.memory.storage_bytes"
	// MemoryVersionTag names the memory root a consolidation metric belongs to
	// (Rust `memory_metric_tags`).
	MemoryVersionTag = "memory_version"
	// MemoryStatusTag names the outcome a job counter reports (Rust's `status`).
	MemoryStatusTag = "status"
)

// MemoryStorageBytesBoundaries are Rust's log-spaced byte buckets: they cover
// small summaries through large memory collections.
func MemoryStorageBytesBoundaries() []float64 {
	return []float64{
		0,
		1_024,
		4_096,
		16_384,
		65_536,
		262_144,
		1_048_576,
		4_194_304,
		16_777_216,
		67_108_864,
		268_435_456,
		1_073_741_824,
	}
}

// MemoryMetricSink receives the consolidation metrics a memory pipeline reports.
// state.TaskMetrics implements it, and `telemetry` reaches the same series
// through its own emitters.
type MemoryMetricSink interface {
	Counter(name string, inc int, tags map[string]string)
	Histogram(name string, value int, tags map[string]string)
	HistogramWithBounds(name string, value int, boundaries []float64, tags map[string]string)
	// RecordDuration records Rust's millisecond duration histogram, the series
	// codex-otel's Timer writes when it is dropped.
	RecordDuration(name string, duration time.Duration, tags map[string]string)
}

// memoryTimer mirrors codex-otel's Timer: a memory metric that records the
// elapsed duration when it is stopped. Stopping twice records once, the way a
// Rust Timer records on its single drop.
type memoryTimer struct {
	metrics MemoryMetricSink
	name    string
	tags    map[string]string
	started time.Time
	now     func() time.Time
}

// Stop records the elapsed duration (Rust's `drop(timer)`).
func (t *memoryTimer) Stop() {
	if t == nil || t.metrics == nil {
		return
	}
	now := t.now
	if now == nil {
		now = time.Now
	}
	t.metrics.RecordDuration(t.name, now().Sub(t.started), t.tags)
	t.metrics = nil
}

// startMemoryTimer starts Rust's `MemoryStartupContext::start_timer` for the
// pipeline's memory version. A pipeline without a sink has no timer.
func (p *StartupPipeline) startMemoryTimer(name string) *memoryTimer {
	if p == nil || p.Metrics == nil {
		return nil
	}
	return &memoryTimer{
		metrics: p.Metrics,
		name:    name,
		tags:    memoryMetricTags(p.Version, nil),
		started: time.Now(),
	}
}

// recordMemoryCounter mirrors `MemoryStartupContext::counter`.
func (p *StartupPipeline) recordMemoryCounter(name string, inc int, tags map[string]string) {
	if p == nil || p.Metrics == nil || inc <= 0 {
		return
	}
	p.Metrics.Counter(name, inc, memoryMetricTags(p.Version, tags))
}

// recordMemoryHistogram mirrors `MemoryStartupContext::histogram`.
func (p *StartupPipeline) recordMemoryHistogram(name string, value int, tags map[string]string) {
	if p == nil || p.Metrics == nil {
		return
	}
	p.Metrics.Histogram(name, value, memoryMetricTags(p.Version, tags))
}

// recordMemoryJobStatus reports one job outcome (Rust's `job::failed` and
// `job::succeed` counters, which share the `status` tag).
func (p *StartupPipeline) recordMemoryJobStatus(metric string, status string) {
	if p == nil || p.Metrics == nil || status == "" {
		return
	}
	p.recordMemoryCounter(metric, 1, map[string]string{MemoryStatusTag: status})
}

// MemoryVersionTagValue reports the tag value Rust's `memory_metric_tags` writes
// for a memory version: an unset or unparsable version is v1, the version the
// pipeline falls back to everywhere else.
func MemoryVersionTagValue(version config.MemoryVersion) string {
	if resolved, ok := config.ParseMemoryVersion(string(version)); ok {
		return string(resolved)
	}
	return string(config.MemoryVersionV1)
}

// memoryMetricTags mirrors Rust's `memory_metric_tags`: the caller's tags with
// the memory root version appended.
func memoryMetricTags(version config.MemoryVersion, tags map[string]string) map[string]string {
	out := make(map[string]string, len(tags)+1)
	for key, value := range tags {
		out[key] = value
	}
	out[MemoryVersionTag] = MemoryVersionTagValue(version)
	return out
}

// RecordMemoryStorageSize mirrors `MemoryStartupContext::record_storage_size`:
// measure the memory root and report its size on the byte histogram, tagged with
// the root version. A measurement failure is logged and reports no sample. It
// returns whether a sample was recorded.
func (p *StartupPipeline) RecordMemoryStorageSize(root string) bool {
	if p == nil || p.Metrics == nil {
		return false
	}
	size, err := MemoryStorageBytes(root)
	if err != nil {
		slog.Warn("failed measuring memory storage size", "root", root, "error", err)
		return false
	}
	p.Metrics.HistogramWithBounds(
		MemoryStorageBytesMetric,
		clampToInt(size),
		MemoryStorageBytesBoundaries(),
		memoryMetricTags(p.Version, nil),
	)
	return true
}

// clampToInt mirrors Rust's `i64::try_from(bytes).unwrap_or(i64::MAX)`: a size
// the platform's int cannot hold saturates instead of wrapping.
func clampToInt(value int64) int {
	if value > int64(math.MaxInt) {
		return math.MaxInt
	}
	return int(value)
}
