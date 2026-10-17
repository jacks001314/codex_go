package config

import (
	"strings"

	"codex_go/sandbox"
)

// PreparedWindowsSandboxConfig separates the configured Windows sandbox mode
// from the effective mode after feature fallback and managed requirements.
type PreparedWindowsSandboxConfig struct {
	// Mode is the explicitly configured or requirement-constrained mode. It
	// stays empty when only a feature fallback selected a mode.
	Mode WindowsSandboxMode
	// EffectiveMode is the mode actually used after feature fallback and
	// requirements are applied.
	EffectiveMode WindowsSandboxMode
	// Level is Go's effective sandbox level; MXC remains distinct because Go's
	// level enum carries the native backend identity.
	Level    sandbox.WindowsSandboxLevel
	FellBack bool
	// Effective reports whether any mode is in effect.
	Effective bool
}

// prepareWindowsSandboxConfig mirrors Rust #45312: explicit configuration wins
// over the feature fallback, requirements constrain the selected mode, and a
// feature-only fallback never becomes the persisted configured mode.
func prepareWindowsSandboxConfig(values map[string]any, requirements *ConfigRequirements) PreparedWindowsSandboxConfig {
	configured, configuredOK := explicitWindowsSandboxModeFromValues(values)
	featureMode, featureOK := windowsSandboxFeatureModeFromValues(values)
	selected, selectedOK := configured, configuredOK
	if !selectedOK && featureOK {
		selected, selectedOK = featureMode, true
	}
	effectiveMode, fellBack, effective := applyWindowsSandboxRequirements(selected, selectedOK, requirements)
	mode := configured
	if fellBack {
		mode = effectiveMode
	}
	return PreparedWindowsSandboxConfig{
		Mode:          mode,
		EffectiveMode: effectiveMode,
		Level:         windowsSandboxLevelForMode(effectiveMode),
		FellBack:      fellBack,
		Effective:     effective,
	}
}

func explicitWindowsSandboxModeFromValues(values map[string]any) (WindowsSandboxMode, bool) {
	if values == nil {
		return "", false
	}
	if windows, ok := values["windows"].(map[string]any); ok {
		if mode, ok := parseWindowsSandboxModeValue(windows["sandbox"]); ok {
			return mode, true
		}
	}
	if mode, ok := parseWindowsSandboxModeValue(values["windows_sandbox"]); ok {
		return mode, true
	}
	return "", false
}

func windowsSandboxFeatureModeFromValues(values map[string]any) (WindowsSandboxMode, bool) {
	if values == nil {
		return "", false
	}
	featureSettings := (&Config{Values: values}).FeatureSettings()
	if featureSettings["elevated_windows_sandbox"] {
		return WindowsSandboxModeElevated, true
	}
	if featureSettings["experimental_windows_sandbox"] {
		return WindowsSandboxModeUnelevated, true
	}
	if featureSettings["windows_sandbox"] {
		return WindowsSandboxModeUnelevated, true
	}
	return "", false
}

func applyWindowsSandboxRequirements(configured WindowsSandboxMode, configuredOK bool, requirements *ConfigRequirements) (WindowsSandboxMode, bool, bool) {
	if configuredOK && configured == WindowsSandboxModeMxc {
		return configured, false, true
	}
	if requirements == nil || len(requirements.AllowedWindowsSandboxImplementations) == 0 {
		return configured, false, configuredOK
	}
	allowed := make(map[WindowsSandboxMode]bool, len(requirements.AllowedWindowsSandboxImplementations))
	fallback := WindowsSandboxModeUnelevated
	for _, entry := range requirements.AllowedWindowsSandboxImplementations {
		normalized := WindowsSandboxMode(strings.TrimSpace(string(entry)))
		allowed[normalized] = true
		if normalized == WindowsSandboxModeElevated {
			fallback = WindowsSandboxModeElevated
		}
	}
	if configuredOK && allowed[configured] {
		return configured, false, true
	}
	return fallback, true, true
}

func windowsSandboxLevelForMode(mode WindowsSandboxMode) sandbox.WindowsSandboxLevel {
	switch mode {
	case WindowsSandboxModeElevated:
		return sandbox.WindowsSandboxElevated
	case WindowsSandboxModeUnelevated:
		return sandbox.WindowsSandboxUnelevated
	case WindowsSandboxModeMxc:
		return sandbox.WindowsSandboxMxc
	default:
		return sandbox.WindowsSandboxDisabled
	}
}
