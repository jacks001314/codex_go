//go:build stability

package scenarios

import (
	"context"
	"sync"
	"testing"
	"time"

	"codex_go/model"
	"codex_go/stability/harness/metrics"
	"codex_go/stability/harness/mockmodel"
	"codex_go/stability/harness/report"
)

// TestSoakIdle keeps the process alive with nothing but a periodic tick and
// asserts that goroutines and file descriptors return to the post-warmup
// baseline. It covers the "TUI left open overnight" case: refresh timers must
// not accumulate.
func TestSoakIdle(t *testing.T) {
	duration := envDuration("CODEX_STABILITY_IDLE_DURATION", 0)
	if duration <= 0 {
		duration = envDuration("CODEX_STABILITY_DURATION", 0)
	}
	if duration <= 0 {
		t.Skip("set CODEX_STABILITY_DURATION (or CODEX_STABILITY_IDLE_DURATION) to run the idle soak")
	}
	settle := envDuration("CODEX_STABILITY_SETTLE", 2*time.Second)
	run := report.New("soak-idle")
	run.Env["duration"] = duration.String()
	run.Env["settle"] = settle.String()

	// Warm up through real turns so lazily started HTTP and agent goroutines
	// exist before the baseline is sampled; otherwise "growth" is just startup.
	server := mockmodel.New()
	defer server.Close()
	runner := newRunner(server.URL())
	for turn := 0; turn < 5; turn++ {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_, err := runner.Run(ctx, &model.AgentRequest{Prompt: "ping", Model: "gpt-mock"})
		cancel()
		if err != nil {
			t.Fatalf("warmup turn %d failed: %v", turn, err)
		}
	}
	time.Sleep(settle)
	samples := []metrics.Sample{metrics.Take()}

	ctx, cancel := context.WithTimeout(context.Background(), duration)
	var mu sync.Mutex
	samplerDone := make(chan struct{})
	go func() {
		defer close(samplerDone)
		sampler := metrics.Sampler{Interval: time.Second}
		for sample := range sampler.Run(ctx) {
			mu.Lock()
			samples = append(samples, sample)
			mu.Unlock()
		}
	}()

	// The tick stands in for the TUI status refresh: it must not spawn work
	// that outlives the tick.
	ticker := time.NewTicker(100 * time.Millisecond)
	ticks := 0
	running := true
	for running {
		select {
		case <-ctx.Done():
			running = false
		case <-ticker.C:
			ticks++
		}
	}
	ticker.Stop()
	cancel()
	<-samplerDone
	time.Sleep(settle)

	mu.Lock()
	samples = append(samples, metrics.Take())
	collected := append([]metrics.Sample(nil), samples...)
	mu.Unlock()

	budgets, err := report.LoadBudgets(budgetsPath())
	if err != nil {
		t.Fatalf("load budgets: %v", err)
	}
	for _, sample := range collected {
		run.AddSample(sample)
	}
	// Samples start at the post-warmup baseline, so the series is already
	// steady and needs no additional trimming.
	for _, check := range budgets.ValidateSoakWithWarmup(collected, 0) {
		run.AddCheck(check)
	}
	run.Check("soak.idle_ticks", ticks > 0, "ticks=%d", ticks)
	run.Metric("soak.idle_ticks", float64(ticks))
	run.Metric("soak.goroutines_final", float64(collected[len(collected)-1].Goroutines))
	run.Metric("soak.open_fds_final", float64(collected[len(collected)-1].OpenFDs))
	run.Finish()
	path, err := run.Write(reportDir())
	if err != nil {
		t.Fatalf("write report: %v", err)
	}
	if !run.Passed {
		t.Fatalf("idle soak failed: %+v (report %s)", run.Checks, path)
	}
}
