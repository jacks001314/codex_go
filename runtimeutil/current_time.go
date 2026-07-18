package runtimeutil

import (
	"errors"
	"time"
)

var ErrExternalTimeProviderRequired = errors.New("external current-time provider is required")

type TimeSource string

const (
	TimeSourceSystem   TimeSource = "system"
	TimeSourceExternal TimeSource = "external"
)

type TimeProvider interface {
	CurrentTime(threadID string) (time.Time, error)
	Sleep(threadID string, duration time.Duration) error
}

type SystemTimeProvider struct{}

func (p *SystemTimeProvider) CurrentTime(threadID string) (time.Time, error) {
	return time.Now().UTC(), nil
}

func (p *SystemTimeProvider) Sleep(threadID string, duration time.Duration) error {
	time.Sleep(duration)
	return nil
}

type StaticTimeProvider struct {
	Now time.Time
}

func (p *StaticTimeProvider) CurrentTime(threadID string) (time.Time, error) {
	return p.Now.UTC(), nil
}

func (p *StaticTimeProvider) Sleep(threadID string, duration time.Duration) error {
	return nil
}

func ResolveTimeProvider(source TimeSource, external TimeProvider) (TimeProvider, error) {
	switch source {
	case "", TimeSourceSystem:
		return &SystemTimeProvider{}, nil
	case TimeSourceExternal:
		if external == nil {
			return nil, ErrExternalTimeProviderRequired
		}
		return external, nil
	default:
		return &SystemTimeProvider{}, nil
	}
}
