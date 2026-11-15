package appserver

import (
	"context"
	"strings"
	"time"

	"codex_go/config"
	"codex_go/runtimeutil"
)

// hostClockProvider adapts the host-supplied clock to the tool clock surface.
// Rust #61382 made clock reads synchronous: a read never yields or calls back
// into the session, while a sleep still observes turn cancellation.
type hostClockProvider struct {
	provider runtimeutil.TimeProvider
}

// unavailableClockProvider reports why the configured clock cannot be read.
type unavailableClockProvider struct {
	err error
}

func (p unavailableClockProvider) CurrentTime(context.Context, string) (time.Time, error) {
	return time.Time{}, p.err
}

func (p unavailableClockProvider) Sleep(context.Context, string, time.Duration) error {
	return p.err
}

func (p hostClockProvider) CurrentTime(ctx context.Context, threadID string) (time.Time, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return time.Time{}, err
		}
	}
	return p.provider.CurrentTime(strings.TrimSpace(threadID))
}

func (p hostClockProvider) Sleep(ctx context.Context, threadID string, duration time.Duration) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if duration <= 0 {
		return nil
	}
	done := make(chan error, 1)
	go func() {
		done <- p.provider.Sleep(strings.TrimSpace(threadID), duration)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-done:
		return err
	}
}

// resolveTimeProviderForConfig mirrors Rust's resolve_time_provider after
// #61382: the system clock is always available, while an external clock
// requires the host to install a provider.
func (r *RuntimeRouter) resolveTimeProviderForConfig(cfg *config.Config) (runtimeutil.TimeProvider, error) {
	source := runtimeutil.TimeSourceSystem
	if cfg != nil {
		if reminder := cfg.CurrentTimeReminder(); reminder != nil && reminder.ClockSource == config.CurrentTimeSourceExternal {
			source = runtimeutil.TimeSourceExternal
		}
	}
	return runtimeutil.ResolveTimeProvider(source, r.externalTimeProvider())
}

// currentTimeForConfig reads the configured clock synchronously for the thread.
func (r *RuntimeRouter) currentTimeForConfig(cfg *config.Config, threadID string) (time.Time, error) {
	provider, err := r.resolveTimeProviderForConfig(cfg)
	if err != nil {
		return time.Time{}, err
	}
	return provider.CurrentTime(strings.TrimSpace(threadID))
}

// externalTimeProvider returns the host-provided external clock, if any.
func (r *RuntimeRouter) externalTimeProvider() runtimeutil.TimeProvider {
	if r == nil {
		return nil
	}
	r.timeProviderMu.RLock()
	defer r.timeProviderMu.RUnlock()
	return r.timeProvider
}

// SetTimeProvider installs the host clock used when the configuration selects
// features.current_time_reminder.clock_source = "external" (Rust #61382).
func (r *RuntimeRouter) SetTimeProvider(provider runtimeutil.TimeProvider) {
	if r == nil {
		return
	}
	r.timeProviderMu.Lock()
	defer r.timeProviderMu.Unlock()
	r.timeProvider = provider
}
