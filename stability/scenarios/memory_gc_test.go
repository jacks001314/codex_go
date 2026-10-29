//go:build stability

package scenarios

import (
	"runtime"
	"strconv"
	"strings"
	"testing"

	"codex_go/stability/harness/metrics"
	"codex_go/stability/harness/mockmodel"
	"codex_go/stability/harness/report"
)

// TestMemoryGCSteady forces a collection at fixed intervals and compares the
// post-GC retained heap of the first third with the last third. Sampling
// without GC measures allocator noise; post-GC values measure what the session
// actually keeps, which is what turns a long chat into an OOM.
func TestMemoryGCSteady(t *testing.T) {
	turns := envIntDefault("CODEX_STABILITY_GC_TURNS", 1200)
	every := envIntDefault("CODEX_STABILITY_GC_SAMPLE_EVERY", 100)
	server := mockmodel.New()
	server.Fallback(mockmodel.Text(strings.Repeat("steady state answer ", 64)))
	defer server.Close()
	runner := newRunner(server.URL())

	for turn := 0; turn < 50; turn++ {
		runMemoryTurn(t, runner, turn)
	}
	runtime.GC()

	retained := make([]uint64, 0, turns/every+1)
	retained = append(retained, metrics.Take().HeapAllocBytes)
	for turn := 0; turn < turns; turn++ {
		runMemoryTurn(t, runner, turn)
		if (turn+1)%every == 0 {
			runtime.GC()
			retained = append(retained, metrics.Take().HeapAllocBytes)
		}
	}

	if len(retained) < 6 {
		t.Fatalf("only %d post-GC samples; lower CODEX_STABILITY_GC_SAMPLE_EVERY or raise turns", len(retained))
	}
	t.Logf("retained post-GC series: %v requests=%d captured_bytes=%d", retained, server.RequestCount(), server.RequestBytes())
	third := len(retained) / 3
	first := medianUint64(retained[:third])
	last := medianUint64(retained[len(retained)-third:])
	limit := float64(first)*1.25 + float64(8<<20)

	budgets, err := report.LoadBudgets(budgetsPath())
	if err != nil {
		t.Fatalf("load budgets: %v", err)
	}
	run := report.New("memory-gc-steady")
	run.Env["turns"] = strconv.Itoa(turns)
	run.Env["sample_every"] = strconv.Itoa(every)
	run.Env["samples"] = strconv.Itoa(len(retained))
	run.Env["requests"] = strconv.Itoa(server.RequestCount())
	run.Env["captured_request_bytes"] = strconv.Itoa(server.RequestBytes())
	run.Check("memory.gc_steady", float64(last) <= limit,
		"first=%d last=%d limit=%.0f samples=%d", first, last, limit, len(retained))
	run.Check("memory.gc_peak", last <= budgets.Large.PeakHeapBytes,
		"last=%d budget=%d", last, budgets.Large.PeakHeapBytes)
	run.Metric("memory.gc_retained_first_bytes", float64(first))
	run.Metric("memory.gc_retained_last_bytes", float64(last))
	finishReport(t, run)
}
