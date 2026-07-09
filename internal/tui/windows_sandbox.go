package tui

import "strings"

// Rust parity: codex-rs/tui/src/windows_sandbox.rs.

type WindowsSandboxLevel string

const (
	WindowsSandboxLevelDisabled        WindowsSandboxLevel = "disabled"
	WindowsSandboxLevelElevated        WindowsSandboxLevel = "elevated"
	WindowsSandboxLevelRestrictedToken WindowsSandboxLevel = "restricted-token"
)

type WindowsSandboxModeConfig string

const (
	WindowsSandboxModeConfigElevated   WindowsSandboxModeConfig = "elevated"
	WindowsSandboxModeConfigUnelevated WindowsSandboxModeConfig = "unelevated"
)

type WindowsSandboxFeatureFlags struct {
	WindowsSandbox         bool
	WindowsSandboxElevated bool
}

type WindowsSandboxPromptState struct {
	Required bool
	Elevated bool
}

func WindowsSandboxLevelFromConfig(mode *WindowsSandboxModeConfig, features WindowsSandboxFeatureFlags) WindowsSandboxLevel {
	if mode != nil {
		switch *mode {
		case WindowsSandboxModeConfigElevated:
			return WindowsSandboxLevelElevated
		case WindowsSandboxModeConfigUnelevated:
			return WindowsSandboxLevelRestrictedToken
		}
	}
	switch {
	case features.WindowsSandboxElevated:
		return WindowsSandboxLevelElevated
	case features.WindowsSandbox:
		return WindowsSandboxLevelRestrictedToken
	default:
		return WindowsSandboxLevelDisabled
	}
}

func ParseWindowsSandboxModeConfig(value string) (*WindowsSandboxModeConfig, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(WindowsSandboxModeConfigElevated):
		mode := WindowsSandboxModeConfigElevated
		return &mode, true
	case string(WindowsSandboxModeConfigUnelevated), "restricted-token", "default":
		mode := WindowsSandboxModeConfigUnelevated
		return &mode, true
	default:
		return nil, false
	}
}

func SandboxSetupIsComplete(codexHome string) bool {
	_ = codexHome
	return false
}
