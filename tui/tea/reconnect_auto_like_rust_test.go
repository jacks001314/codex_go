package tea

import (
	"errors"
	"strings"
	"testing"
	"time"

	codextui "codex_go/tui"
)

// withInstantAutoReconnect makes the scheduled automatic attempt fire
// immediately so the tests do not wait for the real backoff.
func withInstantAutoReconnect(t *testing.T) {
	t.Helper()
	saved := reconnectAutoDelays
	reconnectAutoDelays = []time.Duration{0}
	t.Cleanup(func() { reconnectAutoDelays = saved })
}

// TestAutoReconnectRejoinsConversationLikeRust mirrors Rust #63599 plus
// app/reconnect.rs: a dropped transport reconnects on its own, keeping the
// draft, and clears the offline state when the resume succeeds.
func TestAutoReconnectRejoinsConversationLikeRust(t *testing.T) {
	withInstantAutoReconnect(t)
	var selections []codextui.SessionSelection
	model := disconnectedRetryModel(t, func(selection codextui.SessionSelection) (SessionResumeResponse, error) {
		selections = append(selections, selection)
		return SessionResumeResponse{}, nil
	})
	if !model.reconnectAutoDeadline.After(time.Now()) {
		t.Fatalf("disconnect did not start an automatic reconnect budget: %v", model.reconnectAutoDeadline)
	}
	tick := model.scheduleAutoReconnect()
	if tick == nil {
		t.Fatal("no automatic attempt was scheduled")
	}

	updated, attempt := model.Update(tick())
	model = updated.(*Model)
	if attempt == nil {
		t.Fatal("the scheduled attempt did not start a resume")
	}
	if model.reconnectAttemptThreadID != "thread-a" || !model.retryActivityActive {
		t.Fatalf("automatic attempt not marked running: %#v", model.reconnectAttemptThreadID)
	}
	if model.composer.Value() != "unsent draft" {
		t.Fatalf("draft changed during automatic reconnect: %q", model.composer.Value())
	}

	updated, _ = model.Update(attempt())
	model = updated.(*Model)
	if len(selections) != 1 || selections[0].Target.ThreadID != "thread-a" {
		t.Fatalf("resume selections = %#v", selections)
	}
	if model.appServerDisconnected || model.reconnectFailed {
		t.Fatalf("successful automatic reconnect kept the offline state")
	}
	if !model.reconnectAutoDeadline.IsZero() || model.reconnectAutoAttempts != 0 {
		t.Fatalf("successful automatic reconnect did not reset the loop: deadline=%v attempts=%d",
			model.reconnectAutoDeadline, model.reconnectAutoAttempts)
	}
}

// TestAutoReconnectRetriesUntilBudgetLikeRust mirrors Rust reconnect(): a
// failed attempt inside the budget schedules the next one while Ctrl+R stays
// available, and the draft survives.
func TestAutoReconnectRetriesUntilBudgetLikeRust(t *testing.T) {
	withInstantAutoReconnect(t)
	model := disconnectedRetryModel(t, func(codextui.SessionSelection) (SessionResumeResponse, error) {
		return SessionResumeResponse{}, errors.New("server still unreachable")
	})
	tick := model.scheduleAutoReconnect()
	updated, attempt := model.Update(tick())
	model = updated.(*Model)
	updated, next := model.Update(attempt())
	model = updated.(*Model)
	if next == nil {
		t.Fatal("failed automatic attempt did not schedule the next one")
	}
	if !model.reconnectFailed || !model.canRetryReconnect() {
		t.Fatalf("failed automatic attempt did not offer Ctrl+R")
	}
	if !strings.Contains(model.notice, "automatic") {
		t.Fatalf("retry notice = %q, want it to mention automatic retries", model.notice)
	}
	if model.footerHintOverride != "ctrl+r reconnect · ctrl+c quit" {
		t.Fatalf("retry footer = %q", model.footerHintOverride)
	}
	if model.composer.Value() != "unsent draft" {
		t.Fatalf("draft changed during automatic retries: %q", model.composer.Value())
	}
}

// TestAutoReconnectTimeoutHandsBackCtrlRLikeRust mirrors Rust's bounded
// reconnect: once the shared budget is spent the loop stops and the user gets
// the Ctrl+R retry instead.
func TestAutoReconnectTimeoutHandsBackCtrlRLikeRust(t *testing.T) {
	model := disconnectedRetryModel(t, func(codextui.SessionSelection) (SessionResumeResponse, error) {
		return SessionResumeResponse{}, nil
	})
	model.reconnectAutoDeadline = time.Now().Add(-time.Second)
	generation := model.reconnectAutoGeneration
	if cmd := model.applyReconnectAutoAttempt(ReconnectAutoAttemptMsg{ThreadID: "thread-a", Generation: generation}); cmd != nil {
		t.Fatal("expired automatic budget started another attempt")
	}
	if !strings.Contains(model.notice, "Automatic reconnect timed out") {
		t.Fatalf("timeout notice = %q", model.notice)
	}
	if !model.canRetryReconnect() {
		t.Fatal("timeout did not hand the conversation back to Ctrl+R")
	}
	if model.retryActivityActive {
		t.Fatal("timeout kept the running attempt row")
	}
	if model.composer.Value() != "unsent draft" {
		t.Fatalf("timeout lost the draft: %q", model.composer.Value())
	}
}

// TestReconnectAttemptShowsElapsedTimerLikeRust mirrors Rust #63599: the
// reconnect row carries an elapsed timer only while the attempt is running.
func TestReconnectAttemptShowsElapsedTimerLikeRust(t *testing.T) {
	model := disconnectedRetryModel(t, func(codextui.SessionSelection) (SessionResumeResponse, error) {
		return SessionResumeResponse{}, nil
	})
	model.retryActivityActive = true
	model.retryActivityMessage = "Reconnecting to server\u2026"
	now := time.Now()
	model.reconnectAttemptStarted = now.Add(-5 * time.Second)
	model.now = func() time.Time { return now }
	if got := model.reconnectActivityText(model.retryActivityMessage); got != "Reconnecting to server\u2026 (5s)" {
		t.Fatalf("attempt row = %q, want the elapsed timer", got)
	}

	model.reconnectAttemptStarted = time.Time{}
	if got := model.reconnectActivityText(model.retryActivityMessage); got != "Reconnecting to server\u2026" {
		t.Fatalf("idle retry row = %q, want no timer", got)
	}
	model.reconnectAttemptStarted = now.Add(-5 * time.Second)
	model.retryActivityActive = false
	if got := model.reconnectActivityText("Compacting context"); got != "Compacting context" {
		t.Fatalf("non-reconnect row = %q, want no timer", got)
	}
}
