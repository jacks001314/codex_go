package tui

type AppEventSender struct {
	Events []AppEvent
}

func (s *AppEventSender) Send(event AppEvent) {
	if s == nil {
		return
	}
	s.Events = append(s.Events, event)
}
