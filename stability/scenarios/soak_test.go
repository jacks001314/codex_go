//go:build stability

package scenarios

import (
	"context"
	"fmt"
	"os"
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

func TestSoakTurns(t *testing.T) {
	duration := envDuration("CODEX_STABILITY_DURATION", 0)
	if duration <= 0 {
		t.Skip("set CODEX_STABILITY_DURATION (for example 2m) to run the soak")
	}
	warmup := envDuration("CODEX_STABILITY_WARMUP", defaultWarmup(duration))
	settle := envDuration("CODEX_STABILITY_SETTLE", 2*time.Second)

	// The report must start before the run so started_at/ended_at bound the window.
	run := report.New("soak")

	server := mockmodel.New()
	defer server.Close()
	runner := newRunner(server.URL())

	runFor := func(window time.Duration) int {
		ctx, cancel := context.WithTimeout(context.Background(), window)
		defer cancel()
		turns := 0
		for ctx.Err() == nil {
			turnCtx, turnCancel := context.WithTimeout(context.Background(), 30*time.Second)
			_, err := runner.Run(turnCtx, &model.AgentRequest{Prompt: "ping", Model: "gpt-mock"})
			turnCancel()
			if err != nil {
				t.Fatalf("turn %d failed: %v", turns, err)
			}
			turns++
		}
		return turns
	}

	turns := runFor(warmup)
	time.Sleep(settle)
	baseline := metrics.Take()
	// Connection lifecycle evidence: a process-wide handle count is noisy on
	// Windows, but the fixture can prove whether each connection the client
	// opened was also closed.
	connBefore := server.ConnStats()

	samples := []metrics.Sample{baseline}
	samplerCtx, samplerCancel := context.WithTimeout(context.Background(), duration)
	samplerDone := make(chan struct{})
	var samplerMu sync.Mutex
	go func() {
		defer close(samplerDone)
		sampler := metrics.Sampler{Interval: time.Second}
		for sample := range sampler.Run(samplerCtx) {
			samplerMu.Lock()
			samples = append(samples, sample)
			samplerMu.Unlock()
		}
	}()

	turns += runFor(duration)
	samplerCancel()
	<-samplerDone
	time.Sleep(settle)
	samples = append(samples, metrics.Take())
	conns := server.ConnStats()

	budgets, err := report.LoadBudgets(budgetsPath())
	if err != nil {
		t.Fatalf("load budgets: %v", err)
	}
	run.Env["duration"] = duration.String()
	run.Env["warmup"] = warmup.String()
	run.Env["settle"] = settle.String()
	run.Env["turns"] = strconv.Itoa(turns)
	run.Env["fd_delta_series"] = fdDeltaSeries(samples)
	opened := conns.Opened - connBefore.Opened
	reclaimed := conns.Closed - connBefore.Closed
	active := conns.Active
	run.Env["connections_opened"] = strconv.Itoa(opened)
	run.Env["connections_closed"] = strconv.Itoa(reclaimed)
	run.Env["connections_active"] = strconv.Itoa(active)
	for _, sample := range samples {
		run.AddSample(sample)
	}
	// Samples start at the post-warmup baseline, so the series is already steady.
	for _, check := range budgets.ValidateSoakWithWarmup(run.Samples, 0) {
		run.AddCheck(check)
	}
	run.Check("soak.turns", turns > 0, "turns=%d", turns)
	run.Metric("soak.turns", float64(turns))
	run.Metric("soak.heap_peak_bytes", float64(maxSampleValue(samples, func(sample metrics.Sample) uint64 { return sample.HeapAllocBytes })))
	run.Metric("soak.rss_peak_bytes", float64(maxSampleValue(samples, func(sample metrics.Sample) uint64 { return sample.RSSBytes })))
	run.Metric("soak.goroutines_final", float64(samples[len(samples)-1].Goroutines))
	run.Metric("soak.open_fds_final", float64(samples[len(samples)-1].OpenFDs))
	run.Metric("soak.open_fds_delta", float64(samples[len(samples)-1].OpenFDs-samples[0].OpenFDs))
	run.Metric("soak.connections_opened", float64(opened))
	run.Metric("soak.connections_closed", float64(reclaimed))
	run.Metric("soak.connections_active", float64(active))
	for _, check := range budgets.ValidateConnections(
		report.ConnCounts{Opened: connBefore.Opened, Closed: connBefore.Closed, Active: connBefore.Active},
		report.ConnCounts{Opened: conns.Opened, Closed: conns.Closed, Active: conns.Active},
	) {
		run.AddCheck(check)
	}
	run.Finish()

	path, err := run.Write(reportDir())
	if err != nil {
		t.Fatalf("write report: %v", err)
	}
	if !run.Passed {
		t.Fatalf("soak failed: %+v (report %s)", run.Checks, path)
	}
}

func envDuration(name string, fallback time.Duration) time.Duration {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}

func defaultWarmup(duration time.Duration) time.Duration {
	warmup := duration / 4
	if warmup > 10*time.Second {
		warmup = 10 * time.Second
	}
	if warmup < time.Second {
		warmup = time.Second
	}
	return warmup
}

// fdDeltaSeries renders the handle delta relative to the baseline every tenth
// sample so a report shows whether handles settle or grow without bound.
func fdDeltaSeries(samples []metrics.Sample) string {
	if len(samples) == 0 {
		return ""
	}
	baseline := samples[0].OpenFDs
	var builder strings.Builder
	stride := len(samples) / 20
	if stride < 1 {
		stride = 1
	}
	for index, sample := range samples {
		if index%stride != 0 {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteByte(',')
		}
		fmt.Fprintf(&builder, "%d:%+d", int(sample.At.Sub(samples[0].At).Seconds()), sample.OpenFDs-baseline)
	}
	return builder.String()
}
