package tool

import (
	"context"
	"sync"
	"testing"
	"time"
)

type interruptRecordingSession struct {
	mu       sync.Mutex
	executed []string
	waited   []string
	ended    []string
	closed   bool
	cellID   string
}

func (s *interruptRecordingSession) Execute(ctx context.Context, request CodeModeRemoteExecuteRequest) (CodeModeRemoteResponse, error) {
	s.mu.Lock()
	if s.cellID == "" {
		s.cellID = "cell-1"
	}
	s.executed = append(s.executed, s.cellID)
	cellID := s.cellID
	s.mu.Unlock()
	return CodeModeRemoteResponse{CellID: cellID, State: "yielded"}, nil
}

func (s *interruptRecordingSession) Wait(ctx context.Context, cellID string, ms uint64) (CodeModeRemoteResponse, error) {
	s.mu.Lock()
	s.waited = append(s.waited, cellID)
	s.mu.Unlock()
	return CodeModeRemoteResponse{}, nil
}

func (s *interruptRecordingSession) Terminate(ctx context.Context, cellID string) (CodeModeRemoteResponse, error) {
	s.mu.Lock()
	s.ended = append(s.ended, cellID)
	s.mu.Unlock()
	return CodeModeRemoteResponse{}, nil
}

func (s *interruptRecordingSession) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return nil
}

type interruptRecordingProvider struct {
	session *interruptRecordingSession
}

func (p *interruptRecordingProvider) NewSession(delegate CodeModeRemoteDelegate) CodeModeRemoteSession {
	return p.session
}

func TestCodeModeRuntimeInterruptActiveCellsKeepsSessionAlive(t *testing.T) {
	session := &interruptRecordingSession{}
	provider := &interruptRecordingProvider{session: session}
	runtime := NewCodeModeRuntime(provider, false)

	// Start an exec cell that stays alive.
	exec, _ := runtime.Executors(NewRegistry())
	invocation := &Invocation{
		ToolName: PlainName(CodeModeExecToolName),
		CallID:   "call-1",
		Payload:  Payload{Kind: PayloadCustom, Input: "yield_control()"},
	}
	output, err := exec.Execute(context.Background(), invocation)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output == nil || !output.Success {
		t.Fatalf("Execute() output = %+v, want success", output)
	}
	cellID, _ := output.Data["cell_id"].(string)
	if cellID == "" {
		t.Fatalf("cell_id = %q, want non-empty", cellID)
	}

	runtime.InterruptActiveCells()

	session.mu.Lock()
	ended := append([]string(nil), session.ended...)
	closed := session.closed
	session.mu.Unlock()
	if len(ended) == 0 {
		t.Fatalf("no remote cells terminated after InterruptActiveCells")
	}
	if closed {
		t.Fatalf("session closed after InterruptActiveCells, want kept alive")
	}
}

func TestCodeModeRuntimeInterruptLocalCellsCancels(t *testing.T) {
	runtime := NewCodeModeRuntime(nil, false)
	exec, _ := runtime.Executors(NewRegistry())
	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		close(started)
		defer close(done)
		_, _ = exec.Execute(context.Background(), &Invocation{
			ToolName: PlainName(CodeModeExecToolName),
			CallID:   "call-2",
			Payload:  Payload{Kind: PayloadCustom, Input: "yield_control()"},
		})
	}()
	<-started
	time.Sleep(50 * time.Millisecond)
	runtime.InterruptActiveCells()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("exec did not return after InterruptActiveCells")
	}
}
