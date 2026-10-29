//go:build stability

package turn

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"codex_go/model"
	"codex_go/stability/harness/metrics"
	"codex_go/stability/harness/report"
	"codex_go/tool"
)

func faultToolDispatcher(t *testing.T, run func(context.Context, *tool.Invocation) (*tool.Output, error)) *ToolDispatcher {
	t.Helper()
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("probe")}, run)); err != nil {
		t.Fatalf("register executor: %v", err)
	}
	return NewToolDispatcher(&ToolDispatcherOptions{Router: tool.NewRouter(registry)})
}

func faultToolItems(callID string) []model.AgentItem {
	return []model.AgentItem{{ID: callID, Type: "function_call", Name: "probe", CallID: callID, Arguments: "{}"}}
}

func finishFaultReport(t *testing.T, run *report.Report) {
	t.Helper()
	run.Finish()
	path, err := run.Write(reportDirForTurnFaults(t))
	if err != nil {
		t.Fatalf("write report: %v", err)
	}
	if !run.Passed {
		t.Fatalf("scenario failed: %+v (report %s)", run.Checks, path)
	}
}

func reportDirForTurnFaults(t *testing.T) string {
	t.Helper()
	if path := os.Getenv("CODEX_STABILITY_REPORT_DIR"); path != "" {
		return path
	}
	return filepath.Join("..", "stability", "reports")
}

func budgetsPathForTurnFaults(t *testing.T) string {
	t.Helper()
	if path := os.Getenv("CODEX_STABILITY_BUDGETS"); path != "" {
		return path
	}
	return filepath.Join("..", "stability", "budgets.json")
}

// TestFaultToolHandlerError drives a tool whose handler fails the way an MCP
// server or a shell command does. The failure must reach the model as text, the
// dispatcher must stay usable, and nothing may be left running.
func TestFaultToolHandlerError(t *testing.T) {
	run := report.New("fault-tool-handler-error")
	dispatcher := faultToolDispatcher(t, func(context.Context, *tool.Invocation) (*tool.Output, error) {
		return nil, errors.New("probe failed: exit status 2")
	})
	// Rust classifies an untyped handler error as fatal: it ends the turn with
	// a diagnosable error. A tool that wants the model to recover returns
	// RespondToModel, which becomes a failed tool result instead.
	_, fatalErr := dispatcher.ExecuteToolItems(context.Background(), faultToolItems("call-error"))
	run.Check("fault.tool.error.fatal_is_diagnosable", fatalErr != nil && strings.Contains(fatalErr.Error(), "exit status 2"),
		"err=%v", fatalErr)

	modelFacing := faultToolDispatcher(t, func(context.Context, *tool.Invocation) (*tool.Output, error) {
		return nil, tool.RespondToModel("probe failed: exit status 2")
	})
	results, modelErr := modelFacing.ExecuteToolItems(context.Background(), faultToolItems("call-error-model"))
	run.Check("fault.tool.error.responds_to_model", modelErr == nil && len(results) == 1 && strings.Contains(firstFaultResultText(results), "exit status 2"),
		"err=%v results=%d text=%q", modelErr, len(results), firstFaultResultText(results))

	// The failure must not wedge the dispatcher.
	healthy := faultToolDispatcher(t, func(context.Context, *tool.Invocation) (*tool.Output, error) {
		return &tool.Output{Success: true, Body: "ok"}, nil
	})
	followUp, err := healthy.ExecuteToolItems(context.Background(), faultToolItems("call-after-error"))
	run.Check("fault.tool.error.next_call_works", err == nil && len(followUp) == 1 && followUp[0].Response.Output.Text() == "ok",
		"err=%v results=%d", err, len(followUp))
	finishFaultReport(t, run)
}

// TestFaultToolHandlerPanic proves a panicking tool handler is contained: the
// registry recovers it into a fatal tool error, the process survives, and the
// turn-level dispatcher remains usable afterwards.
func TestFaultToolHandlerPanic(t *testing.T) {
	run := report.New("fault-tool-handler-panic")
	dispatcher := faultToolDispatcher(t, func(context.Context, *tool.Invocation) (*tool.Output, error) {
		panic("probe handler exploded")
	})
	var err error
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Fatalf("panic escaped the tool layer: %v", recovered)
			}
		}()
		_, err = dispatcher.ExecuteToolItems(context.Background(), faultToolItems("call-panic"))
	}()
	run.Check("fault.tool.panic.contained", err != nil && strings.Contains(err.Error(), "panicked"),
		"err=%v", err)

	healthy := faultToolDispatcher(t, func(context.Context, *tool.Invocation) (*tool.Output, error) {
		return &tool.Output{Success: true, Body: "ok"}, nil
	})
	followUp, followErr := healthy.ExecuteToolItems(context.Background(), faultToolItems("call-after-panic"))
	run.Check("fault.tool.panic.next_call_works", followErr == nil && len(followUp) == 1,
		"err=%v results=%d", followErr, len(followUp))
	finishFaultReport(t, run)
}

// TestFaultToolCancellation abandons a tool that never returns. Cancel must
// propagate into the handler, the dispatcher must return promptly, and no
// goroutine may survive it.
func TestFaultToolCancellation(t *testing.T) {
	run := report.New("fault-tool-cancellation")
	entered := make(chan struct{}, 1)
	dispatcher := faultToolDispatcher(t, func(ctx context.Context, _ *tool.Invocation) (*tool.Output, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	})
	runtime.GC()
	baseline := metrics.Take()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := dispatcher.ExecuteToolItems(ctx, faultToolItems("call-cancel"))
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		cancel()
		t.Fatal("handler never started")
	}
	start := time.Now()
	cancel()
	var err error
	select {
	case err = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("cancelled tool dispatch never returned")
	}
	elapsed := time.Since(start)
	// Give the runtime a moment to retire the handler goroutine.
	deadline := time.Now().Add(2 * time.Second)
	var after metrics.Sample
	for time.Now().Before(deadline) {
		after = metrics.Take()
		if after.Goroutines <= baseline.Goroutines {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}

	run.Env["cancel_latency"] = elapsed.String()
	run.Check("fault.tool.cancel.returned", err != nil, "err=%v", err)
	run.Check("fault.tool.cancel.latency", elapsed <= 2*time.Second, "elapsed=%s budget=2s", elapsed)
	run.Check("fault.tool.cancel.goroutines", after.Goroutines <= baseline.Goroutines,
		"baseline=%d after=%d", baseline.Goroutines, after.Goroutines)
	run.Metric("fault.tool.cancel_latency_ms", float64(elapsed.Milliseconds()))
	finishFaultReport(t, run)
}

// TestFaultToolErrorSoak repeats the failure paths for a long time. A leak in
// the error path (retained results, per-call goroutines, expanding metadata)
// only shows up after hundreds of failures, so this is the steady-state gate
// for the tool executor rather than a functional test.
func TestFaultToolErrorSoak(t *testing.T) {
	cycles := 300
	if value := os.Getenv("CODEX_STABILITY_TOOL_FAULT_CYCLES"); value != "" {
		if parsed, parseErr := strconv.Atoi(value); parseErr == nil && parsed > 0 {
			cycles = parsed
		}
	}
	run := report.New("fault-tool-error-soak")
	run.Env["cycles"] = strconv.Itoa(cycles)

	var calls atomic.Int64
	var expectedFatal atomic.Int64
	dispatcher := faultToolDispatcher(t, func(context.Context, *tool.Invocation) (*tool.Output, error) {
		switch calls.Add(1) % 3 {
		case 0:
			expectedFatal.Add(1)
			return nil, errors.New("probe failed")
		case 1:
			expectedFatal.Add(1)
			panic("probe panicked")
		default:
			return &tool.Output{Success: true, Body: "ok"}, nil
		}
	})
	runtime.GC()
	before := metrics.Take()
	start := time.Now()
	failures := 0
	for cycle := 0; cycle < cycles; cycle++ {
		results, err := dispatcher.ExecuteToolItems(context.Background(), faultToolItems(fmt.Sprintf("call-%d", cycle)))
		if err != nil {
			failures++
			continue
		}
		if len(results) != 1 {
			t.Fatalf("cycle %d: results=%d", cycle, len(results))
		}
	}
	elapsed := time.Since(start)
	runtime.GC()
	after := metrics.Take()

	budgets, err := report.LoadBudgets(budgetsPathForTurnFaults(t))
	if err != nil {
		t.Fatalf("load budgets: %v", err)
	}
	retained := int64(0)
	if after.HeapAllocBytes > before.HeapAllocBytes {
		retained = int64(after.HeapAllocBytes - before.HeapAllocBytes)
	}
	goroutines := after.Goroutines - before.Goroutines

	run.Env["failures"] = strconv.Itoa(failures)
	run.Env["elapsed"] = elapsed.String()
	run.Check("fault.tool.soak.cycles", calls.Load() == int64(cycles), "calls=%d cycles=%d", calls.Load(), cycles)
	run.Check("fault.tool.soak.failures_bounded", failures == int(expectedFatal.Load()),
		"failures=%d expected_fatal=%d cycles=%d", failures, expectedFatal.Load(), cycles)
	run.Check("fault.tool.soak.goroutines", goroutines <= budgets.Soak.GoroutineDelta, "%+d (budget +%d)", goroutines, budgets.Soak.GoroutineDelta)
	run.Check("fault.tool.soak.retained_bytes", retained < 8<<20, "retained=%d budget=8MiB", retained)
	run.Metric("fault.tool.soak.retained_bytes", float64(retained))
	run.Metric("fault.tool.soak.goroutines_delta", float64(goroutines))
	run.Metric("fault.tool.soak.elapsed_ms", float64(elapsed.Milliseconds()))
	finishFaultReport(t, run)
}

func firstFaultResultText(results []ToolExecutionResult) string {
	if len(results) == 0 || results[0].Response == nil || results[0].Response.Output == nil {
		return ""
	}
	return results[0].Response.Output.Text()
}
