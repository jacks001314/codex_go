package tool

import (
	"context"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type countingSleepProvider struct {
	calls atomic.Int64
}

func (p *countingSleepProvider) CurrentTime(context.Context, string) (time.Time, error) {
	return time.Time{}, nil
}

func (p *countingSleepProvider) Sleep(ctx context.Context, _ string, duration time.Duration) error {
	p.calls.Add(1)
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func sleepInvocation(durationMS int) *Invocation {
	return &Invocation{
		CallID:   "call-sleep",
		ToolName: NamespacedName(clockToolNamespace, "sleep"),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"duration_ms":` + strconv.Itoa(durationMS) + `}`},
	}
}

// Rust #55644: an injection that was already queued when the sleep begins ends
// the sleep immediately, without consulting the clock provider.
func TestClockSleepInterruptedByPendingInjectionLikeRust(t *testing.T) {
	signal := NewYieldSignal()
	signal.Cancel()
	provider := &countingSleepProvider{}
	handler := NewClockSleepHandler(provider, "thread-1")
	output, err := handler.Execute(WithStepActivity(context.Background(), signal), sleepInvocation(3_600_000))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(output.Body, "Sleep interrupted by new input.") {
		t.Fatalf("sleep body = %q, want the interrupted notice", output.Body)
	}
	if provider.calls.Load() != 0 {
		t.Fatalf("clock provider slept %d times for an interrupted sleep, want 0", provider.calls.Load())
	}
}

// A live injection that arrives while the tool is sleeping ends the wait.
func TestClockSleepInterruptedByLiveActivityLikeRust(t *testing.T) {
	signal := NewYieldSignal()
	handler := NewClockSleepHandler(systemClockProvider{}, "thread-1")
	type sleepResult struct {
		output *Output
		err    error
	}
	done := make(chan sleepResult, 1)
	go func() {
		output, err := handler.Execute(WithStepActivity(context.Background(), signal), sleepInvocation(3_600_000))
		done <- sleepResult{output: output, err: err}
	}()
	time.Sleep(20 * time.Millisecond)
	signal.Cancel()
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("Execute() error = %v", result.err)
		}
		if result.output == nil || !strings.Contains(result.output.Body, "Sleep interrupted by new input.") {
			t.Fatalf("sleep output = %#v, want the interrupted notice", result.output)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("sleep did not end after the activity signal fired")
	}
}

// Without queued input the sleep still reports completion.
func TestClockSleepCompletesWithoutActivityLikeRust(t *testing.T) {
	signal := NewYieldSignal()
	handler := NewClockSleepHandler(systemClockProvider{}, "thread-1")
	output, err := handler.Execute(WithStepActivity(context.Background(), signal), sleepInvocation(1))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(output.Body, "Sleep completed.") || strings.Contains(output.Body, "interrupted") {
		t.Fatalf("sleep body = %q, want the completed notice", output.Body)
	}
}
