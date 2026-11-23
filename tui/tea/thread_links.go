package tea

import (
	"strings"

	bubbletea "charm.land/bubbletea/v2"

	mousemsg "codex_go/tui/mousemsg"
)

// Rust parity: codex-rs/tui/src/app/thread_links.rs and
// pager_overlay/transcript.rs (#62332). A saved-conversation link opens inside
// the TUI instead of being handed to the terminal. Bubble Tea v1 has no Super
// modifier, so Ctrl-click carries Rust's Ctrl/Super-click activation, the same
// carrier as the composer-gap links.
func (m *Model) handleThreadLinkClick(msg mousemsg.MouseMsg) (bool, bubbletea.Cmd) {
	if m == nil || !msg.Ctrl ||
		msg.Action != mousemsg.MouseActionPress ||
		msg.Button != mousemsg.MouseButtonLeft {
		return false, nil
	}
	threadID, ok := m.threadLinkAt(msg.X, msg.Y)
	if !ok {
		return false, nil
	}
	current := ""
	if m.State != nil {
		current = strings.TrimSpace(m.State.ThreadID)
	}
	if threadID == current {
		m.notice = "Already showing this session."
		return true, nil
	}
	return true, m.openThreadLink(threadID)
}

// threadLinkAt resolves the saved-conversation link painted under a cell. The
// transcript region carries the conversation rows in the fullscreen view; the
// painted status, footer, and tip regions carry their own app-owned links.
func (m *Model) threadLinkAt(x, y int) (string, bool) {
	if region, ok := m.transcriptSelectionRegionAt(x, y); ok {
		if threadID, ok := threadLinkInRanges(region.links, x-region.x, y-region.y); ok {
			return threadID, true
		}
	}
	for _, region := range m.renderedSelection.regions {
		if region.w <= 0 || region.h <= 0 {
			continue
		}
		if x < region.x || x >= region.x+region.w || y < region.y || y >= region.y+region.h {
			continue
		}
		if threadID, ok := threadLinkInRanges(region.links, x-region.x, y-region.y); ok {
			return threadID, true
		}
	}
	return "", false
}

func threadLinkInRanges(links map[int][]regionThreadLink, column, row int) (string, bool) {
	for _, link := range links[row] {
		if column < link.Start || column >= link.End {
			continue
		}
		return link.ThreadID, true
	}
	return "", false
}

// openThreadLink attaches the linked conversation through the same switch-agent
// path as the agents overview. A cached archived conversation reopens frozen for
// reading (Rust show_read_only_thread), so following a link never mutates it.
func (m *Model) openThreadLink(threadID string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		m.notice = "This session is no longer available."
		return nil
	}
	if strings.TrimSpace(m.pendingServerProfile) != "" {
		m.addErrorHistoryMessage("Wait for permissions to update before switching tasks.")
		m.refreshTranscript()
		return nil
	}
	return m.applyAgentModalOptionAllowed(threadID, true)
}
