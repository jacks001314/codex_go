package tui

type TUIAppState struct {
	ThreadID string
	Running  bool
	Focused  bool
}

func NewTUIAppState(threadID string) TUIAppState {
	return TUIAppState{ThreadID: threadID, Running: true, Focused: true}
}
