package telemetry

import (
	"math"
	"math/big"
)

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
//
// Rust evaluates `2.0_f64.powf(...)`, which lowers to the platform libm's
// correctly rounded `pow`. Go's math.Pow (and math.Exp2) carry up to one ULP of
// error, which would export explicit bounds that differ from Rust in the last
// bit for 72 of the 511 `2^17` boundaries, so the powers are evaluated here at
// logBucketPrecision bits and rounded once to float64.
// TestContextLogBoundariesMatchRust pins the bytes against Rust's values.
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

// logBucketRootCount covers the dyadic expansion of any exponent below 2^17:
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
