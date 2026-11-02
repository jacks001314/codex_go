package tea

import (
	historycell "codex_go/tui/history_cell"
)

// insertDeprecationNotice adds a deprecation notice to the transcript unless an
// identical notice (same summary and details) is already retained (Rust #47065
// App::insert_history_cell). Global deprecations can be delivered again by
// hidden threads, so the check is scoped to the retained transcript: clearing
// history or rebuilding it for another thread lets the notice appear again.
// It reports whether the notice was inserted.
func (m *Model) insertDeprecationNotice(summary, details string) bool {
	if m == nil || m.State == nil {
		return false
	}
	if m.transcriptHasDeprecationNotice(summary, details) {
		return false
	}
	m.applyHistoryCell(historycell.NewDeprecationNotice(summary, details))
	return true
}

// transcriptHasDeprecationNotice reports whether the retained history already
// carries the same deprecation notice.
func (m *Model) transcriptHasDeprecationNotice(summary, details string) bool {
	if m == nil || m.State == nil {
		return false
	}
	for _, message := range m.State.Messages {
		notice := message.DeprecationNotice
		if notice == nil {
			continue
		}
		if notice.Summary == summary && notice.Details == details {
			return true
		}
	}
	return false
}
