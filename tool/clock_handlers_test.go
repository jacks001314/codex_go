package tool

import (
	"context"
	"errors"
	"testing"
	"time"
)

type failingClockProvider struct{ err error }

func (p failingClockProvider) CurrentTime(context.Context, string) (time.Time, error) {
	return time.Time{}, p.err
}

func (p failingClockProvider) Sleep(context.Context, string, time.Duration) error {
	return p.err
}

// Mirrors Rust #46006: with `nonfatal_clock_read_errors` enabled a stalled clock
// provider yields the model-visible notice, and stays a fatal tool error
// otherwise.
func TestClockHandlersGateFailuresOnNonfatalClockReadErrorsLikeRust(t *testing.T) {
	readErr := errors.New("clock stalled")
	for _, nonfatal := range []bool{false, true} {
		t.Run(map[bool]string{false: "fatal", true: "nonfatal"}[nonfatal], func(t *testing.T) {
			currentTime := NewCurrentTimeHandlerWithOptions(failingClockProvider{err: readErr}, "thread-1", nonfatal)
			_, err := currentTime.Execute(context.Background(), &Invocation{
				CallID:   "call-time",
				ToolName: NamespacedName(clockToolNamespace, currentTimeToolName),
				Payload:  Payload{Kind: PayloadFunction, Arguments: `{}`},
			})
			assertClockToolFailure(t, err, nonfatal, "failed to read current time: clock stalled")

			sleep := NewClockSleepHandlerWithOptions(failingClockProvider{err: readErr}, "thread-1", nonfatal)
			_, err = sleep.Execute(context.Background(), &Invocation{
				CallID:   "call-sleep",
				ToolName: NamespacedName(clockToolNamespace, "sleep"),
				Payload:  Payload{Kind: PayloadFunction, Arguments: `{"duration_ms":1}`},
			})
			assertClockToolFailure(t, err, nonfatal, "failed to sleep: clock stalled")
		})
	}
}

func assertClockToolFailure(t *testing.T, err error, nonfatal bool, fatalMessage string) {
	t.Helper()
	if nonfatal {
		if err == nil {
			t.Fatal("expected the nonfatal clock notice")
		}
		var callErr *FunctionCallError
		if !AsFunctionCallError(err, &callErr) || !callErr.RespondsToModel() {
			t.Fatalf("error = %#v, want a model-visible notice", err)
		}
		if callErr.Message != "failed to read current time" {
			t.Fatalf("message = %q, want the Rust CurrentTimeUnavailable message", callErr.Message)
		}
		return
	}
	if err == nil {
		t.Fatal("expected the fatal clock error")
	}
	var callErr *FunctionCallError
	if !AsFunctionCallError(err, &callErr) || !callErr.IsFatal() {
		t.Fatalf("error = %#v, want a fatal tool error", err)
	}
	if callErr.Message != fatalMessage {
		t.Fatalf("message = %q, want %q", callErr.Message, fatalMessage)
	}
}
