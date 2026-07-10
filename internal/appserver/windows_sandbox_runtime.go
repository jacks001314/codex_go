package appserver

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"codex_go/internal/config"
	"codex_go/internal/sandbox"
	"codex_go/internal/sandbox/windowssandbox"
)

type WindowsSandboxSetupRunner func(*WindowsSandboxSetupRuntimeRequest) error

type WindowsSandboxSetupRuntimeRequest struct {
	Mode                sandbox.WindowsSetupMode
	CWD                 string
	CodexHome           string
	PermissionProfileID string
	PermissionProfile   *sandbox.PermissionProfile
	WorkspaceRoots      []string
	Env                 map[string]string
}

func (r *RuntimeRouter) windowsSandboxSetupCWD(param *string) (string, error) {
	if value := strings.TrimSpace(stringPtrValue(param)); value != "" {
		if !filepath.IsAbs(value) {
			return "", jsonRPCInvalidRequest("Invalid request: AbsolutePathBuf deserialized without a base path")
		}
		return filepath.Clean(value), nil
	}
	cwd := strings.TrimSpace(r.services.DefaultCWD)
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	if !filepath.IsAbs(cwd) {
		absolute, err := filepath.Abs(cwd)
		if err != nil {
			return "", err
		}
		cwd = absolute
	}
	return filepath.Clean(cwd), nil
}

func (r *RuntimeRouter) windowsSandboxSetupRuntimeRequest(mode sandbox.WindowsSetupMode, cwd string) (*WindowsSandboxSetupRuntimeRequest, error) {
	read, err := r.requireConfig().Read(&config.ConfigReadParams{CWD: &cwd})
	if err != nil {
		return nil, err
	}
	cfg := &config.Config{Values: read.Config}
	resolved, err := cfg.ResolveSandboxPermissionProfile("", cwd)
	if err != nil {
		return nil, err
	}
	if resolved == nil || resolved.Profile == nil {
		profile := sandbox.WorkspaceWritePermissionProfile()
		resolved = &config.SandboxPermissionProfileResolution{
			ID:      sandbox.BuiltInPermissionProfileWorkspace,
			Profile: &profile,
		}
	}
	return &WindowsSandboxSetupRuntimeRequest{
		Mode:                mode,
		CWD:                 cwd,
		CodexHome:           r.requireConfig().CodexHome(),
		PermissionProfileID: strings.TrimSpace(resolved.ID),
		PermissionProfile:   resolved.Profile,
		WorkspaceRoots:      threadRuntimeWorkspaceRoots(cwd, resolved.WorkspaceRoots),
		Env:                 windowsSandboxSetupEnvMap(os.Environ()),
	}, nil
}

func (r *RuntimeRouter) runWindowsSandboxSetupForConnection(connectionID string, request *WindowsSandboxSetupRuntimeRequest) {
	err := r.windowsSandboxSetupRunner()(request)
	if err == nil {
		err = r.persistWindowsSandboxSetupMode(request.Mode)
	}
	success := err == nil
	errorMessage := ""
	if err != nil {
		errorMessage = err.Error()
	}
	notification, completeErr := r.requireWindows().CompleteSetup(success, errorMessage)
	if completeErr != nil {
		return
	}
	r.notifyToConnection(connectionID, NotificationWindowsSandboxSetupCompleted, notification)
}

func (r *RuntimeRouter) windowsSandboxReadiness() (*sandbox.WindowsReadinessResponse, error) {
	if runtime.GOOS != "windows" {
		return &sandbox.WindowsReadinessResponse{Status: sandbox.WindowsReadinessNotConfigured}, nil
	}
	read, err := r.requireConfig().Read(&config.ConfigReadParams{})
	if err != nil {
		return nil, err
	}
	level := windowsSandboxLevelFromConfigValues(read.Config)
	setupComplete := false
	if level == sandbox.WindowsSandboxElevated {
		setupComplete, _ = windowssandbox.SandboxSetupIsComplete(r.requireConfig().CodexHome())
	}
	response := sandbox.DetermineWindowsReadinessFromState(level, setupComplete)
	if response.Status == sandbox.WindowsReadinessNotConfigured {
		current := r.requireWindows().Readiness()
		if current.Status == sandbox.WindowsReadinessReady {
			return current, nil
		}
	}
	return response, nil
}

func (r *RuntimeRouter) windowsSandboxSetupRunner() WindowsSandboxSetupRunner {
	if r != nil && r.services.WindowsSetupRunner != nil {
		return r.services.WindowsSetupRunner
	}
	return defaultWindowsSandboxSetupRunner
}

func defaultWindowsSandboxSetupRunner(request *WindowsSandboxSetupRuntimeRequest) error {
	if request == nil {
		return fmt.Errorf("%w: request is nil", sandbox.ErrInvalidWindowsSandboxRequest)
	}
	switch request.Mode {
	case sandbox.WindowsSetupElevated:
		if runtime.GOOS != "windows" {
			return errors.New("elevated Windows sandbox setup is only supported on Windows")
		}
		complete, err := windowssandbox.SandboxSetupIsComplete(request.CodexHome)
		if err != nil {
			return err
		}
		if complete {
			return nil
		}
		permissions, err := windowssandbox.ResolvePermissions(request.PermissionProfile, request.WorkspaceRoots)
		if err != nil {
			return err
		}
		return windowssandbox.RunElevatedSetup(&windowssandbox.SandboxSetupRequest{
			CodexHome:   request.CodexHome,
			CommandCWD:  request.CWD,
			Env:         request.Env,
			Permissions: permissions,
		})
	case sandbox.WindowsSetupUnelevated:
		if runtime.GOOS != "windows" {
			return errors.New("legacy Windows sandbox setup is only supported on Windows")
		}
		return windowssandbox.RunWindowsSandboxLegacyPreflight(&windowssandbox.LegacyPreflightRequest{
			PermissionProfileID: request.PermissionProfileID,
			PermissionProfile:   request.PermissionProfile,
			WorkspaceRoots:      request.WorkspaceRoots,
			CodexHome:           request.CodexHome,
			CWD:                 request.CWD,
			Env:                 request.Env,
		})
	default:
		return fmt.Errorf("%w: unsupported mode %q", sandbox.ErrInvalidWindowsSandboxRequest, request.Mode)
	}
}

func (r *RuntimeRouter) persistWindowsSandboxSetupMode(mode sandbox.WindowsSetupMode) error {
	modeValue, err := windowsSandboxSetupModeConfigValue(mode)
	if err != nil {
		return err
	}
	_, err = r.requireConfig().BatchWrite(&config.ConfigBatchWriteParams{
		Edits: []config.ConfigEdit{
			{KeyPath: "windows.sandbox", Value: modeValue, MergeStrategy: config.MergeReplace},
			{KeyPath: "features.experimental_windows_sandbox", Value: nil, MergeStrategy: config.MergeReplace},
			{KeyPath: "features.elevated_windows_sandbox", Value: nil, MergeStrategy: config.MergeReplace},
			{KeyPath: "features.enable_experimental_windows_sandbox", Value: nil, MergeStrategy: config.MergeReplace},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to persist windows sandbox mode: %w", err)
	}
	return nil
}

func windowsSandboxSetupModeConfigValue(mode sandbox.WindowsSetupMode) (string, error) {
	switch mode {
	case sandbox.WindowsSetupElevated:
		return "elevated", nil
	case sandbox.WindowsSetupUnelevated:
		return "unelevated", nil
	default:
		return "", fmt.Errorf("%w: unsupported mode %q", sandbox.ErrInvalidWindowsSandboxRequest, mode)
	}
}

func windowsSandboxLevelFromConfigValues(values map[string]any) sandbox.WindowsSandboxLevel {
	if mode, ok := windowsSandboxModeFromConfigValues(values); ok {
		switch mode {
		case sandbox.WindowsSetupElevated:
			return sandbox.WindowsSandboxElevated
		case sandbox.WindowsSetupUnelevated:
			return sandbox.WindowsSandboxUnelevated
		}
	}
	featureSettings := (&config.Config{Values: values}).FeatureSettings()
	if featureSettings["elevated_windows_sandbox"] {
		return sandbox.WindowsSandboxElevated
	}
	if featureSettings["experimental_windows_sandbox"] {
		return sandbox.WindowsSandboxUnelevated
	}
	return sandbox.WindowsSandboxDisabled
}

func windowsSandboxModeFromConfigValues(values map[string]any) (sandbox.WindowsSetupMode, bool) {
	if values == nil {
		return "", false
	}
	if windows, ok := values["windows"].(map[string]any); ok {
		if mode, ok := parseWindowsSandboxConfigMode(windows["sandbox"]); ok {
			return mode, true
		}
	}
	if mode, ok := parseWindowsSandboxConfigMode(values["windows_sandbox"]); ok {
		return mode, true
	}
	return "", false
}

func parseWindowsSandboxConfigMode(value any) (sandbox.WindowsSetupMode, bool) {
	text := strings.ToLower(strings.TrimSpace(stringFromAny(value)))
	switch text {
	case "elevated":
		return sandbox.WindowsSetupElevated, true
	case "unelevated", "restricted-token", "default":
		return sandbox.WindowsSetupUnelevated, true
	default:
		return "", false
	}
}

func windowsSandboxSetupEnvMap(values []string) map[string]string {
	out := make(map[string]string, len(values))
	for _, item := range values {
		key, value, ok := strings.Cut(item, "=")
		if !ok || key == "" {
			continue
		}
		out[key] = value
	}
	return out
}
