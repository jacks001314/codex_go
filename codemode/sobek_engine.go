package codemode

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/grafana/sobek"
)

var errEngineExit = errors.New("code mode exit")

// sobekExecutionState records termination for one Execute call. Sobek
// interruption is asynchronous, so callbacks need their own boundary to avoid
// admitting output or replacing an uncatchable interrupt with a JS error.
type sobekExecutionState struct {
	mu               sync.Mutex
	terminationCause error
	terminating      atomic.Bool
	exitRequested    atomic.Bool
}

func (s *sobekExecutionState) requestTermination(cause error) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.terminationCause == nil {
		s.terminationCause = cause
	}
	s.terminating.Store(true)
	s.mu.Unlock()
}

func (s *sobekExecutionState) active() bool {
	return s != nil && !s.terminating.Load() && !s.exitRequested.Load()
}

func (s *sobekExecutionState) appendOutput(items *[]ContentItem, item ContentItem) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active() {
		return
	}
	*items = append(*items, item)
}

func (s *sobekExecutionState) cause() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.terminationCause
}

// SobekEngine owns exactly one Sobek runtime. Execute must not be called
// concurrently; Interrupt is the only method intended for another goroutine.
type SobekEngine struct {
	mu      sync.Mutex
	runtime *sobek.Runtime
	state   *sobekExecutionState
	closed  bool
}

func NewSobekEngine() *SobekEngine { return &SobekEngine{} }

func (e *SobekEngine) Execute(ctx context.Context, request EngineRequest) (*EngineResult, error) {
	if e == nil {
		return nil, fmt.Errorf("sobek engine is nil")
	}
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil, fmt.Errorf("sobek engine is closed")
	}
	if e.runtime != nil {
		e.mu.Unlock()
		return nil, fmt.Errorf("sobek engine is already executing")
	}
	runtime := sobek.New()
	state := &sobekExecutionState{}
	e.runtime = runtime
	e.state = state
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.runtime = nil
		e.state = nil
		e.mu.Unlock()
	}()

	items := make([]ContentItem, 0)
	if err := installSobekHelpers(runtime, &items, state); err != nil {
		return nil, err
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			state.requestTermination(ctx.Err())
			runtime.Interrupt(ctx.Err())
		case <-done:
		}
	}()

	wrapped := "(async () => {\n" + request.Source + "\n})()"
	value, err := runtime.RunString(wrapped)
	if err != nil {
		if state.exitRequested.Load() {
			return &EngineResult{ContentItems: cloneContentItems(items)}, nil
		}
		var interrupted *sobek.InterruptedError
		if errors.As(err, &interrupted) {
			if errors.Is(interrupted, errEngineExit) {
				return &EngineResult{ContentItems: cloneContentItems(items)}, nil
			}
			if cause := state.cause(); cause != nil {
				return nil, cause
			}
			if cause, ok := interrupted.Value().(error); ok {
				return nil, cause
			}
		}
		formatted := formatSobekExecutionError(err, state)
		if state.exitRequested.Load() {
			return &EngineResult{ContentItems: cloneContentItems(items)}, nil
		}
		if cause := state.cause(); cause != nil {
			return nil, cause
		}
		if formatted == nil {
			return nil, fmt.Errorf("javascript execution failed")
		}
		return nil, formatted
	}
	if state.exitRequested.Load() {
		return &EngineResult{ContentItems: cloneContentItems(items)}, nil
	}
	if cause := state.cause(); cause != nil {
		return nil, cause
	}
	if promise, ok := value.Export().(*sobek.Promise); ok {
		switch promise.State() {
		case sobek.PromiseStateRejected:
			text := formatSobekValue(promise.Result(), state)
			if state.exitRequested.Load() {
				return &EngineResult{ContentItems: cloneContentItems(items)}, nil
			}
			if cause := state.cause(); cause != nil {
				return nil, cause
			}
			return nil, fmt.Errorf("javascript execution failed: %s", text)
		case sobek.PromiseStatePending:
			return nil, fmt.Errorf("javascript execution did not settle")
		}
	}
	return &EngineResult{ContentItems: cloneContentItems(items)}, nil
}

func (e *SobekEngine) Interrupt(cause error) {
	if e == nil {
		return
	}
	if cause == nil {
		cause = context.Canceled
	}
	e.mu.Lock()
	runtime := e.runtime
	state := e.state
	if state != nil {
		state.requestTermination(cause)
	}
	e.mu.Unlock()
	if runtime != nil {
		runtime.Interrupt(cause)
	}
}

func (e *SobekEngine) Close() error {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	e.closed = true
	runtime := e.runtime
	state := e.state
	if state != nil {
		state.requestTermination(context.Canceled)
	}
	e.mu.Unlock()
	if runtime != nil {
		runtime.Interrupt(context.Canceled)
	}
	return nil
}

func formatSobekExecutionError(err error, state *sobekExecutionState) (formatted error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if state.terminating.Load() {
				formatted = nil
				return
			}
			panic(recovered)
		}
	}()
	return fmt.Errorf("javascript execution failed: %w", err)
}

func formatSobekValue(value sobek.Value, state *sobekExecutionState) (text string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if state.terminating.Load() {
				text = ""
				return
			}
			panic(recovered)
		}
	}()
	return value.String()
}
