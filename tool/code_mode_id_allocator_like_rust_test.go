package tool

import (
	"context"
	"sync"
	"testing"
	"time"
)

// Rust #48123 attaches a sampling request's preemption signal to every code-mode
// call of that request so the observation yields while the cell keeps running
// (core/tests/suite/pending_input.rs:1514
// `steers_yield_exec_and_wait_without_stopping_the_cell`, parametrized on/off).
// The Rust case shows the nested tool call is identical whether or not
// instant-interrupt is on, so the `{parent_call_id}-nested-{n}` call id must not
// depend on whether the host attached a preempt signal. Go shared one counter
// between `cell-%d` and `%s-nested-%d`, so a preempted exec started its nested
// call at `-nested-2` while the same script run unobserved started at
// `-nested-1`.
func TestCodeModeNestedCallIDIsIndependentOfPreemptLikeRust(t *testing.T) {
	for _, preempted := range []bool{false, true} {
		name := "unobserved"
		if preempted {
			name = "preempted"
		}
		t.Run(name, func(t *testing.T) {
			registry := NewRegistry()
			if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName(DefaultShellCommandToolName)}, func(context.Context, *Invocation) (*Output, error) {
				return &Output{Success: true, Body: "Process exited with code 0\nOutput:\nOK\n", Data: map[string]any{"exit_code": 0, "timed_out": false}}, nil
			})); err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			started := []string{}
			invocationCtx := map[string]any{
				"code_mode_nested_tool_started": CodeModeNestedToolStartedFunc(func(_ context.Context, invocation *Invocation, _ time.Time) {
					mu.Lock()
					defer mu.Unlock()
					started = append(started, invocation.CallID)
				}),
			}
			if preempted {
				invocationCtx[CodeModePreemptContextKey] = NewYieldSignal()
			}
			// No `yield_time_ms` pragma and no `yield_control()`: without a preempt
			// signal the executor takes the inline path, with one it runs an
			// observable cell. The nested call must be numbered the same either way.
			output, err := NewCodeModeExecExecutor(registry).Execute(context.Background(), &Invocation{
				CallID:  "outer-exec",
				Context: invocationCtx,
				Payload: Payload{Kind: PayloadCustom, Input: `const r = await tools.shell_command({"cmd":"echo hi"}); text(r.output);`},
			})
			if err != nil {
				t.Fatal(err)
			}
			if output == nil || !output.Success {
				t.Fatalf("output = %#v", output)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(started) != 1 || started[0] != "outer-exec-nested-1" {
				t.Fatalf("nested call ids = %#v, want [\"outer-exec-nested-1\"]", started)
			}
		})
	}
}

// The observable-cell id sequence is independent of nested calls: starting two
// cells still yields cell-1 and cell-2, and a nested call in between does not
// consume a cell number (Rust numbers cells and nested call ids separately).
func TestCodeModeCellIDsIgnoreNestedCallsLikeRust(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName(DefaultShellCommandToolName)}, func(context.Context, *Invocation) (*Output, error) {
		return &Output{Success: true, Body: "Process exited with code 0\nOutput:\nOK\n", Data: map[string]any{"exit_code": 0, "timed_out": false}}, nil
	})); err != nil {
		t.Fatal(err)
	}
	executor := NewCodeModeExecExecutor(registry)
	cellIDs := []string{}
	for i := 0; i < 2; i++ {
		output, err := executor.Execute(context.Background(), &Invocation{
			CallID: "cell-run",
			Payload: Payload{Kind: PayloadCustom, Input: `// @exec: {"yield_time_ms": 0}
				const r = await tools.shell_command({"cmd":"echo hi"});
				await new Promise(resolve => setTimeout(resolve, 25)); text(r.output);`},
		})
		if err != nil {
			t.Fatal(err)
		}
		cellID, _ := output.Data["cell_id"].(string)
		if cellID == "" {
			t.Fatalf("call %d returned no cell id: %#v", i, output)
		}
		cellIDs = append(cellIDs, cellID)
	}
	if cellIDs[0] != "cell-1" || cellIDs[1] != "cell-2" {
		t.Fatalf("cell ids = %#v, want [cell-1 cell-2]", cellIDs)
	}
}
