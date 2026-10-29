//go:build stability

package memories

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"codex_go/stability/harness/disk"
	"codex_go/stability/harness/metrics"
	"codex_go/stability/harness/report"
	"codex_go/stability/harness/rolloutgen"
)

// TestLargeRolloutRead proves memory extraction over a large rollout is bounded
// by the 8 MiB retention budget instead of the file size. It fails if the
// reader ever starts materialising the whole file.
func TestLargeRolloutRead(t *testing.T) {
	largeBytes := envInt64("CODEX_STABILITY_LARGE_BYTES", 1<<30)
	root := os.Getenv("CODEX_STABILITY_LARGE_DIR")
	removeRoot := false
	if root == "" {
		root = t.TempDir()
	} else {
		removeRoot = true
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("create fixture root: %v", err)
	}
	if removeRoot {
		t.Cleanup(func() { _ = os.RemoveAll(root) })
	}
	if free, err := disk.FreeBytes(root); err == nil {
		need := uint64(largeBytes)*2 + (1 << 30)
		if free < need {
			t.Skipf("need %d bytes free under %s, have %d", need, root, free)
		}
	}

	smallBytes := int64(16 << 20)
	if largeBytes <= 2*smallBytes {
		t.Skipf("CODEX_STABILITY_LARGE_BYTES=%d is too small to compare against %d", largeBytes, smallBytes)
	}
	smallPath := filepath.Join(root, "small.jsonl")
	largePath := filepath.Join(root, "large.jsonl")
	if _, err := rolloutgen.Generate(rolloutgen.Spec{Path: smallPath, TotalBytes: smallBytes, MaxLineSize: 32 << 10, Seed: 1}); err != nil {
		t.Fatalf("generate small fixture: %v", err)
	}
	if _, err := rolloutgen.Generate(rolloutgen.Spec{Path: largePath, TotalBytes: largeBytes, MaxLineSize: 32 << 10, Seed: 1}); err != nil {
		t.Fatalf("generate large fixture: %v", err)
	}

	small := measureRolloutRead(t, smallPath)
	large := measureRolloutRead(t, largePath)
	t.Logf("small file=%d bytes peak=%d total_alloc=%d elapsed=%s", small.bytes, small.peakHeap, small.totalAlloc, small.elapsed)
	t.Logf("large file=%d bytes peak=%d total_alloc=%d elapsed=%s retained=%d items=%d", large.bytes, large.peakHeap, large.totalAlloc, large.elapsed, large.retained, large.items)

	budgets, err := report.LoadBudgets(budgetsPathFor(t))
	if err != nil {
		t.Fatalf("load budgets: %v", err)
	}
	run := report.New("large-rollout")
	run.Env["large_bytes"] = strconv.FormatInt(largeBytes, 10)
	run.Env["small_bytes"] = strconv.FormatInt(smallBytes, 10)
	run.Env["go_max_procs"] = strconv.Itoa(runtime.GOMAXPROCS(0))

	run.Check("large.read_peak_heap", large.peakHeap <= budgets.Large.RolloutReadPeakBytes,
		"peak=%d budget=%d", large.peakHeap, budgets.Large.RolloutReadPeakBytes)
	run.Check("large.retained_bytes", large.retained <= memoryRolloutMaxBytes,
		"retained=%d budget=%d", large.retained, memoryRolloutMaxBytes)
	run.Check("large.items_retained", large.items > 0, "items=%d", large.items)
	// The peak must track the retention window, not the file size. Allow a
	// generous absolute slack for allocator noise, but nothing proportional to
	// the 1 GiB fixture.
	limit := small.peakHeap*2 + (16 << 20)
	run.Check("large.heap_decoupled_from_file_size", large.peakHeap <= limit,
		"large=%d small=%d limit=%d", large.peakHeap, small.peakHeap, limit)
	run.Metric("large.read_peak_heap_bytes", float64(large.peakHeap))
	run.Metric("large.retained_bytes", float64(large.retained))
	run.Metric("large.file_bytes", float64(large.bytes))
	run.Finish()

	path, err := run.Write(reportDirFor(t))
	if err != nil {
		t.Fatalf("write report: %v", err)
	}
	if !run.Passed {
		t.Fatalf("large rollout read failed: %+v (report %s)", run.Checks, path)
	}
}

type rolloutReadResult struct {
	bytes      int64
	items      int
	retained   int
	peakHeap   uint64
	totalAlloc uint64
	elapsed    time.Duration
}

func measureRolloutRead(t *testing.T, path string) rolloutReadResult {
	t.Helper()
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	ctx, cancel := context.WithCancel(context.Background())
	var mu sync.Mutex
	peak := before.HeapAlloc
	done := make(chan struct{})
	go func() {
		defer close(done)
		sampler := metrics.Sampler{Interval: 20 * time.Millisecond}
		for sample := range sampler.Run(ctx) {
			mu.Lock()
			if sample.HeapAllocBytes > peak {
				peak = sample.HeapAllocBytes
			}
			mu.Unlock()
		}
	}()

	start := time.Now()
	items, err := readRecentRolloutItems(path)
	elapsed := time.Since(start)
	cancel()
	<-done
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	retained := 0
	for _, line := range items {
		if line.Type == "item" {
			retained += len(line.Item)
			continue
		}
		retained += len(line.Payload)
	}
	mu.Lock()
	peakHeap := peak
	mu.Unlock()
	return rolloutReadResult{
		bytes:      info.Size(),
		items:      len(items),
		retained:   retained,
		peakHeap:   peakHeap,
		totalAlloc: after.TotalAlloc - before.TotalAlloc,
		elapsed:    elapsed,
	}
}

func budgetsPathFor(t *testing.T) string {
	t.Helper()
	if path := os.Getenv("CODEX_STABILITY_BUDGETS"); path != "" {
		return path
	}
	return filepath.Join("..", "stability", "budgets.json")
}

func reportDirFor(t *testing.T) string {
	t.Helper()
	if path := os.Getenv("CODEX_STABILITY_REPORT_DIR"); path != "" {
		return path
	}
	return filepath.Join("..", "stability", "reports")
}

func envInt64(name string, fallback int64) int64 {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}
