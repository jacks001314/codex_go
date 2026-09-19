package config

import "strings"

// Rust parity: codex-rs/core/src/windows_sandbox.rs::resolve_windows_sandbox_mode
// and codex-rs/core/src/config/mod.rs's requirement-constrained application of
// "windows.sandbox" (apply_requirement_constrained_value).

// WindowsSandboxModeFromValues resolves the configured Windows sandbox mode:
// `[windows] sandbox` first, then the legacy `windows_sandbox` key, then the
// legacy `elevated_windows_sandbox` / `experimental_windows_sandbox` feature
// flags. It reports false when no mode is configured.
func WindowsSandboxModeFromValues(values map[string]any) (WindowsSandboxSetupMode, bool) {
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
	featureSettings := (&Config{Values: values}).FeatureSettings()
	if featureSettings["elevated_windows_sandbox"] {
		return WindowsSandboxSetupElevated, true
	}
	if featureSettings["experimental_windows_sandbox"] {
		return WindowsSandboxSetupUnelevated, true
	}
	return "", false
}

// ResolveWindowsSandboxMode applies the managed
// `windows.allowed_sandbox_implementations` allow-list to the configured mode
// (Rust apply_requirement_constrained_value for "windows.sandbox"). When the
// configured value is disallowed - including "not configured" while a list is
// present - the constrained value's initial mode is used (elevated when the list
// allows it, otherwise unelevated). It reports whether the configured value was
// replaced and whether a mode is in effect at all.
func ResolveWindowsSandboxMode(values map[string]any, requirements *ConfigRequirements) (mode WindowsSandboxSetupMode, fellBack bool, ok bool) {
	configured, configuredOK := WindowsSandboxModeFromValues(values)
	if requirements == nil || len(requirements.AllowedWindowsSandboxImplementations) == 0 {
		return configured, false, configuredOK
	}
	allowed := make(map[WindowsSandboxSetupMode]bool, len(requirements.AllowedWindowsSandboxImplementations))
	fallback := WindowsSandboxSetupUnelevated
	for _, entry := range requirements.AllowedWindowsSandboxImplementations {
		normalized := WindowsSandboxSetupMode(entry)
		allowed[normalized] = true
		if normalized == WindowsSandboxSetupElevated {
			// Prefer elevated when both implementations are allowed.
			fallback = WindowsSandboxSetupElevated
		}
	}
	if configuredOK && allowed[configured] {
		return configured, false, true
	}
	return fallback, true, true
}

func parseWindowsSandboxModeValue(value any) (WindowsSandboxSetupMode, bool) {
	text := strings.ToLower(strings.TrimSpace(stringValueFromAny(value)))
	switch text {
	case "elevated":
		return WindowsSandboxSetupElevated, true
	case "unelevated", "restricted-token", "default":
		return WindowsSandboxSetupUnelevated, true
	default:
		return "", false
	}
}

func stringValueFromAny(value any) string {
	text, _ := value.(string)
	return text
}
