package codemode

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
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
}

func NewSessionRuntime() *SessionRuntime {
	return &SessionRuntime{
		cells:   NewCellStore(),
		nextID:  1,
		wakeups: map[string]chan struct{}{},
	}
}

func (r *SessionRuntime) Cells() *CellStore {
	if r == nil {
		return nil
	}
	return r.cells
}

func (r *SessionRuntime) Execute(ctx context.Context, request *ExecuteRequest) (*StartedCell, error) {
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
	initialText, finalText, shouldYield := renderLocalExecution(request.Source)
	if initialText != "" {
		if _, err := r.cells.AppendOutput(cellID.String(), initialText); err != nil {
			return nil, err
		}
	}
	yieldMS := ProtocolDefaultExecYieldTimeMS
	if request.YieldTimeMS != nil {
		yieldMS = *request.YieldTimeMS
	}
	if shouldYield || yieldMS == 0 {
		go r.completeLater(ctx, cellID, finalText, wakeup)
		return &StartedCell{
			CellID:          cellID,
			InitialResponse: Yielded(cellID, []ContentItem{InputText(initialText)}),
		}, nil
	}
	if _, err := r.cells.Complete(cellID.String(), finalText, nil); err != nil {
		return nil, err
	}
	r.signal(cellID.String())
	return &StartedCell{
		CellID:          cellID,
		InitialResponse: Result(cellID, []ContentItem{InputText(initialText + finalText)}, nil),
	}, nil
}

func (r *SessionRuntime) Wait(ctx context.Context, request *WaitRequest) (*WaitOutcome, error) {
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
		outcome := LiveCell(runtimeResponseFromCell(cell))
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
	}
	cell, ok := r.cells.Get(cellID)
	if !ok {
		response := Result(request.CellID, nil, stringPtrLocal(fmt.Sprintf("exec cell %s not found", cellID)))
		outcome := MissingCell(response)
		return &outcome, nil
	}
	outcome := LiveCell(runtimeResponseFromCell(cell))
	return &outcome, nil
}

func (r *SessionRuntime) Terminate(cellID CellID) (*WaitOutcome, error) {
	if r == nil {
		return nil, fmt.Errorf("code mode session runtime is nil")
	}
	cell, err := r.cells.Terminate(cellID.String())
	if err != nil {
		response := Result(cellID, nil, stringPtrLocal(fmt.Sprintf("exec cell %s not found", cellID.String())))
		outcome := MissingCell(response)
		return &outcome, nil
	}
	r.signal(cellID.String())
	outcome := LiveCell(runtimeResponseFromCell(cell))
	return &outcome, nil
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

func (r *SessionRuntime) completeLater(ctx context.Context, cellID CellID, output string, wakeup <-chan struct{}) {
	select {
	case <-ctx.Done():
		_, _ = r.cells.Complete(cellID.String(), "", ctx.Err())
	case <-wakeup:
		return
	case <-time.After(10 * time.Millisecond):
		_, _ = r.cells.Complete(cellID.String(), output, nil)
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

func renderLocalExecution(source string) (string, string, bool) {
	request := ParseExecRequest(source)
	body := strings.TrimSpace(request.Source)
	if body == "" {
		return "", "", false
	}
	shouldYield := strings.Contains(body, "await") || strings.Contains(body, "setTimeout") || strings.Contains(request.Pragma, "yield")
	text := extractTextArgument(body)
	if text == "" {
		text = body
	}
	if shouldYield {
		return text, "", true
	}
	return text, "", false
}

func extractTextArgument(source string) string {
	for _, marker := range []string{"text(", "console.log(", "notify("} {
		index := strings.Index(source, marker)
		if index < 0 {
			continue
		}
		start := index + len(marker)
		if start >= len(source) {
			continue
		}
		quote := source[start]
		if quote != '\'' && quote != '"' && quote != '`' {
			continue
		}
		end := strings.IndexByte(source[start+1:], quote)
		if end < 0 {
			continue
		}
		return source[start+1 : start+1+end]
	}
	return ""
}

func stringPtrLocal(value string) *string {
	return &value
}
