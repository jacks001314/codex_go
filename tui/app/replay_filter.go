package app

import "codex_go/appserver"

const (
	ThreadBufferedEventRequest      = "request"
	ThreadBufferedEventNotification = "notification"
)

type ThreadBufferedEvent struct {
	Type         string
	Request      *ServerRequest
	Notification *ServerEvent
}

type ThreadEventSnapshot struct {
	Session *ThreadSessionState
	// DelegatedTurns lists the turns opened by a realtime voice handoff so a
	// rebuilt thread keeps their private reasoning/commentary hidden (Rust
	// ThreadEventSnapshot::delegated_turns).
	DelegatedTurns []string
	Turns          []appserver.Turn
	Events         []ThreadBufferedEvent
	InputState     *ThreadInputState
}

func ShouldReplay(kind string) bool {
	return kind != "ephemeral"
}

func SnapshotHasPendingInteractiveRequest(snapshot ThreadEventSnapshot) bool {
	for _, event := range snapshot.Events {
		if event.Type != ThreadBufferedEventRequest || event.Request == nil {
			continue
		}
		switch event.Request.Kind {
		case ServerRequestCommandExecutionApproval,
			ServerRequestFileChangeApproval,
			ServerRequestMcpElicitation,
			ServerRequestPermissionsApproval,
			ServerRequestUserInput:
			return true
		}
	}
	return false
}

func EventIsNotice(event ThreadBufferedEvent) bool {
	if event.Type != ThreadBufferedEventNotification || event.Notification == nil {
		return false
	}
	switch event.Notification.Name {
	case ServerNotificationWarning,
		ServerNotificationGuardianWarning,
		ServerNotificationStrictReviewRequired,
		ServerNotificationConfigWarning:
		return true
	default:
		return false
	}
}
