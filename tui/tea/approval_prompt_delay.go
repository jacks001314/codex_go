package tea

import (
	"strings"
	"time"

	"codex_go/tui/keymsg"

	bubbletea "charm.land/bubbletea/v2"
)

// approvalPromptTypingIdleDelay mirrors Rust's
// bottom_pane::APPROVAL_PROMPT_TYPING_IDLE_DELAY. Rust lets the composer finish
// the current thought before covering it with a modal; continued typing resets
// the deadline.
const approvalPromptTypingIdleDelay = time.Second

// approvalPromptDelayState owns the composer-idle clock and the single pending
// timer. The token invalidates an older timer when typing moves the deadline.
type approvalPromptDelayState struct {
	lastComposerActivityAt time.Time
	scheduledFor           time.Time
	token                  uint64
}

type approvalPromptDelayMsg struct {
	token uint64
}

func recordsComposerApprovalDelayActivity(msg keymsg.KeyMsg) bool {
	if msg.Alt || msg.Paste {
		return false
	}
	switch msg.Type {
	case keymsg.KeyRunes:
		return len(msg.Runes) > 0
	case keymsg.KeySpace,
		keymsg.KeyBackspace,
		keymsg.KeyDelete,
		keymsg.KeyEnter,
		keymsg.KeyTab,
		keymsg.KeyShiftTab:
		return true
	default:
		return false
	}
}

func (m *Model) recordComposerActivityAt(now time.Time) {
	if m == nil {
		return
	}
	m.approvalPromptDelay.lastComposerActivityAt = now
}

func (m *Model) approvalPromptDelayRemaining(now time.Time) time.Duration {
	if m == nil || m.approvalPromptDelay.lastComposerActivityAt.IsZero() {
		return 0
	}
	showAt := m.approvalPromptDelay.lastComposerActivityAt.Add(approvalPromptTypingIdleDelay)
	if !now.Before(showAt) {
		return 0
	}
	return showAt.Sub(now)
}

func (m *Model) hasDelayedActiveApproval() bool {
	if m == nil || len(m.pendingApprovals) == 0 {
		return false
	}
	active := m.currentThreadID()
	for _, pending := range m.pendingApprovals {
		threadID := strings.TrimSpace(pending.ThreadID)
		if threadID == "" || threadID == active {
			return true
		}
	}
	return false
}

// syncApprovalPromptDelay promotes the oldest active-thread approval once the
// composer has been idle long enough. While the deadline is still in the
// future it arms exactly one Bubble Tea timer; subsequent typing changes the
// deadline, which increments the token and makes the older timer a no-op.
func (m *Model) syncApprovalPromptDelay() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if m.modal != nil || !m.hasDelayedActiveApproval() {
		m.approvalPromptDelay.scheduledFor = time.Time{}
		return nil
	}
	now := m.currentTime()
	remaining := m.approvalPromptDelayRemaining(now)
	if remaining <= 0 {
		m.approvalPromptDelay.scheduledFor = time.Time{}
		return m.openNextThreadApproval()
	}
	showAt := now.Add(remaining)
	if m.approvalPromptDelay.scheduledFor.Equal(showAt) {
		return nil
	}
	m.approvalPromptDelay.scheduledFor = showAt
	m.approvalPromptDelay.token++
	token := m.approvalPromptDelay.token
	return bubbletea.Tick(remaining, func(time.Time) bubbletea.Msg {
		return approvalPromptDelayMsg{token: token}
	})
}

func (m *Model) applyApprovalPromptDelay(msg approvalPromptDelayMsg) bubbletea.Cmd {
	if m == nil || msg.token != m.approvalPromptDelay.token {
		return nil
	}
	m.approvalPromptDelay.scheduledFor = time.Time{}
	return m.syncApprovalPromptDelay()
}
