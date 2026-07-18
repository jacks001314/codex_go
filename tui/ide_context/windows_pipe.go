package idecontext

import (
	"errors"
	"math"
	"runtime"
	"strings"
	"time"
)

const DefaultWindowsPipeName = `\\.\pipe\codex-ipc`

type WindowsPipeConfig struct {
	Name     string
	Deadline time.Time
}

func NewWindowsPipeConfig(name string, timeout time.Duration) WindowsPipeConfig {
	if strings.TrimSpace(name) == "" {
		name = DefaultWindowsPipeName
	}
	return WindowsPipeConfig{
		Name:     name,
		Deadline: time.Now().Add(timeout),
	}
}

func (c WindowsPipeConfig) PipePath() string {
	if strings.TrimSpace(c.Name) == "" {
		return DefaultWindowsPipeName
	}
	return c.Name
}

func (c WindowsPipeConfig) RemainingTimeoutMS(now time.Time) uint32 {
	return RemainingTimeoutMS(c.Deadline, now)
}

func WindowsPipeAvailable() bool {
	return runtime.GOOS == "windows"
}

func RemainingTimeoutMS(deadline time.Time, now time.Time) uint32 {
	if deadline.IsZero() || !now.Before(deadline) {
		return 0
	}
	millis := deadline.Sub(now).Milliseconds()
	if millis < 1 {
		return 1
	}
	if millis > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(millis)
}

func WindowsPipeTimeoutError() error {
	return ErrIDEContextTimedOut
}

func ValidatePipeServerOwner(serverUserID string, currentUserID string) error {
	if strings.TrimSpace(serverUserID) == "" || strings.TrimSpace(currentUserID) == "" {
		return errors.New("IDE context provider owner could not be determined")
	}
	if serverUserID != currentUserID {
		return errors.New("IDE context provider is not owned by the current user")
	}
	return nil
}
