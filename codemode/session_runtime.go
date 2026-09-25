package codemode

import (
	"context"
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
	factory EngineFactory
}

func NewSessionRuntime() *SessionRuntime {
	return &SessionRuntime{
		cells:   NewCellStore(),
		nextID:  1,
		wakeups: map[string]chan struct{}{},
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
	done := make(chan engineOutcome, 1)
	go func() {
		result, execErr := engine.Execute(ctx, EngineRequest{ToolCallID: request.ToolCallID, Source: request.Source, EnabledTools: request.EnabledTools})
		_ = engine.Close()
		done <- engineOutcome{result: result, execErr: execErr}
	}()

	yieldMS := ProtocolDefaultExecYieldTimeMS
	if request.YieldTimeMS != nil {
		yieldMS = *request.YieldTimeMS
	}
	var timer <-chan time.Time
	if yieldMS == 0 {
		// Rust's YieldAfter(0) yields before the script produces anything.
		go r.finishCellFromEngine(cellID, done, wakeup)
		return &StartedCell{
			CellID:          cellID,
			InitialResponse: withHostDuration(Yielded(cellID, nil), startedAt),
		}, nil
	}
	deadline := time.NewTimer(time.Duration(yieldMS) * time.Millisecond)
	defer deadline.Stop()
	timer = deadline.C
	select {
	case outcome := <-done:
		items, appendErr, execErr := r.applyEngineOutcome(cellID, outcome)
		if appendErr != nil {
			return nil, appendErr
		}
		if _, err := r.cells.Complete(cellID.String(), "", execErr); err != nil {
			return nil, err
		}
		r.signal(cellID.String())
		return &StartedCell{
			CellID:          cellID,
			InitialResponse: withHostDuration(Result(cellID, cloneContentItems(items), errorText(execErr)), startedAt),
		}, nil
	case <-timer:
	case <-preempt.Done():
	case <-ctx.Done():
	}
	go r.finishCellFromEngine(cellID, done, wakeup)
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
	cell, err := r.cells.Terminate(cellID.String())
	if err != nil {
		response := withHostDuration(Result(cellID, nil, stringPtrLocal(fmt.Sprintf("exec cell %s not found", cellID.String()))), startedAt)
		outcome := MissingCell(response)
		return &outcome, nil
	}
	r.signal(cellID.String())
	outcome := LiveCell(withHostDuration(runtimeResponseFromCell(cell), startedAt))
	return &outcome, nil
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
	r.mu.Unlock()
}

type engineOutcome struct {
	result  *EngineResult
	execErr error
}

// applyEngineOutcome records the finished script's output on the cell.
func (r *SessionRuntime) applyEngineOutcome(cellID CellID, outcome engineOutcome) ([]ContentItem, error, error) {
	items := []ContentItem{}
	if outcome.result != nil {
		items = outcome.result.ContentItems
	}
	output := ""
	for _, item := range items {
		if item.Type == "input_text" {
			output += item.Text
		}
	}
	if output != "" {
		if _, err := r.cells.AppendOutput(cellID.String(), output); err != nil {
			return nil, err, outcome.execErr
		}
	}
	return items, nil, outcome.execErr
}

// finishCellFromEngine completes a yielded cell once its script finishes, unless
// the cell was terminated or completed first.
func (r *SessionRuntime) finishCellFromEngine(cellID CellID, done <-chan engineOutcome, wakeup <-chan struct{}) {
	select {
	case outcome := <-done:
		_, appendErr, execErr := r.applyEngineOutcome(cellID, outcome)
		if appendErr != nil {
			return
		}
		if _, err := r.cells.Complete(cellID.String(), "", execErr); err != nil {
			return
		}
	case <-wakeup:
		return
	}
	r.signal(cellID.String())
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
