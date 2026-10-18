package tea

import (
	"time"

	bottompane "codex_go/tui/bottom_pane"
)

// Rust parity: codex-rs/tui/src/bottom_pane/chat_composer/agents_navigation.rs
// (#41917). Only local-daemon sessions enable the empty-prompt `Left`
// shortcut. Draft contents, attachments, transient input surfaces, and explicit
// editor/Vim remaps take precedence over navigation.

// agentsNavigationAvailable reports whether a plain Left press should open the
// agents overview instead of moving the composer cursor.
func (m *Model) agentsNavigationAvailable() bool {
	if m == nil || !m.localDaemonSession {
		return false
	}
	if m.modal != nil || m.overlay != nil || m.readOnlyThread || m.windowsSandboxSetupActive {
		return false
	}
	if !m.terminalFocused || !m.composerIsEmpty() || len(m.attachments) > 0 {
		return false
	}
	if m.slashPopup.Active || m.skillPopup.Active || m.mentionPopup != nil {
		return false
	}
	if m.asyncQuestions.Expanded() || m.vimSearchMode {
		return false
	}
	if m.historySearch != nil && m.historySearch.Active {
		return false
	}
	if m.vimPendingOp != "" || m.vimPendingObject != "" {
		return false
	}
	if m.composerPasteBurstActive(m.currentTime()) {
		return false
	}
	return m.agentsNavigationKeyAvailable()
}

// composerPasteBurstActive reports whether the composer is still inside the
// transient window that Rust's PasteBurst::is_active covers: either an observed
// newline burst or a character that arrived within the burst interval.
func (m *Model) composerPasteBurstActive(now time.Time) bool {
	if m == nil || m.disablePasteBurst {
		return false
	}
	if m.composerPasteEnterUntil != nil && !now.After(*m.composerPasteEnterUntil) {
		return true
	}
	return m.composerLastCharAt != nil && now.Sub(*m.composerLastCharAt) <= bottompane.PasteBurstCharInterval
}
