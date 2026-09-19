package tool

import (
	"sync"
)

// recordedUnifiedExecSpan captures one span's name, the attributes it opened
// with, the attributes set on it, and whether it was ended.
type recordedUnifiedExecSpan struct {
	mu      sync.Mutex
	name    string
	open    map[string]string
	updates map[string]string
	order   []string
	events  []recordedUnifiedExecEvent
	ended   bool
}

type recordedUnifiedExecEvent struct {
	name       string
	attributes map[string]string
}

func (s *recordedUnifiedExecSpan) AddEvent(name string, attributes map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cloned := make(map[string]string, len(attributes))
	for key, value := range attributes {
		cloned[key] = value
	}
	s.events = append(s.events, recordedUnifiedExecEvent{name: name, attributes: cloned})
}

// eventWithName reports the last event recorded on the span under the name.
func (s *recordedUnifiedExecSpan) eventWithName(name string) map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var found map[string]string
	for _, event := range s.events {
		if event.name == name {
			found = event.attributes
		}
	}
	return found
}

func (s *recordedUnifiedExecSpan) SetAttribute(key string, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.updates == nil {
		s.updates = map[string]string{}
	}
	s.updates[key] = value
	s.order = append(s.order, key)
}

func (s *recordedUnifiedExecSpan) End() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ended = true
}

func (s *recordedUnifiedExecSpan) attribute(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value, ok := s.updates[key]; ok {
		return value
	}
	return s.open[key]
}

func (s *recordedUnifiedExecSpan) wasEnded() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ended
}

// recordingUnifiedExecSpanSink records every span the pipeline opens.
type recordingUnifiedExecSpanSink struct {
	mu    sync.Mutex
	spans []*recordedUnifiedExecSpan
}

func (s *recordingUnifiedExecSpanSink) sink() UnifiedExecSpanSink {
	return func(name string, attributes map[string]string) UnifiedExecSpan {
		span := &recordedUnifiedExecSpan{name: name, open: map[string]string{}, updates: map[string]string{}}
		for key, value := range attributes {
			span.open[key] = value
		}
		s.mu.Lock()
		s.spans = append(s.spans, span)
		s.mu.Unlock()
		return span
	}
}

// spanWithName reports the last span recorded under the given name.
func (s *recordingUnifiedExecSpanSink) spanWithName(name string) *recordedUnifiedExecSpan {
	s.mu.Lock()
	defer s.mu.Unlock()
	var found *recordedUnifiedExecSpan
	for _, span := range s.spans {
		if span.name == name {
			found = span
		}
	}
	return found
}
