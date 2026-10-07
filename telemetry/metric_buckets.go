package telemetry

import "math"

// Explicit histogram boundaries for the context-budget metrics (Rust #48819,
// codex-rs/otel/src/metrics/names.rs).
//
// Rust also defines `THREAD_TOOLS_METRIC_BUCKETS` (logarithmic through 32,768)
// for `codex.thread.tools.fragment_bytes` and `codex.thread.tools.namespaces_total`;
// the Go port does not emit that metric family yet, so only the skill families
// are carried here.

// ThreadSkillsCountMetricBoundaries are integer boundaries from 0 through 512
// for the enabled and kept skill counts. Rust
// `THREAD_SKILLS_COUNT_METRIC_BUCKETS` is `[f64; 513]` built with
// `std::array::from_fn(|index| index as f64)`.
var ThreadSkillsCountMetricBoundaries = integerCountBoundaries(512)

// ThreadSkillsTruncatedBoundaries separates "nothing truncated" from
// "truncated" (Rust `THREAD_SKILLS_TRUNCATED_BUCKETS`).
var ThreadSkillsTruncatedBoundaries = []float64{0, 1}

// ThreadSkillsDescriptionTruncatedCharsBoundaries are logarithmic boundaries up
// to 131,072 removed description characters (Rust
// `THREAD_SKILLS_DESCRIPTION_TRUNCATED_CHARS_BUCKETS`, `context_log_buckets(17.0)`).
var ThreadSkillsDescriptionTruncatedCharsBoundaries = contextLogBuckets(17)

// integerCountBoundaries builds the integer boundary family Rust expresses with
// `std::array::from_fn`, so index 0 is the zero bucket.
func integerCountBoundaries(maximum int) []float64 {
	boundaries := make([]float64, maximum+1)
	for index := range boundaries {
		boundaries[index] = float64(index)
	}
	return boundaries
}

// contextLogBuckets mirrors Rust's `context_log_buckets`: 511 boundaries that
// keep zero in its own bucket and then grow logarithmically to `2^maxExponent`.
func contextLogBuckets(maxExponent float64) []float64 {
	boundaries := make([]float64, 511)
	for index := range boundaries {
		if index == 0 {
			boundaries[index] = 0
			continue
		}
		boundaries[index] = math.Pow(2, maxExponent*float64(index-1)/509)
	}
	return boundaries
}
