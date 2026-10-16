package metrics

import (
	"math"
	"math/big"
)

// Per-tool incremental catalog metrics (Rust #54133,
// codex-rs/otel/src/metrics/names.rs `TOOL_INCREMENTAL_UPDATES_METRIC` and
// `TOOL_SPEC_DESCRIPTION_BYTES_METRIC`). They live next to the recorder they are
// emitted through: telemetry.MetricsClient already exposes the
// explicit-boundary method below, so the emission path needs no interface change
// and the telemetry package keeps its own name table untouched.

// ToolIncrementalUpdatesMetric counts rendered incremental tool deltas, tagged
// by `action` (`added`, `removed`, `schema_changed`) and by the qualified
// `tool` name. Initial catalogs of a window are excluded, and a whole namespace
// removal still counts every member (Rust `TOOL_INCREMENTAL_UPDATES_METRIC`).
const ToolIncrementalUpdatesMetric = "codex.tools.incremental_updates"

// ToolSpecDescriptionBytesMetric observes the UTF-8 description bytes of a tool
// definition that replaced an existing one; a replacement without a description
// is skipped (Rust `TOOL_SPEC_DESCRIPTION_BYTES_METRIC`).
const ToolSpecDescriptionBytesMetric = "codex.tools.spec_description_bytes"

// ToolSpecDescriptionBytesBoundaries are the logarithmic boundaries up to
// 1 MiB (Rust `TOOL_SPEC_DESCRIPTION_BYTES_METRIC_BUCKETS`,
// `context_log_buckets(20.0)`): 511 boundaries that keep zero in its own bucket
// and then grow to 2^20.
var ToolSpecDescriptionBytesBoundaries = contextLogBuckets(20)

// HistogramWithBoundsRecorder is implemented by recorders that accept explicit
// histogram boundaries; telemetry.MetricsClient does, so the bounded path works
// without widening the Recorder interface.
type HistogramWithBoundsRecorder interface {
	HistogramWithBounds(name string, value int, boundaries []float64, tags map[string]string)
}

// HistogramWithBounds records one histogram observation with explicit
// boundaries on the process-global recorder (Rust
// `ExtensionMetrics::histogram_with_boundaries`). A recorder that cannot take
// explicit boundaries still receives the observation through its own bucket
// layout, so no sample is dropped. It is nil-safe.
func HistogramWithBounds(name string, value int, boundaries []float64, tags map[string]string) {
	recorder := Global()
	if recorder == nil {
		return
	}
	if bounded, ok := recorder.(HistogramWithBoundsRecorder); ok {
		bounded.HistogramWithBounds(name, value, boundaries, cloneTags(tags))
		return
	}
	recorder.Histogram(name, value, cloneTags(tags))
}

// contextLogBuckets mirrors Rust's `context_log_buckets`: 511 boundaries that
// keep zero in its own bucket and then grow logarithmically to `2^maxExponent`.
//
// This is the algorithm telemetry.contextLogBuckets already carries for the
// skill families, evaluated the same way: Rust lowers `2.0_f64.powf(...)` to the
// platform libm's correctly rounded `pow`, while Go's math.Pow (and math.Exp2)
// carry up to one ULP of error and would export bounds that differ from Rust in
// the last bit for 72 of the 511 `2^17` boundaries. The powers are therefore
// evaluated at logBucketPrecision bits and rounded once to float64.
// TestToolSpecDescriptionBytesBoundariesMatchRust pins this family and
// reproduces the shipped `2^17` family's Rust digest, so both stay aligned.
func contextLogBuckets(maxExponent float64) []float64 {
	boundaries := make([]float64, 511)
	roots := exactPowerOfTwoRoots()
	value := new(big.Float).SetPrec(logBucketPrecision)
	for index := range boundaries {
		if index == 0 {
			boundaries[index] = 0
			continue
		}
		exponent := maxExponent * float64(index-1) / 509
		boundaries[index] = roundedPowerOfTwo(exponent, roots, value)
	}
	return boundaries
}

// logBucketPrecision keeps the evaluation error far below the 2^-53 that a
// float64 rounding could notice, so every boundary rounds to libm's value.
const logBucketPrecision = 256

// logBucketRootCount covers the dyadic expansion of any exponent below 2^21:
// a float64 fraction of such a value has no set bit deeper than 2^-57.
const logBucketRootCount = 64

// exactPowerOfTwoRoots returns `2^(2^-1) .. 2^(2^-64)`, the factors the binary
// expansion of a dyadic exponent multiplies into `2^exponent`.
func exactPowerOfTwoRoots() []*big.Float {
	roots := make([]*big.Float, logBucketRootCount)
	root := new(big.Float).SetPrec(logBucketPrecision).SetInt64(2)
	for index := range roots {
		root = new(big.Float).SetPrec(logBucketPrecision).Sqrt(root)
		roots[index] = root
	}
	return roots
}

// roundedPowerOfTwo evaluates `2^exponent` at logBucketPrecision bits by
// multiplying the square roots its binary expansion selects, scaling by the
// whole part (an exact power of two) and rounding once to the nearest float64.
func roundedPowerOfTwo(exponent float64, roots []*big.Float, scratch *big.Float) float64 {
	whole := math.Floor(exponent)
	remaining := exponent - whole
	value := scratch.SetInt64(1)
	for step, root := range roots {
		if remaining == 0 {
			break
		}
		bit := math.Ldexp(1, -(step + 1))
		if remaining >= bit {
			remaining -= bit
			value.Mul(value, root)
		}
	}
	if remaining != 0 {
		panic("logarithmic histogram boundary exponent exceeds the square-root chain")
	}
	value.SetMantExp(value, int(whole))
	result, _ := value.Float64()
	return result
}
