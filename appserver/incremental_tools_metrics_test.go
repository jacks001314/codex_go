package appserver

import (
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"codex_go/metrics"
	"codex_go/session"
)

// toolMetricCounterSample is one recorded counter increment of
// `codex.tools.incremental_updates`.
type toolMetricCounterSample struct {
	action string
	tool   string
	inc    int
}

// toolMetricHistogramSample is one recorded observation of
// `codex.tools.spec_description_bytes`.
type toolMetricHistogramSample struct {
	value      int
	boundaries []float64
	tool       string
}

// toolMetricRecorder captures the per-tool metrics the appserver emits. It
// implements the explicit-boundary histogram the way telemetry.MetricsClient
// does.
type toolMetricRecorder struct {
	counters   []toolMetricCounterSample
	histograms []toolMetricHistogramSample
	plain      []int
}

func (r *toolMetricRecorder) RecordDuration(string, time.Duration, map[string]string) {}

func (r *toolMetricRecorder) Counter(_ string, inc int, tags map[string]string) {
	r.counters = append(r.counters, toolMetricCounterSample{action: tags["action"], tool: tags["tool"], inc: inc})
}

func (r *toolMetricRecorder) Histogram(_ string, value int, _ map[string]string) {
	r.plain = append(r.plain, value)
}

func (r *toolMetricRecorder) HistogramWithBounds(_ string, value int, boundaries []float64, tags map[string]string) {
	r.histograms = append(r.histograms, toolMetricHistogramSample{value: value, boundaries: boundaries, tool: tags["tool"]})
}

func installToolMetricRecorder(t *testing.T) *toolMetricRecorder {
	t.Helper()
	recorder := &toolMetricRecorder{}
	previous := metrics.Global()
	metrics.InstallGlobal(recorder)
	t.Cleanup(func() { metrics.InstallGlobal(previous) })
	return recorder
}

// toolMetricDeclaration mirrors the Rust test fixture `declaration`: a named
// function with a description.
func toolMetricDeclaration(name string) map[string]any {
	return map[string]any{
		"type":        "function",
		"name":        name,
		"description": "Look up a value.",
		"parameters":  map[string]any{"type": "object", "properties": map[string]any{}},
	}
}

// toolMetricNamespace mirrors the Rust test fixture `namespace`.
func toolMetricNamespace(name string, tools []any) map[string]any {
	return map[string]any{"type": "namespace", "name": name, "description": "Tools.", "tools": tools}
}

// toolMetricHashes returns the catalog hashes the section persists (Rust
// `TopLevelToolsState::new`), so each recorded transition diffs the same values
// the incremental catalog uses.
func toolMetricHashes(t *testing.T, definitions []any) map[string]string {
	t.Helper()
	hashes, err := session.TopLevelToolsHashes(definitions)
	if err != nil {
		t.Fatalf("TopLevelToolsHashes() error = %v", err)
	}
	return hashes
}

// TestRecordChangedToolMetricsTagsPerToolLikeRust replays the nine catalog
// transitions of Rust #54133
// (`codex-rs/core/src/context/world_state/top_level_tools_tests.rs`,
// `snapshots_namespace_transitions`) and pins the per-tool counter table and the
// description-size histogram the upstream test asserts.
func TestRecordChangedToolMetricsTagsPerToolLikeRust(t *testing.T) {
	recorder := installToolMetricRecorder(t)

	original := []any{
		toolMetricNamespace("functions", []any{
			toolMetricDeclaration("unchanged"),
			toolMetricDeclaration("removed"),
			toolMetricDeclaration("changed"),
		}),
		toolMetricNamespace("other", []any{toolMetricDeclaration("lookup")}),
	}
	changedDeclaration := toolMetricDeclaration("changed")
	changedDeclaration["parameters"] = map[string]any{
		"type":       "object",
		"properties": map[string]any{"query": map[string]any{"type": "string"}},
	}
	updated := []any{
		toolMetricNamespace("functions", []any{
			toolMetricDeclaration("unchanged"),
			changedDeclaration,
			toolMetricDeclaration("added"),
		}),
		toolMetricNamespace("other", []any{toolMetricDeclaration("lookup")}),
	}
	onlyOther := []any{toolMetricNamespace("other", []any{toolMetricDeclaration("lookup")})}
	onlyFunctions := []any{toolMetricNamespace("functions", []any{toolMetricDeclaration("unchanged")})}
	empty := []any{}
	restored := []any{
		toolMetricNamespace("functions", []any{
			toolMetricDeclaration("unchanged"),
			toolMetricDeclaration("restored"),
		}),
	}

	hOriginal := toolMetricHashes(t, original)
	hUpdated := toolMetricHashes(t, updated)
	hOnlyOther := toolMetricHashes(t, onlyOther)
	hOnlyFunctions := toolMetricHashes(t, onlyFunctions)
	hEmpty := toolMetricHashes(t, empty)
	hRestored := toolMetricHashes(t, restored)

	// The transition list of the Rust test: initial/full snapshots contribute
	// nothing because there is no previous state, an unchanged catalog records
	// nothing, and a whole namespace removal counts each member.
	recordChangedToolMetrics(original, nil, false, hOriginal) // (Absent, Known(original))
	recordChangedToolMetrics(original, hOriginal, true, hOriginal)
	recordChangedToolMetrics(updated, hOriginal, true, hUpdated)
	recordChangedToolMetrics(onlyOther, hUpdated, true, hOnlyOther)
	recordChangedToolMetrics(original, hOnlyOther, true, hOriginal)
	recordChangedToolMetrics(onlyFunctions, hUpdated, true, hOnlyFunctions)
	recordChangedToolMetrics(empty, hUpdated, true, hEmpty)
	recordChangedToolMetrics(empty, hEmpty, true, hEmpty)
	recordChangedToolMetrics(restored, hEmpty, true, hRestored)

	counts := map[string]int{}
	for _, sample := range recorder.counters {
		if sample.inc != 1 {
			t.Fatalf("counter increment = %d, want 1 (%s %s)", sample.inc, sample.action, sample.tool)
		}
		counts[sample.action+" "+sample.tool]++
	}
	want := map[string]int{
		"added functions.added":            1,
		"added functions.changed":          1,
		"added functions.removed":          1,
		"added functions.restored":         1,
		"added functions.unchanged":        2,
		"removed functions.added":          3,
		"removed functions.changed":        3,
		"removed functions.removed":        1,
		"removed functions.unchanged":      2,
		"removed other.lookup":             2,
		"schema_changed functions.changed": 1,
	}
	if !reflect.DeepEqual(counts, want) {
		t.Fatalf("counts = %v, want %v", counts, want)
	}
	if len(recorder.plain) != 0 {
		t.Fatalf("plain histogram calls = %v, want none", recorder.plain)
	}
	if len(recorder.histograms) != 1 {
		t.Fatalf("description histograms = %v, want exactly one", recorder.histograms)
	}
	sample := recorder.histograms[0]
	if sample.tool != "functions.changed" {
		t.Fatalf("histogram tool = %q, want functions.changed", sample.tool)
	}
	if sample.value != len("Look up a value.") {
		t.Fatalf("histogram value = %d, want %d", sample.value, len("Look up a value."))
	}
	if len(sample.boundaries) != len(metrics.ToolSpecDescriptionBytesBoundaries) {
		t.Fatalf("histogram boundaries = %d, want %d", len(sample.boundaries), len(metrics.ToolSpecDescriptionBytesBoundaries))
	}
}

// TestRecordChangedToolMetricsSkipsInitialCatalogLikeRust pins the Rust rule
// that a section without previous state records nothing at all: the initial
// catalog of a window is sent in full and is not a delta.
func TestRecordChangedToolMetricsSkipsInitialCatalogLikeRust(t *testing.T) {
	recorder := installToolMetricRecorder(t)
	definitions := []any{
		toolMetricNamespace("functions", []any{toolMetricDeclaration("exec")}),
	}
	recordChangedToolMetrics(definitions, nil, false, toolMetricHashes(t, definitions))
	if len(recorder.counters) != 0 || len(recorder.histograms) != 0 {
		t.Fatalf("initial catalog recorded counters=%v histograms=%v, want none", recorder.counters, recorder.histograms)
	}
}

// TestRecordChangedToolMetricsSkipsNonReplacementHistogramsLikeRust pins the
// Rust guard around the description histogram: only a replacement of an
// existing declaration measures bytes, and a declaration without a description
// is skipped.
func TestRecordChangedToolMetricsSkipsNonReplacementHistogramsLikeRust(t *testing.T) {
	recorder := installToolMetricRecorder(t)

	recordChangedToolMetric("functions.new", map[string]any{"type": "function", "name": "new", "description": "Fresh tool."}, map[string]string{})
	recordChangedToolMetric("functions.nodesc", map[string]any{"type": "function", "name": "nodesc"}, map[string]string{"functions.nodesc": "prior"})

	want := []toolMetricCounterSample{
		{action: "added", tool: "functions.new", inc: 1},
		{action: "schema_changed", tool: "functions.nodesc", inc: 1},
	}
	if !reflect.DeepEqual(recorder.counters, want) {
		t.Fatalf("counters = %v, want %v", recorder.counters, want)
	}
	if len(recorder.histograms) != 0 {
		t.Fatalf("histograms = %v, want none", recorder.histograms)
	}
}

// toolMetricBucketIndex returns the explicit-bucket index a value lands in: the
// first boundary greater than or equal to it, or the overflow bucket.
func toolMetricBucketIndex(boundaries []float64, value int) int {
	return sort.SearchFloat64s(boundaries, float64(value))
}

// TestToolSpecDescriptionBytesBucketProbeLikeRust probes the explicit bucket
// boundaries of `codex.tools.spec_description_bytes` through the emission path
// of #54133: 0, 1, 511 and 512 description bytes land in the buckets the
// logarithmic family defines (a replacement at the boundary itself stays in that
// boundary's bucket), and the 1 MiB family maximum keeps an overflow bucket.
func TestToolSpecDescriptionBytesBucketProbeLikeRust(t *testing.T) {
	recorder := installToolMetricRecorder(t)

	probes := []struct {
		bytes       int
		bucket      int
		description string
	}{
		{bytes: 0, bucket: 0, description: ""},
		{bytes: 1, bucket: 1, description: "x"},
		{bytes: 511, bucket: 230, description: strings.Repeat("x", 511)},
		{bytes: 512, bucket: 231, description: strings.Repeat("x", 512)},
		{bytes: 1 << 20, bucket: 510, description: strings.Repeat("x", 1<<20)},
		{bytes: (1 << 20) + 1, bucket: 511, description: strings.Repeat("x", (1<<20)+1)},
	}
	previous := map[string]string{"functions.probe": "prior"}
	for _, probe := range probes {
		recordChangedToolMetric("functions.probe", map[string]any{
			"type":        "function",
			"name":        "probe",
			"description": probe.description,
		}, previous)
	}

	if len(recorder.histograms) != len(probes) {
		t.Fatalf("histograms = %d, want %d", len(recorder.histograms), len(probes))
	}
	if len(recorder.counters) != len(probes) {
		t.Fatalf("counters = %d, want %d", len(recorder.counters), len(probes))
	}
	for _, counter := range recorder.counters {
		if counter.action != "schema_changed" || counter.tool != "functions.probe" {
			t.Fatalf("counter = %v, want a schema_changed replacement of functions.probe", counter)
		}
	}
	for index, probe := range probes {
		sample := recorder.histograms[index]
		if sample.tool != "functions.probe" {
			t.Fatalf("histogram tool = %q, want functions.probe", sample.tool)
		}
		if sample.value != probe.bytes {
			t.Fatalf("histogram value = %d, want %d", sample.value, probe.bytes)
		}
		if len(sample.boundaries) != 511 {
			t.Fatalf("histogram boundaries = %d, want 511", len(sample.boundaries))
		}
		if sample.boundaries[0] != 0 || sample.boundaries[1] != 1 || sample.boundaries[510] != 1<<20 {
			t.Fatalf("boundaries = [%v %v ... %v], want [0 1 ... 1048576]",
				sample.boundaries[0], sample.boundaries[1], sample.boundaries[510])
		}
		if got := toolMetricBucketIndex(sample.boundaries, sample.value); got != probe.bucket {
			t.Fatalf("%d description bytes landed in bucket %d, want %d", probe.bytes, got, probe.bucket)
		}
	}
}
