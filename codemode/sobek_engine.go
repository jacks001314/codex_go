package codemode

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/grafana/sobek"
)

var errEngineExit = errors.New("code mode exit")

// SobekEngine owns exactly one Sobek runtime. Execute must not be called
// concurrently; Interrupt is the only method intended for another goroutine.
type SobekEngine struct {
	mu      sync.Mutex
	runtime *sobek.Runtime
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
	e.runtime = runtime
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.runtime = nil
		e.mu.Unlock()
	}()

	items := make([]ContentItem, 0)
	if err := installSobekHelpers(runtime, &items); err != nil {
		return nil, err
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			runtime.Interrupt(ctx.Err())
		case <-done:
		}
	}()

	wrapped := "(async () => {\n" + request.Source + "\n})()"
	value, err := runtime.RunString(wrapped)
	if err != nil {
		var interrupted *sobek.InterruptedError
		if errors.As(err, &interrupted) {
			if errors.Is(interrupted, errEngineExit) {
				return &EngineResult{ContentItems: cloneContentItems(items)}, nil
			}
			if cause, ok := interrupted.Value().(error); ok {
				return nil, cause
			}
		}
		return nil, fmt.Errorf("javascript execution failed: %w", err)
	}
	if promise, ok := value.Export().(*sobek.Promise); ok {
		switch promise.State() {
		case sobek.PromiseStateRejected:
			return nil, fmt.Errorf("javascript execution failed: %s", promise.Result().String())
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
	e.mu.Unlock()
	if runtime != nil {
		runtime.Interrupt(context.Canceled)
	}
	return nil
}
