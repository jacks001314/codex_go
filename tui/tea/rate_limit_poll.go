package tea

import (
	"time"

	bubbletea "charm.land/bubbletea/v2"

	"codex_go/tui/chatwidget"
)

// RateLimitSnapshotsReaderFunc reads the full account limit snapshots used to
// decide the next polling cadence (Rust app/rate_limit_refresh.rs, #42372).
type RateLimitSnapshotsReaderFunc func() ([]chatwidget.RateLimitSnapshot, error)

// rateLimitPollTickMsg asks the model to re-read account limits.
type rateLimitPollTickMsg struct{}

// rateLimitPollResultMsg carries one background account-limits read.
type rateLimitPollResultMsg struct {
	// ReadID identifies the read that produced these snapshots so a rolling
	// update that landed meanwhile can mark it stale (Rust #60435).
	ReadID    uint64
	Snapshots []chatwidget.RateLimitSnapshot
	Err       error
}

// rateLimitPollEnabled reports whether a near-exhaustion poll can run: only
// ChatGPT accounts with a snapshot reader poll, matching Rust's
// has_chatgpt_account gate.
func (m *Model) rateLimitPollEnabled() bool {
	return m != nil && m.State != nil && m.State.HasChatGPTAccount && m.rateLimitSnapshotsReader != nil
}

// rateLimitPollInterval is the Rust interval for the current usage level.
func (m *Model) rateLimitPollInterval() time.Duration {
	if m == nil {
		return 60 * time.Second
	}
	return chatwidget.RateLimitRefreshInterval(m.rateLimitSnapshots, m.currentBannerModel())
}

// rateLimitPollTickCmd schedules the next account-limits read.
func (m *Model) rateLimitPollTickCmd() bubbletea.Cmd {
	interval := m.rateLimitPollInterval()
	return bubbletea.Tick(interval, func(time.Time) bubbletea.Msg {
		return rateLimitPollTickMsg{}
	})
}

// rateLimitPollReadCmd performs the read off the update loop.
func (m *Model) rateLimitPollReadCmd() bubbletea.Cmd {
	reader := m.rateLimitSnapshotsReader
	if reader == nil {
		return nil
	}
	// Rust start_usage_notice_read: remember the newest read so a rolling
	// update can retire reads that started before it.
	m.rateLimitReadSeq++
	readID := m.rateLimitReadSeq
	return func() bubbletea.Msg {
		snapshots, err := reader()
		return rateLimitPollResultMsg{ReadID: readID, Snapshots: snapshots, Err: err}
	}
}

// applyRateLimitPollResult feeds every snapshot through the full account usage
// path so banners, Reserve fallback, status rows and switch-prompt recovery
// react exactly as they do to server notifications. A read that started before
// the newest rolling update cannot confirm recovery (Rust #60435). The next
// tick is scheduled from the refreshed usage.
func (m *Model) applyRateLimitPollResult(message rateLimitPollResultMsg) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	staleRead := m.rateLimitStaleThrough != 0 && message.ReadID != 0 && message.ReadID <= m.rateLimitStaleThrough
	var commands []bubbletea.Cmd
	if message.Err == nil {
		for _, snapshot := range message.Snapshots {
			if cmd := m.applyRateLimitSnapshotFromSource(snapshot, rateLimitSnapshotAccountUsage, staleRead); cmd != nil {
				commands = append(commands, cmd)
			}
		}
	}
	if m.rateLimitPollEnabled() {
		commands = append(commands, m.rateLimitPollTickCmd())
	}
	if len(commands) == 0 {
		return nil
	}
	if len(commands) == 1 {
		return commands[0]
	}
	return bubbletea.Batch(commands...)
}
