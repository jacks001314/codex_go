//go:build stability

package scenarios

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"codex_go/model"
	"codex_go/stability/harness/metrics"
	"codex_go/stability/harness/mockmodel"
	"codex_go/stability/harness/report"
)

// TestSoakReconnect abandons in-flight streams the way a flaky network does:
// every cycle starts a turn, waits until the server is holding the stream, and
// then cancels the request. A transport that is not reclaimed shows up as
// goroutine growth or as connections the server never sees closed.
//
// The connection check is deliberate. A process-wide handle count is not a
// usable gate on Windows: Go's runtime and Winsock keep a bounded set of
// handles that does not scale with the cycle count (evidence: 123 opened, 122
// closed, delta flat at +7 over 120 cycles), so a small absolute handle budget
// measures platform bookkeeping instead of a leak. The handle delta is still
// recorded as a metric for trend analysis.
func TestSoakReconnect(t *testing.T) {
	cycles := envIntDefault("CODEX_STABILITY_RECONNECT_CYCLES", 120)
	run := report.New("soak-reconnect")
	run.Env["cycles"] = strconv.Itoa(cycles)

	server := mockmodel.New()
	defer server.Close()
	runner := newRunner(server.URL())

	// Warm up so lazily created HTTP/agent goroutines and pooled connections
	// exist before the baseline is taken. The warmup must use the operation
	// under test: a completed turn leaves a pooled idle connection, while a
	// cancelled stream tears its connection down, so warming with only the
	// former would charge the steady-state cost to the measurement.
	server.Fallback(mockmodel.Text("warm"))
	for turn := 0; turn < 3; turn++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if _, err := runner.Run(ctx, faultRequest()); err != nil {
			cancel()
			t.Fatalf("warmup turn %d failed: %v", turn, err)
		}
		cancel()
	}
	for warmup := 0; warmup < 3; warmup++ {
		abandonStreamCycle(t, server, runner, -1)
	}
	settle := envDuration("CODEX_STABILITY_SETTLE", 2*time.Second)
	time.Sleep(settle)
	baseline := metrics.Take()
	// Count only the connections the measured cycles open, so the report can
	// tell "reclaimed" apart from "never opened".
	before := server.ConnStats()

	open := 0
	series := make([]string, 0, cycles/10+2)
	for cycle := 0; cycle < cycles; cycle++ {
		abandonStreamCycle(t, server, runner, cycle)
		open++
		if cycle%10 == 9 {
			sample := metrics.Take()
			series = append(series, fmt.Sprintf("%d:%d", cycle+1, sample.OpenFDs-baseline.OpenFDs))
		}
	}

	time.Sleep(settle)
	final := metrics.Take()
	conns := server.ConnStats()
	opened := conns.Opened - before.Opened
	reclaimed := conns.Closed - before.Closed
	active := conns.Active

	run.Env["abandoned"] = strconv.Itoa(open)
	run.Env["fd_delta_series"] = strings.Join(series, ",")
	run.Env["baseline_goroutines"] = strconv.Itoa(baseline.Goroutines)
	run.Env["final_goroutines"] = strconv.Itoa(final.Goroutines)
	run.Env["connections_opened"] = strconv.Itoa(opened)
	run.Env["connections_closed"] = strconv.Itoa(reclaimed)
	run.Env["connections_active"] = strconv.Itoa(active)

	budgets, err := report.LoadBudgets(budgetsPath())
	if err != nil {
		t.Fatalf("load budgets: %v", err)
	}
	run.Check("soak.reconnect.all_abandoned", open == cycles, "abandoned=%d cycles=%d", open, cycles)
	goroutines := final.Goroutines - baseline.Goroutines
	run.Check("soak.reconnect.goroutines", goroutines <= budgets.Soak.GoroutineDelta,
		"%+d (budget +%d)", goroutines, budgets.Soak.GoroutineDelta)
	// Every abandoned stream must be closed on the server side. One connection
	// may still be settling when the sample is taken.
	run.Check("soak.reconnect.connections_reclaimed", reclaimed >= opened-1,
		"opened=%d closed=%d active=%d", opened, reclaimed, active)
	run.Metric("soak.reconnect.goroutines_delta", float64(goroutines))
	run.Metric("soak.reconnect.connections_opened", float64(opened))
	run.Metric("soak.reconnect.connections_reclaimed", float64(reclaimed))
	run.Metric("soak.reconnect.open_fds_delta", float64(final.OpenFDs-baseline.OpenFDs))
	finishReport(t, run)
}

// abandonStreamCycle starts one turn, waits until the server is streaming, and
// cancels it the way a dropped connection would. The released gate lets the
// handler observe the disconnect and exit.
func abandonStreamCycle(t *testing.T, server *mockmodel.Server, runner *model.ResponsesAgentRunner, cycle int) {
	t.Helper()
	gate := mockmodel.NewGate()
	server.Enqueue(mockmodel.Text("recovered").GateAt(0, gate))
	want := server.RequestCount() + 1
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	done := make(chan struct{}, 1)
	go func() {
		defer close(done)
		_, _ = runner.Run(ctx, faultRequest())
	}()
	if !waitForRequestCount(server, want, 5*time.Second) {
		cancel()
		<-done
		t.Fatalf("cycle %d: server never received the request", cycle)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("cycle %d: cancelled run never returned", cycle)
	}
	gate.Release()
}

// waitForRequestCount polls the mock server until it has seen want requests.
func waitForRequestCount(server *mockmodel.Server, want int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if server.RequestCount() >= want {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return server.RequestCount() >= want
}
