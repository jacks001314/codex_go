//go:build stability

package scenarios

import (
	"context"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"codex_go/stability/harness/metrics"
	"codex_go/stability/harness/mockmodel"
	"codex_go/stability/harness/report"
	codextui "codex_go/tui"
)

// TestLargeToolOutput streams one assistant message far larger than a normal
// turn and asserts it round-trips without exceeding the heap budget. It also
// covers the single-event case: the final response.output_item.done frame
// carries the whole payload on one SSE data line.
func TestLargeToolOutput(t *testing.T) {
	payloadBytes := envIntDefault("CODEX_STABILITY_LARGE_TOOL_BYTES", 32<<20)
	if payloadBytes < 64<<10 {
		payloadBytes = 64 << 10
	}
	payload := strings.Repeat("a", payloadBytes)
	server := mockmodel.New(mockmodel.TextWithChunkSize(payload, 64<<10))
	defer server.Close()
	runner := newRunner(server.URL())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	var peak atomic.Uint64
	stopSampling := make(chan struct{})
	samplingDone := make(chan struct{})
	go func() {
		defer close(samplingDone)
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopSampling:
				return
			case <-ticker.C:
				sample := metrics.Take()
				for {
					current := peak.Load()
					if sample.HeapAllocBytes <= current || peak.CompareAndSwap(current, sample.HeapAllocBytes) {
						break
					}
				}
			}
		}
	}()

	start := time.Now()
	response, err := runner.Run(ctx, faultRequest())
	elapsed := time.Since(start)
	close(stopSampling)
	<-samplingDone
	runtime.GC()
	final := metrics.Take()

	budgets, err2 := report.LoadBudgets(budgetsPath())
	if err2 != nil {
		t.Fatalf("load budgets: %v", err2)
	}
	run := report.New("large-tool-output")
	run.Env["payload_bytes"] = strconv.Itoa(payloadBytes)
	run.Env["elapsed"] = elapsed.String()
	if err != nil {
		run.Check("large.tool_output.completed", false, "turn failed: %v", err)
		finishReport(t, run)
		return
	}
	got := ""
	if response != nil {
		got = response.Message
	}
	run.Check("large.tool_output.round_trip", len(got) == len(payload),
		"len=%d want=%d", len(got), len(payload))

	peakHeap := peak.Load()
	run.Check("large.tool_output.peak_heap", peakHeap <= budgets.Large.PeakHeapBytes,
		"peak=%d budget=%d", peakHeap, budgets.Large.PeakHeapBytes)
	// The peak must stay a constant multiple of the payload (copies along the
	// SSE -> accumulator -> response path), not grow with anything else.
	proportional := uint64(payloadBytes) * 12
	run.Check("large.tool_output.proportional", peakHeap <= proportional,
		"peak=%d limit=%d", peakHeap, proportional)
	run.Metric("large.tool_output.peak_heap_bytes", float64(peakHeap))
	run.Metric("large.tool_output.payload_bytes", float64(payloadBytes))
	run.Metric("large.tool_output.elapsed_ms", float64(elapsed.Milliseconds()))
	run.Env["heap_after_gc"] = strconv.FormatUint(final.HeapAllocBytes, 10)
	finishReport(t, run)
}

// TestLargeThreadItems builds a very long thread in memory and bounds both the
// per-item retention and the append cost. A quadratic transcript store shows up
// here long before a user notices the TUI stalling.
func TestLargeThreadItems(t *testing.T) {
	items := envIntDefault("CODEX_STABILITY_LARGE_ITEMS", 200000)
	budgets, err := report.LoadBudgets(budgetsPath())
	if err != nil {
		t.Fatalf("load budgets: %v", err)
	}

	state := codextui.NewState(&codextui.Options{Model: "gpt-5"})
	runtime.GC()
	before := metrics.Take()
	start := time.Now()
	for i := 0; i < items; i++ {
		role := codextui.RoleUser
		if i%2 == 1 {
			role = codextui.RoleAssistant
		}
		state.AddMessage(role, "message body for the long thread stress case")
	}
	elapsed := time.Since(start)
	runtime.GC()
	after := metrics.Take()

	retained := uint64(0)
	if after.HeapAllocBytes > before.HeapAllocBytes {
		retained = after.HeapAllocBytes - before.HeapAllocBytes
	}
	perItem := float64(retained) / float64(items)

	run := report.New("large-thread-items")
	run.Env["items"] = strconv.Itoa(items)
	run.Env["elapsed"] = elapsed.String()
	run.Check("large.thread_items.count", len(state.Messages) == items,
		"messages=%d want=%d", len(state.Messages), items)
	run.Check("large.thread_items.per_item_bytes", perItem <= 512,
		"per_item=%.1f retained=%d budget=512", perItem, retained)
	run.Check("large.thread_items.peak_heap", after.HeapAllocBytes <= budgets.Large.PeakHeapBytes,
		"heap=%d budget=%d", after.HeapAllocBytes, budgets.Large.PeakHeapBytes)
	// 200k appends must stay linear; a quadratic store blows well past this.
	run.Check("large.thread_items.append_ms", elapsed <= 30*time.Second,
		"elapsed=%s budget=30s", elapsed)
	run.Metric("large.thread_items.per_item_bytes", perItem)
	run.Metric("large.thread_items.retained_bytes", float64(retained))
	run.Metric("large.thread_items.append_ms", float64(elapsed.Milliseconds()))
	finishReport(t, run)
}
