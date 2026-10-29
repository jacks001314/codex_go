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

// WindowsSandboxPolicy is the managed Windows sandbox policy reported by
// configRequirements/read (Rust #45813 WindowsSandboxConfig.requirements).
// Loaded is false until a read succeeds, which denies every setup choice;
// Allowed is nil when the policy does not restrict the implementation, and an
// empty list when it allows none. MXC is an implementation, not a TUI setup
// mode, so callers drop it before building the policy.
type WindowsSandboxPolicy struct {
	Loaded  bool
	Allowed []WindowsSandboxModeConfig
}

// WindowsSandboxPolicyFromImplementations converts the managed
// `allowedWindowsSandboxImplementations` list into a TUI policy. A nil list is
// the unconstrained policy, while an empty list allows no setup mode; MXC is
// dropped because it is not a TUI setup mode (Rust #45813 from_responses).
func WindowsSandboxPolicyFromImplementations(implementations []string) WindowsSandboxPolicy {
	if implementations == nil {
		return WindowsSandboxPolicy{Loaded: true}
	}
	allowed := make([]WindowsSandboxModeConfig, 0, len(implementations))
	for _, implementation := range implementations {
		switch strings.ToLower(strings.TrimSpace(implementation)) {
		case string(WindowsSandboxModeConfigElevated):
			allowed = append(allowed, WindowsSandboxModeConfigElevated)
		case string(WindowsSandboxModeConfigUnelevated):
			allowed = append(allowed, WindowsSandboxModeConfigUnelevated)
		}
	}
	return WindowsSandboxPolicy{Loaded: true, Allowed: allowed}
}

// WindowsSandboxConfig is the TUI's Windows sandbox setup state: the effective
// mode from app-server configuration plus the managed policy that constrains
// it (Rust #45813 WindowsSandboxConfig).
type WindowsSandboxConfig struct {
	// Mode is nil when no enabled mode is configured or allowed.
	Mode *WindowsSandboxModeConfig
	// RequirementsLoaded is false until the managed policy has been read.
	RequirementsLoaded bool
	// AllowedModes is meaningful only when RequirementsLoaded is true; nil means
	// the policy allows both modes and an empty list allows neither.
	AllowedModes []WindowsSandboxModeConfig
}

// Allows reports whether the managed policy permits a setup mode.
func (c WindowsSandboxConfig) Allows(mode WindowsSandboxModeConfig) bool {
	if !c.RequirementsLoaded {
		return false
	}
	if c.AllowedModes == nil {
		return true
	}
	return windowsSandboxModeListContains(c.AllowedModes, mode)
}

// RequiresElevated reports whether an elevated setup is selected under a loaded
// managed policy, mirroring Rust requires_elevated.
func (c WindowsSandboxConfig) RequiresElevated() bool {
	return c.Mode != nil &&
		*c.Mode == WindowsSandboxModeConfigElevated &&
		c.RequirementsLoaded &&
		c.AllowedModes != nil
}

// Level reports the effective sandbox level, mirroring Rust level().
func (c WindowsSandboxConfig) Level() WindowsSandboxLevel {
	if c.Mode == nil {
		return WindowsSandboxLevelDisabled
	}
	switch *c.Mode {
	case WindowsSandboxModeConfigElevated:
		return WindowsSandboxLevelElevated
	case WindowsSandboxModeConfigUnelevated:
		return WindowsSandboxLevelRestrictedToken
	default:
		return WindowsSandboxLevelDisabled
	}
}

// WindowsSandboxConfigFromResponses mirrors Rust #45813 from_responses: explicit
// `windows.sandbox` configuration wins over the legacy feature flags, and a
// configured mode that managed requirements disallow falls back to an allowed
// mode with elevated preferred.
func WindowsSandboxConfigFromResponses(values map[string]any, policy WindowsSandboxPolicy) WindowsSandboxConfig {
	state := WindowsSandboxConfig{
		Mode:               windowsSandboxSetupModeFromValues(values),
		RequirementsLoaded: policy.Loaded,
	}
	if policy.Allowed != nil {
		state.AllowedModes = make([]WindowsSandboxModeConfig, len(policy.Allowed))
		copy(state.AllowedModes, policy.Allowed)
	}
	if state.Mode == nil {
		state.Mode = windowsSandboxFeatureSetupModeFromValues(values)
	}
	if state.AllowedModes != nil &&
		(state.Mode == nil || !windowsSandboxModeListContains(state.AllowedModes, *state.Mode)) {
		state.Mode = nil
		for _, candidate := range []WindowsSandboxModeConfig{
			WindowsSandboxModeConfigElevated,
			WindowsSandboxModeConfigUnelevated,
		} {
			if windowsSandboxModeListContains(state.AllowedModes, candidate) {
				mode := candidate
				state.Mode = &mode
				break
			}
		}
	}
	return state
}

// windowsSandboxSetupModeFromValues reads `[windows] sandbox` strictly, like
// serde deserializing WindowsSandboxSetupMode.
func windowsSandboxSetupModeFromValues(values map[string]any) *WindowsSandboxModeConfig {
	windows, ok := values["windows"].(map[string]any)
	if !ok {
		return nil
	}
	return windowsSandboxSetupModeFromValue(windows["sandbox"])
}

func windowsSandboxSetupModeFromValue(value any) *WindowsSandboxModeConfig {
	raw, ok := value.(string)
	if !ok {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(WindowsSandboxModeConfigElevated):
		mode := WindowsSandboxModeConfigElevated
		return &mode
	case string(WindowsSandboxModeConfigUnelevated):
		mode := WindowsSandboxModeConfigUnelevated
		return &mode
	default:
		return nil
	}
}

// windowsSandboxFeatureSetupModeFromValues mirrors the legacy feature fallback:
// the first enabled flag in Rust's order selects the mode.
func windowsSandboxFeatureSetupModeFromValues(values map[string]any) *WindowsSandboxModeConfig {
	features, ok := values["features"].(map[string]any)
	if !ok {
		return nil
	}
	for _, candidate := range []struct {
		key  string
		mode WindowsSandboxModeConfig
	}{
		{key: "elevated_windows_sandbox", mode: WindowsSandboxModeConfigElevated},
		{key: "experimental_windows_sandbox", mode: WindowsSandboxModeConfigUnelevated},
		{key: "enable_experimental_windows_sandbox", mode: WindowsSandboxModeConfigUnelevated},
	} {
		if enabled, ok := features[candidate.key].(bool); ok && enabled {
			mode := candidate.mode
			return &mode
		}
	}
	return nil
}

func windowsSandboxModeListContains(modes []WindowsSandboxModeConfig, mode WindowsSandboxModeConfig) bool {
	for _, candidate := range modes {
		if candidate == mode {
			return true
		}
	}
	return false
}
