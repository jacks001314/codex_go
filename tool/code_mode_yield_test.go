package tool

import (
	"context"
	"testing"
	"time"
)

// #48135: the sampling request's preemption signal travels in the step context
// and reaches the code-mode exec/wait invocations through the dispatcher.
func TestCodeModePreemptContextRoundTripAndToolNames(t *testing.T) {
	if signal := CodeModePreemptFromContext(nil); signal != nil {
		t.Fatalf("nil context signal = %#v", signal)
	}
	if signal := CodeModePreemptFromContext(context.Background()); signal != nil {
		t.Fatalf("absent signal = %#v", signal)
	}
	// A nil signal leaves the context untouched, so a step without the feature
	// cannot accidentally arm a cell.
	if signal := CodeModePreemptFromContext(WithCodeModePreempt(context.Background(), nil)); signal != nil {
		t.Fatalf("nil signal round trip = %#v", signal)
	}

	signal := NewYieldSignal()
	if got := CodeModePreemptFromContext(WithCodeModePreempt(context.Background(), signal)); got != signal {
		t.Fatalf("round-tripped signal = %#v", got)
	}

	if !IsCodeModeToolName(PlainName(CodeModeExecToolName)) || !IsCodeModeToolName(PlainName(CodeModeWaitToolName)) {
		t.Fatal("the public exec and wait tools must be code-mode tools")
	}
	if IsCodeModeToolName(PlainName(DefaultShellCommandToolName)) || IsCodeModeToolName(NamespacedName("mcp", CodeModeExecToolName)) {
		t.Fatal("a shell tool and a namespaced lookalike must not be code-mode tools")
	}
}

// Mirrors Rust #48123: a request carrying a preempt signal must run as an
// observable cell (so a later yield frame can end its observation) instead of
// the inline fast path.
func TestCodeModeExecPreemptRunsObservableCell(t *testing.T) {
	runtime := NewCodeModeRuntime(nil, false)
	defer func() { _ = runtime.Close() }()
	exec, _ := runtime.Executors(NewRegistry())
	var observed []CodeModeCallObservation
	runtime.SetCallObserver(func(observation CodeModeCallObservation) {
		observed = append(observed, observation)
	})

	signal := NewYieldSignal()
	if _, err := exec.Execute(context.Background(), &Invocation{
		CallID:  "call-preempt",
		Payload: Payload{Kind: PayloadCustom, Input: `text("x")`},
		Context: map[string]any{CodeModePreemptContextKey: signal},
	}); err != nil {
		t.Fatalf("exec error = %v", err)
	}

	started := false
	for _, observation := range observed {
		if observation.Kind == CodeModeCallCellStarted && observation.CellID != "" {
			started = true
		}
	}
	if !started {
		t.Fatalf("a preempt-carrying exec did not publish a cell start: %#v", observed)
	}
}

// Mirrors Rust #48123: firing the wait observation's preempt signal ends the
// observation with the cell's live state instead of waiting for its yield time.
func TestCodeModeWaitPreemptYieldsRunningObservation(t *testing.T) {
	runtime := NewCodeModeRuntime(nil, false)
	defer func() { _ = runtime.Close() }()
	exec, waitExecutor := runtime.Executors(NewRegistry())
	executor, ok := exec.(*codeModeExecExecutor)
	if !ok {
		t.Fatalf("exec executor type = %T", exec)
	}
	cell := &codeModeCell{done: make(chan struct{}), cancel: func() {}, startedAt: time.Now()}
	executor.cellsMu.Lock()
	executor.cells["cell-running"] = cell
	executor.cellsMu.Unlock()
	defer close(cell.done)

	signal := NewYieldSignal()
	signal.Cancel()
	begin := time.Now()
	output, err := waitExecutor.Execute(context.Background(), &Invocation{
		CallID:  "wait-preempt",
		Payload: Payload{Kind: PayloadFunction, Arguments: `{"cell_id":"cell-running","yield_time_ms":60000}`},
		Context: map[string]any{CodeModePreemptContextKey: signal},
	})
	if err != nil {
		t.Fatalf("wait error = %v", err)
	}
	if elapsed := time.Since(begin); elapsed > 5*time.Second {
		t.Fatalf("the preempted wait did not return early: %v", elapsed)
	}
	if output == nil || output.Data == nil || output.Data["running"] != true {
		t.Fatalf("wait output = %#v", output)
	}
}
