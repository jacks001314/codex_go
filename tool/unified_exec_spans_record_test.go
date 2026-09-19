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
	ended   bool
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
