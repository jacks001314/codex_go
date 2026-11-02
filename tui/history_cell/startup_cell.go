package historycell

// IsStartupCell mirrors Rust's empty_state_animation::is_startup_cell: only the
// cells that announce the session may coexist with the empty-state blossom.
// Anything else latches the animation off for the rest of the thread.
func IsStartupCell(cell HistoryCell) bool {
	switch typed := cell.(type) {
	case SessionHeaderHistoryCell:
		return true
	case *SessionHeaderHistoryCell:
		return typed != nil
	case SessionInfoCell:
		return true
	case *SessionInfoCell:
		return typed != nil
	case StartupWarningsCell:
		return true
	case *StartupWarningsCell:
		return typed != nil
	case DeprecationNoticeCell:
		return true
	case *DeprecationNoticeCell:
		return typed != nil
	case UpdateAvailableHistoryCell:
		return true
	case *UpdateAvailableHistoryCell:
		return typed != nil
	case WarningEventCell:
		return typed.ServerVersionNotice
	case *WarningEventCell:
		return typed != nil && typed.ServerVersionNotice
	default:
		return false
	}
}
