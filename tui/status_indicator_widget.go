package tui

import "time"

// Rust parity: codex-rs/tui/src/status_indicator_widget.rs.

type StatusIndicatorWidgetState = StatusIndicator

func NewStatusIndicatorWidgetState(now time.Time) *StatusIndicatorWidgetState {
	return NewStatusIndicator(now)
}

func FmtElapsedCompact(elapsedSeconds uint64) string {
	return FormatElapsedCompact(int64(elapsedSeconds))
}
