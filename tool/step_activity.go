package tool

import "context"

// StepActivityContextKey carries a sampling request's input-activity signal into
// tool invocations. Rust #55644: a tool that waits on wall time (`clock.sleep`)
// subscribes to the turn's input queue, so input queued before the wait or while
// it runs ends the wait.
const StepActivityContextKey = "step_activity_signal"

type stepActivityContextKey struct{}

// WithStepActivity attaches a sampling request's activity signal to its step
// context. The signal is created once per model request, so every tool call the
// request issues observes the same token.
func WithStepActivity(ctx context.Context, signal *YieldSignal) context.Context {
	if signal == nil {
		return ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, stepActivityContextKey{}, signal)
}

// StepActivityFromContext reads the step's activity signal.
func StepActivityFromContext(ctx context.Context) *YieldSignal {
	if ctx == nil {
		return nil
	}
	signal, _ := ctx.Value(stepActivityContextKey{}).(*YieldSignal)
	return signal
}

// ActivitySignalFromInvocation reads an activity signal a host placed in the
// invocation context (code-mode hosts build invocation contexts directly).
func ActivitySignalFromInvocation(invocation *Invocation) *YieldSignal {
	if invocation == nil || invocation.Context == nil {
		return nil
	}
	signal, _ := invocation.Context[StepActivityContextKey].(*YieldSignal)
	return signal
}
