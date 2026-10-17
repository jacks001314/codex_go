package telemetry

import (
	"context"
	"time"

	"codex_go/tracehook"
)

type tracehookSpan struct {
	span *Span
}

func (s tracehookSpan) AddEvent(name string, attributes map[string]string) {
	if s.span != nil {
		s.span.AddEvent(name, attributes, time.Time{})
	}
}

func (s tracehookSpan) SetAttribute(key string, value string) {
	if s.span != nil {
		s.span.SetAttribute(key, value)
	}
}

func (s tracehookSpan) End() {
	if s.span != nil {
		s.span.End()
	}
}

func (s tracehookSpan) StartChildSpan(name string, attributes map[string]string) tracehook.Span {
	if s.span == nil {
		return nil
	}
	child := s.span.StartChildSpan(name, attributes)
	if child == nil {
		return nil
	}
	return tracehookSpan{span: child}
}

func withTracehookSpan(ctx context.Context, span *Span) context.Context {
	if span == nil {
		return ctx
	}
	return tracehook.WithSpan(ctx, tracehookSpan{span: span})
}
