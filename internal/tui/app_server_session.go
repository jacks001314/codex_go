package tui

type AppServerSessionState struct {
	ThreadID string
	Remote   bool
}

func NewAppServerSessionState(threadID string, remote bool) AppServerSessionState {
	return AppServerSessionState{ThreadID: threadID, Remote: remote}
}
