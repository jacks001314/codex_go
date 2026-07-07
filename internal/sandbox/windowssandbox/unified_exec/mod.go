package unified_exec

import (
	"fmt"

	"codex_go/internal/sandbox/windowssandbox"
	"codex_go/internal/sandbox/windowssandbox/unified_exec/backends"
)

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
	if req.ProxyEnforced || req.WindowsSandboxLevel == WindowsSandboxLevelElevated {
		return SpawnWindowsSandboxSessionElevatedForPermissionProfile(req)
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
