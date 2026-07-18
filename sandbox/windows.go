package sandbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

var ErrInvalidWindowsSandboxRequest = errors.New("invalid windows sandbox request")

type WorldWritableWarningNotification struct {
	SamplePaths []string `json:"samplePaths"`
	ExtraCount  int      `json:"extraCount"`
	FailedScan  bool     `json:"failedScan"`
}

func (n *WorldWritableWarningNotification) MarshalJSON() ([]byte, error) {
	samplePaths := append([]string(nil), n.SamplePaths...)
	if samplePaths == nil {
		samplePaths = []string{}
	}
	return json.Marshal(struct {
		SamplePaths []string `json:"samplePaths"`
		ExtraCount  int      `json:"extraCount"`
		FailedScan  bool     `json:"failedScan"`
	}{
		SamplePaths: samplePaths,
		ExtraCount:  n.ExtraCount,
		FailedScan:  n.FailedScan,
	})
}

type WindowsSetupMode string

const (
	WindowsSetupElevated   WindowsSetupMode = "elevated"
	WindowsSetupUnelevated WindowsSetupMode = "unelevated"
	WindowsSetupDefault    WindowsSetupMode = "default"
)

type WindowsReadiness string

const (
	WindowsReadinessReady          WindowsReadiness = "ready"
	WindowsReadinessNotConfigured  WindowsReadiness = "notConfigured"
	WindowsReadinessUpdateRequired WindowsReadiness = "updateRequired"
)

type WindowsSetupStartParams struct {
	Mode WindowsSetupMode `json:"mode"`
	CWD  *string          `json:"cwd,omitempty"`
}

func (p *WindowsSetupStartParams) Validate() error {
	if p == nil {
		return fmt.Errorf("%w: params are nil", ErrInvalidWindowsSandboxRequest)
	}
	switch p.Mode {
	case WindowsSetupElevated, WindowsSetupUnelevated, WindowsSetupDefault:
	default:
		return fmt.Errorf("%w: unsupported mode %q", ErrInvalidWindowsSandboxRequest, p.Mode)
	}
	if p.CWD != nil && strings.TrimSpace(*p.CWD) != "" && !filepath.IsAbs(*p.CWD) {
		return fmt.Errorf("%w: cwd must be absolute", ErrInvalidWindowsSandboxRequest)
	}
	if p.Mode == WindowsSetupDefault {
		p.Mode = WindowsSetupUnelevated
	}
	return nil
}

type WindowsSetupStartResponse struct {
	Started bool `json:"started"`
}

type WindowsReadinessResponse struct {
	Status WindowsReadiness `json:"status"`
}

type WindowsSetupCompletedNotification struct {
	Mode    WindowsSetupMode `json:"mode"`
	Success bool             `json:"success"`
	Error   *string          `json:"error"`
}

type WindowsManager struct {
	mu        sync.Mutex
	readiness WindowsReadiness
	active    *WindowsSetupMode
	lastCWD   *string
}

func NewWindowsManager(readiness WindowsReadiness) *WindowsManager {
	if readiness == "" {
		readiness = WindowsReadinessNotConfigured
	}
	return &WindowsManager{readiness: readiness}
}

func (m *WindowsManager) Readiness() *WindowsReadinessResponse {
	m.mu.Lock()
	defer m.mu.Unlock()
	return &WindowsReadinessResponse{Status: m.readiness}
}

func (m *WindowsManager) SetReadiness(readiness WindowsReadiness) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if readiness == "" {
		readiness = WindowsReadinessNotConfigured
	}
	m.readiness = readiness
}

func (m *WindowsManager) StartSetup(params *WindowsSetupStartParams) (*WindowsSetupStartResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if params.Mode == WindowsSetupDefault {
		params.Mode = WindowsSetupUnelevated
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active != nil {
		return &WindowsSetupStartResponse{Started: false}, nil
	}
	mode := params.Mode
	m.active = &mode
	m.lastCWD = cloneWindowsStringPtr(params.CWD)
	return &WindowsSetupStartResponse{Started: true}, nil
}

func (m *WindowsManager) CompleteSetup(success bool, errMessage string) (*WindowsSetupCompletedNotification, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil {
		return nil, fmt.Errorf("%w: setup is not active", ErrInvalidWindowsSandboxRequest)
	}
	mode := *m.active
	m.active = nil
	if success {
		m.readiness = WindowsReadinessReady
	} else if m.readiness == "" {
		m.readiness = WindowsReadinessNotConfigured
	}
	return &WindowsSetupCompletedNotification{
		Mode:    mode,
		Success: success,
		Error:   windowsStringPtrIfNotEmpty(errMessage),
	}, nil
}

func WindowsSandboxWarning(samplePaths []string, failedScan bool, maxSamples int) *WorldWritableWarningNotification {
	if maxSamples <= 0 {
		maxSamples = len(samplePaths)
	}
	extra := 0
	if len(samplePaths) > maxSamples {
		extra = len(samplePaths) - maxSamples
		samplePaths = samplePaths[:maxSamples]
	}
	return &WorldWritableWarningNotification{
		SamplePaths: append([]string(nil), samplePaths...),
		ExtraCount:  extra,
		FailedScan:  failedScan,
	}
}

func DetermineWindowsReadiness(level WindowsSandboxLevel, setupComplete bool) *WindowsReadinessResponse {
	if runtime.GOOS != "windows" {
		return &WindowsReadinessResponse{Status: WindowsReadinessNotConfigured}
	}
	return DetermineWindowsReadinessFromState(level, setupComplete)
}

func DetermineWindowsReadinessFromState(level WindowsSandboxLevel, setupComplete bool) *WindowsReadinessResponse {
	status := WindowsReadinessNotConfigured
	switch level {
	case WindowsSandboxDisabled, "":
		status = WindowsReadinessNotConfigured
	case WindowsSandboxDefault, WindowsSandboxUnelevated:
		status = WindowsReadinessReady
	case WindowsSandboxElevated:
		if setupComplete {
			status = WindowsReadinessReady
		} else {
			status = WindowsReadinessUpdateRequired
		}
	default:
		status = WindowsReadinessNotConfigured
	}
	return &WindowsReadinessResponse{Status: status}
}

func ParseWindowsSetupMode(value string) (WindowsSetupMode, error) {
	switch WindowsSetupMode(strings.TrimSpace(value)) {
	case WindowsSetupElevated:
		return WindowsSetupElevated, nil
	case WindowsSetupUnelevated, WindowsSetupDefault:
		return WindowsSetupUnelevated, nil
	default:
		return "", fmt.Errorf("%w: unsupported mode %q", ErrInvalidWindowsSandboxRequest, value)
	}
}

func cloneWindowsStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func windowsStringPtrIfNotEmpty(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}
