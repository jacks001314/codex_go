package metrics_test

import (
	"context"
	"runtime"
	"testing"
	"time"

	"codex_go/stability/harness/metrics"
)

func TestTakeReportsHeapAndGoroutines(t *testing.T) {
	sample := metrics.Take()
	if sample.Goroutines <= 0 {
		t.Fatalf("goroutines = %d, want > 0", sample.Goroutines)
	}
	if sample.HeapAllocBytes == 0 {
		t.Fatal("heap alloc = 0, want a live process")
	}
	if runtime.GOOS == "windows" || runtime.GOOS == "linux" {
		if !sample.RSSKnown || sample.RSSBytes == 0 {
			t.Fatalf("rss known=%v bytes=%d, want a positive reading", sample.RSSKnown, sample.RSSBytes)
		}
		if !sample.FDsKnown || sample.OpenFDs <= 0 {
			t.Fatalf("fds known=%v count=%d, want a positive reading", sample.FDsKnown, sample.OpenFDs)
		}
	}
}

func TestSamplerEmitsASeries(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	sampler := metrics.Sampler{Interval: 10 * time.Millisecond}
	var samples []metrics.Sample
	for sample := range sampler.Run(ctx) {
		samples = append(samples, sample)
	}
	if len(samples) < 2 {
		t.Fatalf("samples = %d, want at least 2", len(samples))
	}
	for index := 1; index < len(samples); index++ {
		if samples[index].At.Before(samples[index-1].At) {
			t.Fatalf("sample timestamps are not monotonic: %v then %v", samples[index-1].At, samples[index].At)
		}
	}
}
