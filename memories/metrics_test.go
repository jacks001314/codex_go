package memories

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"codex_go/config"
	"codex_go/state"
)

// memoryMetricRecords selects one metric's records, in the order they were
// reported.
func memoryMetricRecords(metrics *state.TaskMetrics, name string) []*state.TaskMetric {
	var out []*state.TaskMetric
	for _, record := range metrics.Records() {
		if record.Name == name {
			out = append(out, record)
		}
	}
	return out
}

// memoryStatusCounts reports the `status` -> increment pairs one job counter
// metric recorded (Rust's job counters share the status tag).
func memoryStatusCounts(metrics *state.TaskMetrics, name string) map[string]int {
	counts := map[string]int{}
	for _, record := range memoryMetricRecords(metrics, name) {
		counts[record.Tags[MemoryStatusTag]] += record.Inc
	}
	return counts
}

// assertMemoryStatusCounts fails unless the metric's status counter matches.
func assertMemoryStatusCounts(t *testing.T, metrics *state.TaskMetrics, name string, want map[string]int) {
	t.Helper()
	got := memoryStatusCounts(metrics, name)
	if len(got) != len(want) {
		t.Fatalf("%s status counts = %#v, want %#v", name, got, want)
	}
	for status, count := range want {
		if got[status] != count {
			t.Fatalf("%s status counts = %#v, want %#v", name, got, want)
		}
	}
}

// assertMemoryTimerRecorded fails unless the run reported one duration sample
// tagged with the pipeline's memory version (Rust's Timer).
func assertMemoryTimerRecorded(t *testing.T, metrics *state.TaskMetrics, name string, version config.MemoryVersion) {
	t.Helper()
	records := memoryMetricRecords(metrics, name)
	if len(records) != 1 {
		t.Fatalf("%s records = %#v, want one duration", name, records)
	}
	if records[0].Kind != "duration" || records[0].Tags[MemoryVersionTag] != MemoryVersionTagValue(version) {
		t.Fatalf("%s record = %#v", name, records[0])
	}
}

// Mirrors Rust's memory_metric_tags: the reported version follows the pipeline's
// selected root, and an unset version is v1.
func TestMemoryVersionTagValueFollowsTheSelectedRoot(t *testing.T) {
	cases := map[config.MemoryVersion]string{
		config.MemoryVersionV1: "v1",
		config.MemoryVersionV2: "v2",
		"":                     "v1",
	}
	for version, want := range cases {
		if got := MemoryVersionTagValue(version); got != want {
			t.Fatalf("MemoryVersionTagValue(%q) = %q, want %q", version, got, want)
		}
	}
}

// RecordMemoryStorageSize reports the root's size on Rust's byte histogram with
// its bucket boundaries and the memory-version tag, and stays silent without a
// sink or when the root cannot be measured.
func TestRecordMemoryStorageSizeFollowsRust(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "memory_summary.md"), []byte("v1\nsummary\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	metrics := state.NewTaskMetrics()
	pipeline := &StartupPipeline{Metrics: metrics, Version: config.MemoryVersionV2}
	if !pipeline.RecordMemoryStorageSize(root) {
		t.Fatal("a measurable root must record a sample")
	}
	records := metrics.Records()
	if len(records) != 1 {
		t.Fatalf("records = %#v", records)
	}
	record := records[0]
	if record.Name != MemoryStorageBytesMetric || record.Kind != "histogram" || record.Value != len("v1\nsummary\n") {
		t.Fatalf("record = %#v", record)
	}
	if record.Tags[MemoryVersionTag] != "v2" {
		t.Fatalf("record tags = %#v", record.Tags)
	}
	// Rust stops the phase-two timer before measuring, so the sample is taken
	// after the artifacts of the run are in place; the boundaries are Rust's
	// log-spaced byte buckets.
	boundaries := MemoryStorageBytesBoundaries()
	if len(record.Boundaries) != len(boundaries) {
		t.Fatalf("boundaries = %#v", record.Boundaries)
	}
	for index := range boundaries {
		if record.Boundaries[index] != boundaries[index] {
			t.Fatalf("boundary %d = %v, want %v", index, record.Boundaries[index], boundaries[index])
		}
	}

	if (&StartupPipeline{Version: config.MemoryVersionV1}).RecordMemoryStorageSize(root) {
		t.Fatal("a pipeline without a sink must not record")
	}
	silent := state.NewTaskMetrics()
	unmeasurable := &StartupPipeline{Metrics: silent, Version: config.MemoryVersionV1}
	if unmeasurable.RecordMemoryStorageSize(filepath.Join(root, "missing")) {
		t.Fatal("an unmeasurable root must not record")
	}
	if records := silent.Records(); len(records) != 0 {
		t.Fatalf("unmeasurable records = %#v", records)
	}
}

// Mirrors the phase-one counters in Rust's phase1::emit_metrics: a run with no
// eligible rollout reports the skip, and never reports an output counter. Rust
// starts the phase-one timer before claiming, so the skip is timed too.
func TestStartupPipelineReportsPhaseOneSkipLikeRust(t *testing.T) {
	home := t.TempDir()
	runtime := newMemoryPipelineRuntime(t, home)
	metrics := state.NewTaskMetrics()
	pipeline := &StartupPipeline{
		State: runtime, CodexHome: home, CurrentThreadID: "current-thread",
		Config: config.MemoriesConfig{MaxRolloutsPerStartup: 2}, Metrics: metrics,
	}
	pipeline.runStageOne(context.Background(), &StartupReport{})
	assertMemoryStatusCounts(t, metrics, MemoryPhaseOneJobsMetric, map[string]int{"skipped_no_candidates": 1})
	assertMemoryTimerRecorded(t, metrics, MemoryPhaseOneE2EMetric, config.MemoryVersionV1)
	if records := memoryMetricRecords(metrics, MemoryPhaseOneOutputMetric); len(records) != 0 {
		t.Fatalf("output records = %#v", records)
	}
}

// A phase-two run that cannot claim the global job still reports the claim
// outcome and the duration (Rust's job::claim counters the status).
func TestStartupPipelineReportsSkippedPhaseTwoClaimLikeRust(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	runtime := newMemoryPipelineRuntime(t, home)
	metrics := state.NewTaskMetrics()
	pipeline := &StartupPipeline{
		State: runtime, CodexHome: home, CurrentThreadID: "current-thread",
		Config: config.MemoriesConfig{MaxUnusedDays: 30}, Metrics: metrics,
	}
	if claim, err := runtime.TryClaimGlobalPhase2Job(ctx, "other-thread", PhaseTwoJobLeaseSeconds); err != nil || claim.Outcome != state.Phase2JobClaimed {
		t.Fatalf("first claim = %+v, %v", claim, err)
	}
	if status := pipeline.runPhaseTwo(ctx); status != string(state.Phase2JobSkippedRunning) {
		t.Fatalf("phase two status = %q", status)
	}
	assertMemoryStatusCounts(t, metrics, MemoryPhaseTwoJobsMetric, map[string]int{"skipped_running": 1})
	assertMemoryTimerRecorded(t, metrics, MemoryPhaseTwoE2EMetric, config.MemoryVersionV1)
	for _, name := range []string{MemoryPhaseTwoInputMetric, MemoryStorageBytesMetric} {
		if records := memoryMetricRecords(metrics, name); len(records) != 0 {
			t.Fatalf("%s records = %#v after a skipped claim", name, records)
		}
	}
}

// The rate-limit guard stops the pipeline before phase one, the one startup
// outcome Rust reports (status = skipped_rate_limit).
func TestStartupPipelineReportsSkippedRateLimitLikeRust(t *testing.T) {
	home := t.TempDir()
	runtime := newMemoryPipelineRuntime(t, home)
	metrics := state.NewTaskMetrics()
	pipeline := &StartupPipeline{
		State: runtime, CodexHome: home, CurrentThreadID: "current-thread",
		Config: config.MemoriesConfig{MinRateLimitRemainingPercent: 25},
		Guard:  deniedStartupGuard{}, Metrics: metrics,
	}
	report, err := pipeline.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.PhaseTwoStatus != "skipped_rate_limit" {
		t.Fatalf("phase two status = %q", report.PhaseTwoStatus)
	}
	assertMemoryStatusCounts(t, metrics, MemoryStartupMetric, map[string]int{"skipped_rate_limit": 1})
	for _, name := range []string{MemoryPhaseOneJobsMetric, MemoryPhaseTwoJobsMetric, MemoryStorageBytesMetric} {
		if records := memoryMetricRecords(metrics, name); len(records) != 0 {
			t.Fatalf("%s records = %#v after a rate-limit skip", name, records)
		}
	}
}

type deniedStartupGuard struct{}

func (deniedStartupGuard) AllowMemoryStartup(context.Context, int64) bool { return false }
