package tea

// Rust parity: codex-rs/tui/src/app/reconnect.rs (#63599, #63211). A dropped
// app-server transport reconnects automatically inside a shared budget before
// the conversation falls back to the Ctrl+R retry.

import (
	"strings"
	"time"

	bubbletea "charm.land/bubbletea/v2"
)

// reconnectAutoBudget is the shared deadline for one automatic reconnect
// sequence (Rust reconnect(): 120 seconds including history hydration).
const reconnectAutoBudget = 120 * time.Second

// reconnectAutoDelays mirrors Rust's [0, 1, 2, 4, 8, ...] retry schedule. Go
// waits before the first attempt so the offline footer and Ctrl+R stay
// available while the transport settles; the last delay repeats until the
// budget runs out.
var reconnectAutoDelays = []time.Duration{
	time.Second,
	2 * time.Second,
	4 * time.Second,
	8 * time.Second,
}

// ReconnectAutoAttemptMsg fires one scheduled automatic reconnect attempt.
type ReconnectAutoAttemptMsg struct {
	ThreadID string
	// Generation invalidates timers scheduled before a manual retry, a
	// successful reconnect or a newer disconnect.
	Generation int
}

// beginAutoReconnect starts the automatic loop for a fresh disconnect and
// returns the first scheduled attempt. It reports nil when this transport,
// backend or conversation has no automatic retry (Rust begin_reconnect).
func (m *Model) beginAutoReconnect() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	m.reconnectAutoDeadline = time.Now().Add(reconnectAutoBudget)
	m.reconnectAutoAttempts = 0
	m.reconnectAutoGeneration++
	return m.scheduleAutoReconnect()
}

// scheduleAutoReconnect queues the next delayed attempt while the shared
// budget lasts and a Ctrl+R retry is still allowed.
func (m *Model) scheduleAutoReconnect() bubbletea.Cmd {
	if m == nil || !m.canRetryReconnect() {
		return nil
	}
	now := time.Now()
	if m.reconnectAutoDeadline.IsZero() {
		m.reconnectAutoDeadline = now.Add(reconnectAutoBudget)
	}
	if !now.Before(m.reconnectAutoDeadline) {
		return nil
	}
	delay := reconnectAutoDelays[len(reconnectAutoDelays)-1]
	if m.reconnectAutoAttempts < len(reconnectAutoDelays) {
		delay = reconnectAutoDelays[m.reconnectAutoAttempts]
	}
	m.reconnectAutoAttempts++
	m.reconnectAutoGeneration++
	generation := m.reconnectAutoGeneration
	threadID := strings.TrimSpace(m.State.ThreadID)
	return bubbletea.Tick(delay, func(time.Time) bubbletea.Msg {
		return ReconnectAutoAttemptMsg{ThreadID: threadID, Generation: generation}
	})
}

// applyReconnectAutoAttempt starts the scheduled attempt, or reports the
// timeout that hands control back to Ctrl+R once the budget is spent.
func (m *Model) applyReconnectAutoAttempt(msg ReconnectAutoAttemptMsg) bubbletea.Cmd {
	if m == nil || msg.Generation != m.reconnectAutoGeneration {
		return nil
	}
	threadID := strings.TrimSpace(msg.ThreadID)
	if threadID == "" || threadID != strings.TrimSpace(m.State.ThreadID) {
		return nil
	}
	if !m.canRetryReconnect() {
		return nil
	}
	if !time.Now().Before(m.reconnectAutoDeadline) {
		m.showAutoReconnectTimeout()
		return nil
	}
	return m.startReconnectAttempt()
}

// showAutoReconnectTimeout ends the automatic loop and hands the conversation
// back to the Ctrl+R retry with the draft untouched.
func (m *Model) showAutoReconnectTimeout() {
	if m == nil {
		return
	}
	m.resetReconnectAuto()
	m.reconnectFailed = true
	m.clearRetryActivity()
	m.setDisconnectedFooter(true)
	m.notice = "Automatic reconnect timed out. Press Ctrl+R to retry."
	m.refreshTranscript()
}

// resetReconnectAuto forgets the automatic loop so a later disconnect starts
// with a fresh budget.
func (m *Model) resetReconnectAuto() {
	if m == nil {
		return
	}
	m.reconnectAutoDeadline = time.Time{}
	m.reconnectAutoAttempts = 0
	m.reconnectAutoGeneration++
}
