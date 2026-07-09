package tui

// Rust parity subset: codex-rs/tui/src/tui/event_stream.rs.

type EventBrokerState string

const (
	EventBrokerPaused  EventBrokerState = "paused"
	EventBrokerStart   EventBrokerState = "start"
	EventBrokerRunning EventBrokerState = "running"
)

type EventBroker struct {
	State       EventBrokerState
	ResumeCount int
}

func NewEventBroker() *EventBroker {
	return &EventBroker{State: EventBrokerStart}
}

func (b *EventBroker) PauseEvents() {
	if b != nil {
		b.State = EventBrokerPaused
	}
}

func (b *EventBroker) ResumeEvents() {
	if b != nil {
		b.State = EventBrokerStart
		b.ResumeCount++
	}
}

func (b *EventBroker) ActiveEventSourceAvailable() bool {
	if b == nil || b.State == EventBrokerPaused {
		return false
	}
	if b.State == EventBrokerStart {
		b.State = EventBrokerRunning
	}
	return true
}

type TerminalEventKind string

const (
	TerminalEventKey         TerminalEventKind = "key"
	TerminalEventResize      TerminalEventKind = "resize"
	TerminalEventPaste       TerminalEventKind = "paste"
	TerminalEventFocusGained TerminalEventKind = "focus_gained"
	TerminalEventFocusLost   TerminalEventKind = "focus_lost"
	TerminalEventMouse       TerminalEventKind = "mouse"
)

type TerminalEvent struct {
	Kind  TerminalEventKind
	Key   string
	Paste string
}

type TuiEventKind string

const (
	TuiEventDraw   TuiEventKind = "draw"
	TuiEventKey    TuiEventKind = "key"
	TuiEventResize TuiEventKind = "resize"
	TuiEventPaste  TuiEventKind = "paste"
)

type TuiEvent struct {
	Kind  TuiEventKind
	Key   string
	Paste string
}

type EventStreamState struct {
	Open            bool
	Broker          *EventBroker
	TerminalFocused bool
	PollDrawFirst   bool
	DrawPending     int
	Events          []TerminalEvent
}

func NewEventStreamState(broker *EventBroker) *EventStreamState {
	if broker == nil {
		broker = NewEventBroker()
	}
	return &EventStreamState{Open: true, Broker: broker, TerminalFocused: true}
}

func (s *EventStreamState) PushTerminalEvent(event TerminalEvent) {
	if s != nil {
		s.Events = append(s.Events, event)
	}
}

func (s *EventStreamState) PushDraw() {
	if s != nil {
		s.DrawPending++
	}
}

func (s *EventStreamState) NextEvent() (TuiEvent, bool) {
	if s == nil || !s.Open {
		return TuiEvent{}, false
	}
	drawFirst := s.PollDrawFirst
	s.PollDrawFirst = !s.PollDrawFirst
	if drawFirst {
		if event, ok := s.nextDraw(); ok {
			return event, true
		}
		return s.nextTerminal()
	}
	if event, ok := s.nextTerminal(); ok {
		return event, true
	}
	return s.nextDraw()
}

func (s *EventStreamState) nextDraw() (TuiEvent, bool) {
	if s.DrawPending <= 0 {
		return TuiEvent{}, false
	}
	s.DrawPending--
	return TuiEvent{Kind: TuiEventDraw}, true
}

func (s *EventStreamState) nextTerminal() (TuiEvent, bool) {
	if s.Broker != nil && !s.Broker.ActiveEventSourceAvailable() {
		return TuiEvent{}, false
	}
	for len(s.Events) > 0 {
		event := s.Events[0]
		s.Events = s.Events[1:]
		if mapped, ok := s.MapTerminalEvent(event); ok {
			return mapped, true
		}
	}
	return TuiEvent{}, false
}

func (s *EventStreamState) MapTerminalEvent(event TerminalEvent) (TuiEvent, bool) {
	if s == nil {
		return TuiEvent{}, false
	}
	switch event.Kind {
	case TerminalEventKey:
		return TuiEvent{Kind: TuiEventKey, Key: event.Key}, true
	case TerminalEventResize:
		return TuiEvent{Kind: TuiEventResize}, true
	case TerminalEventPaste:
		return TuiEvent{Kind: TuiEventPaste, Paste: event.Paste}, true
	case TerminalEventFocusGained:
		s.TerminalFocused = true
		return TuiEvent{Kind: TuiEventDraw}, true
	case TerminalEventFocusLost:
		s.TerminalFocused = false
		return TuiEvent{}, false
	default:
		return TuiEvent{}, false
	}
}
