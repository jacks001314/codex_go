package unified_exec

import (
	"fmt"
	"io"
	"sync"

	"codex_go/sandbox/windowssandbox"
	"codex_go/sandbox/windowssandbox/unified_exec/backends"
)

type LiveSession struct {
	Stdin     io.WriteCloser
	Readers   []io.ReadCloser
	wait      func() (int, error)
	terminate func() error
	close     func() error
	closeOnce sync.Once
	closeErr  error
}

func (s *LiveSession) Wait() (int, error) {
	if s == nil || s.wait == nil {
		return -1, windowssandbox.ErrInvalidRequest
	}
	return s.wait()
}

func (s *LiveSession) Terminate() error {
	if s == nil || s.terminate == nil {
		return nil
	}
	return s.terminate()
}

func (s *LiveSession) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		if s.Stdin != nil {
			_ = s.Stdin.Close()
			s.Stdin = nil
		}
		for _, reader := range s.Readers {
			if reader != nil {
				_ = reader.Close()
			}
		}
		if s.close != nil {
			s.closeErr = s.close()
		}
	})
	return s.closeErr
}

type WindowsSandboxLevel string

const (
	WindowsSandboxLevelLegacy   WindowsSandboxLevel = "legacy"
	WindowsSandboxLevelElevated WindowsSandboxLevel = "elevated"
)

type WindowsSandboxSessionRequest struct {
	Capture             windowssandbox.CaptureRequest
	PTY                 bool
	WindowsSandboxLevel WindowsSandboxLevel
	ProxyEnforced       bool
}

type WindowsSandboxSession struct {
	Backend  backends.BackendKind
	Result   *windowssandbox.CaptureResult
	ExitCode int
}

func SpawnWindowsSandboxSessionForLevel(req *WindowsSandboxSessionRequest) (*WindowsSandboxSession, error) {
	if req == nil {
		return nil, windowssandbox.ErrInvalidRequest
	}
	if req.WindowsSandboxLevel == WindowsSandboxLevelElevated {
		return SpawnWindowsSandboxSessionElevatedForPermissionProfile(req)
	}
	if req.ProxyEnforced {
		return nil, fmt.Errorf("managed networking requires the elevated Windows sandbox backend")
	}
	return SpawnWindowsSandboxSessionLegacy(req)
}

func SpawnWindowsSandboxSessionLegacy(req *WindowsSandboxSessionRequest) (*WindowsSandboxSession, error) {
	if req == nil {
		return nil, windowssandbox.ErrInvalidRequest
	}
	capture := req.Capture
	capture.TTY = req.PTY
	capture.ProxyEnforced = req.ProxyEnforced
	result, err := backends.RunLegacyBackend(&capture)
	if err != nil {
		return nil, err
	}
	return &WindowsSandboxSession{Backend: backends.BackendLegacy, Result: result, ExitCode: result.ExitCode}, nil
}

func SpawnWindowsSandboxSessionElevatedForPermissionProfile(req *WindowsSandboxSessionRequest) (*WindowsSandboxSession, error) {
	if req == nil {
		return nil, windowssandbox.ErrInvalidRequest
	}
	capture := req.Capture
	capture.TTY = req.PTY
	capture.ProxyEnforced = req.ProxyEnforced
	result, err := backends.RunElevatedBackend(&capture)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("elevated backend returned nil result")
	}
	return &WindowsSandboxSession{Backend: backends.BackendElevated, Result: result, ExitCode: result.ExitCode}, nil
}
