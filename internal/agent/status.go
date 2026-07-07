package agent

type StatusKind string

const (
	StatusPendingInit StatusKind = "pending_init"
	StatusRunning     StatusKind = "running"
	StatusCompleted   StatusKind = "completed"
	StatusInterrupted StatusKind = "interrupted"
	StatusErrored     StatusKind = "errored"
	StatusShutdown    StatusKind = "shutdown"
)

type Status struct {
	Kind    StatusKind
	Message string
}

type Event struct {
	Type             string
	LastAgentMessage string
	Reason           string
	Message          string
}

func StatusFromEvent(event *Event) (*Status, bool) {
	if event == nil {
		return nil, false
	}
	switch event.Type {
	case "turn_started":
		return &Status{Kind: StatusRunning}, true
	case "turn_complete":
		return &Status{Kind: StatusCompleted, Message: event.LastAgentMessage}, true
	case "turn_aborted":
		if event.Reason == "interrupted" || event.Reason == "budget_limited" {
			return &Status{Kind: StatusInterrupted}, true
		}
		return &Status{Kind: StatusErrored, Message: event.Reason}, true
	case "error":
		return &Status{Kind: StatusErrored, Message: event.Message}, true
	case "shutdown_complete":
		return &Status{Kind: StatusShutdown}, true
	default:
		return nil, false
	}
}

func (s *Status) IsFinal() bool {
	if s == nil {
		return false
	}
	return s.Kind != StatusPendingInit && s.Kind != StatusRunning && s.Kind != StatusInterrupted
}
