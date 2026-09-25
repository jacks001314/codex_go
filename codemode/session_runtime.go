package codemode

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"codex_go/tool"
)

type StartedCell struct {
	CellID          CellID
	InitialResponse RuntimeResponse
}

type SessionRuntime struct {
	mu      sync.Mutex
	cells   *CellStore
	closed  bool
	nextID  uint64
	wakeups map[string]chan struct{}
	// engines tracks each running cell's script, so a termination can still
	// deliver output the script had already produced before the observer was
	// detached (Rust #48207).
	engines map[string]*engineState
	factory EngineFactory
}

func NewSessionRuntime() *SessionRuntime {
	return &SessionRuntime{
		cells:   NewCellStore(),
		nextID:  1,
		wakeups: map[string]chan struct{}{},
		engines: map[string]*engineState{},
		factory: SobekEngineFactory{},
	}
}

func (r *SessionRuntime) Cells() *CellStore {
	if r == nil {
		return nil
	}
	return r.cells
}

// Execute starts a cell. Rust #48123's preempt signal ends the foreground
// observation early while the cell keeps running and stays available to later
// waits; a nil signal never preempts.
func (r *SessionRuntime) Execute(ctx context.Context, request *ExecuteRequest, preempt *tool.YieldSignal) (*StartedCell, error) {
	startedAt := time.Now()
	if r == nil {
		return nil, fmt.Errorf("code mode session runtime is nil")
	}
	if request == nil {
		return nil, fmt.Errorf("execute request is nil")
	}
	if err := request.Validate(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, fmt.Errorf("code mode session is closed")
	}
	cellID := NewCellID(fmt.Sprintf("%d", r.nextID))
	r.nextID++
	wakeup := make(chan struct{})
	r.wakeups[cellID.String()] = wakeup
	r.mu.Unlock()

	if _, err := r.cells.Start(cellID.String(), request.Source); err != nil {
		return nil, err
	}
	engine, err := r.factory.NewEngine()
	if err != nil {
		return nil, err
	}
	// The script runs off the caller's goroutine so the yield timer and the
	// preempt signal can end the observation while the cell keeps running
	// (Rust's cell actor select).
	engineState := r.registerEngine(cellID.String(), engine)
	go func() {
		result, execErr := engine.Execute(ctx, EngineRequest{ToolCallID: request.ToolCallID, Source: request.Source, EnabledTools: request.EnabledTools})
		_ = engine.Close()
		r.publishEngineOutcome(cellID.String(), engineOutcome{result: result, execErr: execErr})
	}()

	yieldMS := ProtocolDefaultExecYieldTimeMS
	if request.YieldTimeMS != nil {
		yieldMS = *request.YieldTimeMS
	}
	var timer <-chan time.Time
	if yieldMS == 0 {
		// Rust's YieldAfter(0) yields before the script produces anything.
		go r.finishCellFromEngine(cellID, engineState.wake, wakeup)
		return &StartedCell{
			CellID:          cellID,
			InitialResponse: withHostDuration(Yielded(cellID, nil), startedAt),
		}, nil
	}
	deadline := time.NewTimer(time.Duration(yieldMS) * time.Millisecond)
	defer deadline.Stop()
	timer = deadline.C
	select {
	case <-engineState.wake:
		outcome := r.takeEngineOutcome(cellID.String())
		if outcome == nil {
			return nil, fmt.Errorf("code mode cell %s lost its script outcome", cellID.String())
		}
		response, err := r.completeCellFromEngine(cellID, *outcome, startedAt)
		if err != nil {
			return nil, err
		}
		return &StartedCell{
			CellID:          cellID,
			InitialResponse: response,
		}, nil
	case <-timer:
	case <-preempt.Done():
	case <-ctx.Done():
	}
	go r.finishCellFromEngine(cellID, engineState.wake, wakeup)
	return &StartedCell{
		CellID:          cellID,
		InitialResponse: withHostDuration(Yielded(cellID, nil), startedAt),
	}, nil
}

func (r *SessionRuntime) Wait(ctx context.Context, request *WaitRequest, preempt *tool.YieldSignal) (*WaitOutcome, error) {
	startedAt := time.Now()
	if r == nil {
		return nil, fmt.Errorf("code mode session runtime is nil")
	}
	if request == nil {
		return nil, fmt.Errorf("wait request is nil")
	}
	cellID := request.CellID.String()
	if strings.TrimSpace(cellID) == "" {
		return nil, fmt.Errorf("cell_id is required")
	}
	deadline := time.Duration(request.YieldTimeMS) * time.Millisecond
	if request.YieldTimeMS == 0 {
		deadline = time.Duration(ProtocolDefaultWaitYieldTimeMS) * time.Millisecond
	}
	if cell, ok := r.cells.Get(cellID); ok && cell.Status != CellRunning {
		outcome := LiveCell(withHostDuration(runtimeResponseFromCell(cell), startedAt))
		return &outcome, nil
	}
	wakeup := r.wakeup(cellID)
	timer := time.NewTimer(deadline)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-wakeup:
	case <-timer.C:
	case <-preempt.Done():
		// #48123: the caller yielded the observation; the cell keeps running.
	}
	cell, ok := r.cells.Get(cellID)
	if !ok {
		response := withHostDuration(Result(request.CellID, nil, stringPtrLocal(fmt.Sprintf("exec cell %s not found", cellID))), startedAt)
		outcome := MissingCell(response)
		return &outcome, nil
	}
	outcome := LiveCell(withHostDuration(runtimeResponseFromCell(cell), startedAt))
	return &outcome, nil
}

func (r *SessionRuntime) Terminate(cellID CellID) (*WaitOutcome, error) {
	startedAt := time.Now()
	if r == nil {
		return nil, fmt.Errorf("code mode session runtime is nil")
	}
	// Capture the cell's wakeup channel before the termination so a script that
	// finishes concurrently wakes this wait instead of a fresh channel.
	wakeup := r.wakeup(cellID.String())
	previous, _ := r.cells.Get(cellID.String())
	cell, err := r.cells.Terminate(cellID.String())
	if err != nil {
		response := withHostDuration(Result(cellID, nil, stringPtrLocal(fmt.Sprintf("exec cell %s not found", cellID.String()))), startedAt)
		outcome := MissingCell(response)
		return &outcome, nil
	}
	if previous != nil && previous.Status == CellRunning {
		// Rust's `begin_termination` interrupts the runtime and keeps draining until
		// it closes; output the script had already produced must still reach the
		// observer in the terminated event instead of being dropped with the
		// detached observation (#48207).
		r.terminateRunningEngine(cellID.String(), wakeup)
		if refreshed, ok := r.cells.Get(cellID.String()); ok {
			cell = refreshed
		}
	}
	r.releaseEngine(cellID.String())
	r.signal(cellID.String())
	outcome := LiveCell(withHostDuration(runtimeResponseFromCell(cell), startedAt))
	return &outcome, nil
}

// terminationDrainGrace bounds how long a termination waits for the interrupted
// script to publish the output it had already produced. Go's engines stop on
// Interrupt, so the wait is normally instant; the bound keeps termination
// responsive for an engine that does not react.
const terminationDrainGrace = 250 * time.Millisecond

func (r *SessionRuntime) terminateRunningEngine(cellID string, wakeup <-chan struct{}) {
	state := r.engineStateFor(cellID)
	if state == nil {
		return
	}
	if state.engine != nil {
		state.engine.Interrupt(errors.New("code mode cell terminated"))
	}
	select {
	case <-state.wake:
	case <-time.After(terminationDrainGrace):
	}
	// The path that consumes the outcome appends the produced output to the
	// terminated cell and then wakes its waiters; wait for that append so the
	// terminated event carries the output.
	select {
	case <-wakeup:
	case <-time.After(terminationDrainGrace):
	}
	r.drainEngineOutputIntoTerminatedCell(cellID)
}

// withHostDuration stamps Rust's `code_mode_host_duration_ns` (#46288): the
// host's measured time from receiving the request to having its outcome ready.
func withHostDuration(response RuntimeResponse, startedAt time.Time) RuntimeResponse {
	if !startedAt.IsZero() {
		response.CodeModeHostDurationNS = uint64(time.Since(startedAt).Nanoseconds())
	}
	return response
}

func (r *SessionRuntime) Shutdown() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.closed = true
	for cellID, wakeup := range r.wakeups {
		delete(r.wakeups, cellID)
		close(wakeup)
	}
	r.engines = map[string]*engineState{}
	r.mu.Unlock()
}

type engineOutcome struct {
	result  *EngineResult
	execErr error
}

// engineState is one running script. Rust's cell actor keeps the observer
// attached through termination so queued output reaches the terminated event;
// Go's equivalent queue is the published outcome, which the terminating caller
// and the completion path each consume at most once (#48207).
type engineState struct {
	engine  Engine
	wake    chan struct{}
	outcome *engineOutcome
}

func (r *SessionRuntime) registerEngine(cellID string, engine Engine) *engineState {
	state := &engineState{engine: engine, wake: make(chan struct{})}
	r.mu.Lock()
	if r.engines == nil {
		r.engines = map[string]*engineState{}
	}
	r.engines[cellID] = state
	r.mu.Unlock()
	return state
}

func (r *SessionRuntime) publishEngineOutcome(cellID string, outcome engineOutcome) {
	r.mu.Lock()
	if state := r.engines[cellID]; state != nil {
		cloned := outcome
		state.outcome = &cloned
		close(state.wake)
	}
	r.mu.Unlock()
}

// takeEngineOutcome consumes the published outcome so exactly one path delivers
// it, whether that path is the terminating caller or the completion goroutine.
func (r *SessionRuntime) takeEngineOutcome(cellID string) *engineOutcome {
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.engines[cellID]
	if state == nil || state.outcome == nil {
		return nil
	}
	outcome := state.outcome
	state.outcome = nil
	return outcome
}

func (r *SessionRuntime) releaseEngine(cellID string) {
	r.mu.Lock()
	delete(r.engines, cellID)
	r.mu.Unlock()
}

func (r *SessionRuntime) engineStateFor(cellID string) *engineState {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.engines[cellID]
}

// drainEngineOutputIntoTerminatedCell moves an already-published outcome onto a
// terminated cell: Rust's termination disables yielding and ignores its queued
// started/pending/yield events so the output produced so far is delivered in the
// terminated event instead of being dropped.
func (r *SessionRuntime) drainEngineOutputIntoTerminatedCell(cellID string) {
	outcome := r.takeEngineOutcome(cellID)
	if outcome == nil {
		return
	}
	if output := contentItemsOutput(engineOutcomeItems(*outcome)); output != "" {
		_, _ = r.cells.AppendTerminatedOutput(cellID, output)
	}
}

func engineOutcomeItems(outcome engineOutcome) []ContentItem {
	if outcome.result == nil {
		return nil
	}
	return outcome.result.ContentItems
}

// contentItemsOutput joins the text content items the way cell output is stored.
func contentItemsOutput(items []ContentItem) string {
	output := ""
	for _, item := range items {
		if item.Type == "input_text" {
			output += item.Text
		}
	}
	return output
}

// applyEngineOutcome records the finished script's output on the cell.
func (r *SessionRuntime) applyEngineOutcome(cellID CellID, outcome engineOutcome) ([]ContentItem, error, error) {
	items := []ContentItem{}
	if outcome.result != nil {
		items = outcome.result.ContentItems
	}
	output := contentItemsOutput(items)
	if output != "" {
		if _, err := r.cells.AppendOutput(cellID.String(), output); err != nil {
			// The script finished while its cell was being terminated: keep the
			// produced output on the terminated event (Rust #48207).
			cell, ok := r.cells.Get(cellID.String())
			if !ok || cell.Status != CellTerminated {
				return nil, err, outcome.execErr
			}
			if _, appendErr := r.cells.AppendTerminatedOutput(cellID.String(), output); appendErr != nil {
				return nil, appendErr, outcome.execErr
			}
		}
	}
	return items, nil, outcome.execErr
}

// finishCellFromEngine completes a yielded cell once its script finishes, unless
// the cell was terminated or completed first.
func (r *SessionRuntime) finishCellFromEngine(cellID CellID, engineWake <-chan struct{}, wakeup <-chan struct{}) {
	select {
	case <-engineWake:
		outcome := r.takeEngineOutcome(cellID.String())
		if outcome == nil {
			return
		}
		if _, err := r.completeCellFromEngine(cellID, *outcome, time.Now()); err != nil {
			return
		}
	case <-wakeup:
		// The cell was terminated or completed first. Any output the script had
		// already produced still belongs to its terminated event (Rust #48207).
		r.drainEngineOutputIntoTerminatedCell(cellID.String())
		r.releaseEngine(cellID.String())
		return
	}
	r.signal(cellID.String())
}

// completeCellFromEngine records a finished script on its cell. A cell that was
// terminated while the script finished keeps its terminated status, with the
// produced output already attached (Rust #48207).
func (r *SessionRuntime) completeCellFromEngine(cellID CellID, outcome engineOutcome, startedAt time.Time) (RuntimeResponse, error) {
	items, appendErr, execErr := r.applyEngineOutcome(cellID, outcome)
	if appendErr != nil {
		return RuntimeResponse{}, appendErr
	}
	r.releaseEngine(cellID.String())
	if cell, ok := r.cells.Get(cellID.String()); ok && cell.Status != CellRunning {
		r.signal(cellID.String())
		return withHostDuration(runtimeResponseFromCell(cell), startedAt), nil
	}
	if _, err := r.cells.Complete(cellID.String(), "", execErr); err != nil {
		return RuntimeResponse{}, err
	}
	r.signal(cellID.String())
	return withHostDuration(Result(cellID, cloneContentItems(items), errorText(execErr)), startedAt), nil
}

func (r *SessionRuntime) wakeup(cellID string) <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	if wakeup := r.wakeups[cellID]; wakeup != nil {
		return wakeup
	}
	wakeup := make(chan struct{})
	r.wakeups[cellID] = wakeup
	return wakeup
}

func (r *SessionRuntime) signal(cellID string) {
	r.mu.Lock()
	wakeup := r.wakeups[cellID]
	if wakeup != nil {
		delete(r.wakeups, cellID)
	}
	r.mu.Unlock()
	if wakeup != nil {
		close(wakeup)
	}
}

func runtimeResponseFromCell(cell *Cell) RuntimeResponse {
	if cell == nil {
		return Result("", nil, stringPtrLocal("exec cell not found"))
	}
	items := []ContentItem{}
	if cell.Output != "" {
		items = append(items, InputText(cell.Output))
	}
	cellID := NewCellID(cell.ID)
	switch cell.Status {
	case CellRunning:
		return Yielded(cellID, items)
	case CellTerminated:
		return Terminated(cellID, items)
	case CellFailed:
		return Result(cellID, items, stringPtrLocal(cell.Error))
	default:
		return Result(cellID, items, nil)
	}
}

func errorText(err error) *string {
	if err == nil {
		return nil
	}
	value := err.Error()
	return &value
}

func stringPtrLocal(value string) *string {
	return &value
}
