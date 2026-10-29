//go:build stability

package scenarios

import (
	"context"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/model"
	"codex_go/stability/harness/metrics"
	"codex_go/stability/harness/mockmodel"
	"codex_go/stability/harness/report"
)

// TestMemoryLongSession runs a long single-session turn loop with a multi-KiB
// answer per turn and asserts the steady-state heap does not trend upward.
// A leak here is invisible to a short test but ruins a multi-hour session.
func TestMemoryLongSession(t *testing.T) {
	run := report.New("memory-long-session")
	turns := envIntDefault("CODEX_STABILITY_MEMORY_TURNS", 1500)
	payload := strings.Repeat("memory evidence ", 256)
	server := mockmodel.New()
	server.Fallback(mockmodel.Text(payload))
	defer server.Close()
	runner := newRunner(server.URL())

	warmup := envIntDefault("CODEX_STABILITY_MEMORY_WARMUP", 50)
	for turn := 0; turn < warmup; turn++ {
		runMemoryTurn(t, runner, turn)
	}
	time.Sleep(500 * time.Millisecond)

	var mu sync.Mutex
	peak := metrics.Take().HeapAllocBytes
	samples := []uint64{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		sampler := metrics.Sampler{Interval: 100 * time.Millisecond}
		for sample := range sampler.Run(ctx) {
			mu.Lock()
			if sample.HeapAllocBytes > peak {
				peak = sample.HeapAllocBytes
			}
			samples = append(samples, sample.HeapAllocBytes)
			mu.Unlock()
		}
	}()

	start := time.Now()
	for turn := 0; turn < turns; turn++ {
		runMemoryTurn(t, runner, turn)
		if turn%250 == 249 {
			// Compaction noise would otherwise dominate the trend: the point of
			// the check is live-object growth, not postponed GC.
			runtime.GC()
		}
	}
	elapsed := time.Since(start)
	cancel()
	<-done
	runtime.GC()
	final := metrics.Take()

	mu.Lock()
	peakHeap := peak
	collected := append([]uint64(nil), samples...)
	mu.Unlock()

	budgets, err := report.LoadBudgets(budgetsPath())
	if err != nil {
		t.Fatalf("load budgets: %v", err)
	}
	if len(collected) < 8 {
		t.Fatalf("only %d heap samples collected", len(collected))
	}
	firstQuarter := medianUint64(collected[:len(collected)/4])
	lastQuarter := medianUint64(collected[len(collected)*3/4:])
	// Allow 50% growth plus absolute slack for allocator noise before calling
	// it a leak; a real per-turn leak over this many turns is far larger.
	trendLimit := float64(firstQuarter)*1.5 + float64(64<<20)

	run.Env["turns"] = strconv.Itoa(turns)
	run.Env["payload_bytes"] = strconv.Itoa(len(payload))
	run.Env["elapsed"] = elapsed.String()
	run.Check("memory.peak_heap", peakHeap <= budgets.Large.PeakHeapBytes,
		"peak=%d budget=%d", peakHeap, budgets.Large.PeakHeapBytes)
	run.Check("memory.heap_trend", float64(lastQuarter) <= trendLimit,
		"first_quarter=%d last_quarter=%d limit=%.0f", firstQuarter, lastQuarter, trendLimit)
	run.Env["goroutines_final"] = strconv.Itoa(final.Goroutines)
	run.Metric("memory.peak_heap_bytes", float64(peakHeap))
	run.Metric("memory.heap_first_quarter_bytes", float64(firstQuarter))
	run.Metric("memory.heap_last_quarter_bytes", float64(lastQuarter))
	finishReport(t, run)
}

func runMemoryTurn(t *testing.T, runner *model.ResponsesAgentRunner, turn int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	response, err := runner.Run(ctx, &model.AgentRequest{Prompt: "remember this", Model: "gpt-mock"})
	if err != nil {
		t.Fatalf("turn %d failed: %v", turn, err)
	}
	if response == nil || response.Message == "" {
		t.Fatalf("turn %d returned an empty answer", turn)
	}
}

func medianUint64(values []uint64) uint64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]uint64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[len(sorted)/2]
}
