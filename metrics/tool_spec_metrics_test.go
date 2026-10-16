package metrics

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"testing"
	"time"
)

// TestToolSpecDescriptionBytesBoundariesMatchRust pins the explicit boundaries
// of `codex.tools.spec_description_bytes` to the bytes Rust produces
// (codex-rs/otel/src/metrics/names.rs #54133:
// `TOOL_SPEC_DESCRIPTION_BYTES_METRIC_BUCKETS = context_log_buckets(20.0)`).
//
// Rust evaluates `2.0_f64.powf(...)`, which lowers to the platform libm's
// correctly rounded `pow`; the digest below is therefore taken from the shared
// `contextLogBuckets` evaluation rather than from this node's toolchain. The
// second digest is the `2^17` family already shipped by the telemetry package
// and pinned there against Rust, so reproducing it here proves this copy of the
// algorithm is the one that is known to match Rust.
//
// A rustc build of the verbatim `context_log_buckets` on this Windows node
// produces a different digest for both families (b17 = 8a35317b..., b20 =
// 7922bcbb...) because MSVC's `pow` is not correctly rounded: 72 of the 511
// `2^17` boundaries differ, the exact count the telemetry package documents. A
// drift (for example back to math.Pow) changes the digests below.
func TestToolSpecDescriptionBytesBoundariesMatchRust(t *testing.T) {
	const (
		rustToolDescriptionDigest = "4984e25980d77888eeb41e1f51ec7198338e307059c32b948f14a1d1f9de1648"
		shippedThreadSkillsDigest = "8cacf78cbe23be0158fb5fa8f4be5d8768684969b45c3f48b8841d8e42c80fea"
		expectedBoundaryCount     = 511
		expectedLastBoundary      = 1 << 20
	)

	boundaries := ToolSpecDescriptionBytesBoundaries
	if len(boundaries) != expectedBoundaryCount {
		t.Fatalf("boundaries = %d, want %d", len(boundaries), expectedBoundaryCount)
	}
	if boundaries[0] != 0 || boundaries[1] != 1 {
		t.Fatalf("boundaries start = %v, want [0 1]", boundaries[:2])
	}
	if boundaries[len(boundaries)-1] != float64(expectedLastBoundary) {
		t.Fatalf("last boundary = %v, want %d (1 MiB)", boundaries[len(boundaries)-1], expectedLastBoundary)
	}
	for index := 1; index < len(boundaries); index++ {
		if boundaries[index-1] >= boundaries[index] {
			t.Fatalf("boundaries not strictly increasing at %d: %v", index, boundaries[index-1:index+1])
		}
	}
	if got := boundaryDigest(boundaries); got != rustToolDescriptionDigest {
		t.Fatalf("tool description boundaries drifted from Rust: sha256 = %s, want %s", got, rustToolDescriptionDigest)
	}
	if got := boundaryDigest(contextLogBuckets(17)); got != shippedThreadSkillsDigest {
		t.Fatalf("shared bucket algorithm drifted from the shipped 2^17 family: sha256 = %s, want %s", got, shippedThreadSkillsDigest)
	}
}

// boundaryDigest hashes the little-endian float64 bits of a boundary family the
// way the telemetry package's Rust pin does.
func boundaryDigest(boundaries []float64) string {
	digest := sha256.New()
	for _, boundary := range boundaries {
		var encoded [8]byte
		binary.LittleEndian.PutUint64(encoded[:], math.Float64bits(boundary))
		digest.Write(encoded[:])
	}
	return hex.EncodeToString(digest.Sum(nil))
}

// explicitRecorder captures the boundaries HistogramWithBounds is called with.
type explicitRecorder struct {
	name       string
	value      int
	boundaries []float64
	tags       map[string]string
	plain      []int
}

func (r *explicitRecorder) RecordDuration(string, time.Duration, map[string]string) {}

func (r *explicitRecorder) Counter(string, int, map[string]string) {}

func (r *explicitRecorder) Histogram(_ string, value int, _ map[string]string) {
	r.plain = append(r.plain, value)
}

func (r *explicitRecorder) HistogramWithBounds(name string, value int, boundaries []float64, tags map[string]string) {
	r.name, r.value, r.boundaries, r.tags = name, value, boundaries, tags
}

// TestHistogramWithBoundsUsesExplicitBoundariesLikeRust covers the emission
// helper of #54133: a recorder that accepts boundaries receives them, and the
// tags and observation are passed through unchanged.
func TestHistogramWithBoundsUsesExplicitBoundariesLikeRust(t *testing.T) {
	recorder := &explicitRecorder{}
	previous := Global()
	InstallGlobal(recorder)
	t.Cleanup(func() { InstallGlobal(previous) })

	HistogramWithBounds(ToolSpecDescriptionBytesMetric, 17, ToolSpecDescriptionBytesBoundaries, map[string]string{"tool": "functions.exec"})

	if recorder.name != ToolSpecDescriptionBytesMetric {
		t.Fatalf("name = %q, want %q", recorder.name, ToolSpecDescriptionBytesMetric)
	}
	if recorder.value != 17 {
		t.Fatalf("value = %d, want 17", recorder.value)
	}
	if len(recorder.boundaries) != len(ToolSpecDescriptionBytesBoundaries) {
		t.Fatalf("boundaries = %d, want %d", len(recorder.boundaries), len(ToolSpecDescriptionBytesBoundaries))
	}
	if recorder.tags["tool"] != "functions.exec" {
		t.Fatalf("tags = %v, want tool=functions.exec", recorder.tags)
	}
	if len(recorder.plain) != 0 {
		t.Fatalf("plain histogram calls = %v, want none", recorder.plain)
	}
}

// plainRecorder only implements the Recorder interface, without explicit
// boundaries.
type plainRecorder struct {
	values []int
}

func (r *plainRecorder) RecordDuration(string, time.Duration, map[string]string) {}

func (r *plainRecorder) Counter(string, int, map[string]string) {}

func (r *plainRecorder) Histogram(_ string, value int, _ map[string]string) {
	r.values = append(r.values, value)
}

// TestHistogramWithBoundsFallsBackToRecorderWithoutBoundariesLikeRust keeps the
// observation from being dropped by a recorder that cannot take explicit
// boundaries.
func TestHistogramWithBoundsFallsBackToRecorderWithoutBoundariesLikeRust(t *testing.T) {
	recorder := &plainRecorder{}
	previous := Global()
	InstallGlobal(recorder)
	t.Cleanup(func() { InstallGlobal(previous) })

	HistogramWithBounds(ToolSpecDescriptionBytesMetric, 511, ToolSpecDescriptionBytesBoundaries, nil)

	if len(recorder.values) != 1 || recorder.values[0] != 511 {
		t.Fatalf("histogram values = %v, want [511]", recorder.values)
	}
}
