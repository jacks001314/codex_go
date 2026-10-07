package config

import (
	"errors"
	"strings"
)

// Rust parity: codex-rs/core/src/windows_sandbox.rs::resolve_windows_sandbox_mode
// and codex-rs/core/src/config/mod.rs's requirement-constrained application of
// "windows.sandbox" (apply_requirement_constrained_value).

// WindowsSandboxMode is the configured `windows.sandbox` mode, mirroring Rust's
// WindowsSandboxModeToml (#46271): the legacy elevated/unelevated backends plus
// the native mxc backend. It is distinct from WindowsSandboxSetupMode, which
// only describes the legacy setup RPC's elevated/unelevated choice and cannot
// represent mxc.
type WindowsSandboxMode string

const (
	WindowsSandboxModeElevated   WindowsSandboxMode = "elevated"
	WindowsSandboxModeUnelevated WindowsSandboxMode = "unelevated"
	WindowsSandboxModeMxc        WindowsSandboxMode = "mxc"
)

// WindowsSandboxModeFromValues resolves the configured Windows sandbox mode:
// `[windows] sandbox` first, then the legacy `windows_sandbox` key, then the
// legacy `elevated_windows_sandbox` / `experimental_windows_sandbox` feature
// flags. It reports false when no mode is configured.
func WindowsSandboxModeFromValues(values map[string]any) (WindowsSandboxMode, bool) {
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
		return WindowsSandboxModeElevated, true
	}
	if featureSettings["experimental_windows_sandbox"] {
		return WindowsSandboxModeUnelevated, true
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
//
// The allow-list only covers the legacy elevated/unelevated backends: Rust
// accepts mxc unconditionally (#46271), so a configured mxc is never replaced.
func ResolveWindowsSandboxMode(values map[string]any, requirements *ConfigRequirements) (mode WindowsSandboxMode, fellBack bool, ok bool) {
	configured, configuredOK := WindowsSandboxModeFromValues(values)
	if configuredOK && configured == WindowsSandboxModeMxc {
		return configured, false, true
	}
	if requirements == nil || len(requirements.AllowedWindowsSandboxImplementations) == 0 {
		return configured, false, configuredOK
	}
	allowed := make(map[WindowsSandboxMode]bool, len(requirements.AllowedWindowsSandboxImplementations))
	fallback := WindowsSandboxModeUnelevated
	for _, entry := range requirements.AllowedWindowsSandboxImplementations {
		normalized := WindowsSandboxMode(entry)
		allowed[normalized] = true
		if normalized == WindowsSandboxModeElevated {
			// Prefer elevated when both implementations are allowed.
			fallback = WindowsSandboxModeElevated
		}
	}
	if configuredOK && allowed[configured] {
		return configured, false, true
	}
	return fallback, true, true
}

func parseWindowsSandboxModeValue(value any) (WindowsSandboxMode, bool) {
	text := strings.ToLower(strings.TrimSpace(stringValueFromAny(value)))
	switch text {
	case "elevated":
		return WindowsSandboxModeElevated, true
	case "unelevated", "restricted-token", "default":
		return WindowsSandboxModeUnelevated, true
	case "mxc":
		return WindowsSandboxModeMxc, true
	default:
		return "", false
	}
}

func stringValueFromAny(value any) string {
	text, _ := value.(string)
	return text
}

// WindowsAllowMXCFromValues reads the `windows.allow_mxc` opt-out added by Rust
// #51547. The setting is absent by default (allow=true, configured=false), so
// omitting it preserves the existing behavior; an explicit false blocks both
// explicit MXC configuration and automatic MXC selection.
func WindowsAllowMXCFromValues(values map[string]any) (allow bool, configured bool) {
	table, ok := values["windows"].(map[string]any)
	if !ok {
		return true, false
	}
	value, ok := table["allow_mxc"].(bool)
	if !ok {
		return true, false
	}
	return value, true
}

// ValidateWindowsMXCOptOut rejects an explicit `windows.sandbox = "mxc"` when
// `windows.allow_mxc` is false, mirroring Rust #51547's load-time failure
// (`Config::load` returns InvalidInput with the same message).
func ValidateWindowsMXCOptOut(values map[string]any) error {
	if allow, configured := WindowsAllowMXCFromValues(values); configured && !allow {
		if mode, ok := WindowsSandboxModeFromValues(values); ok && mode == WindowsSandboxModeMxc {
			return errors.New(`windows.sandbox = "mxc" is not allowed when windows.allow_mxc = false`)
		}
	}
	return nil
}

// WindowsAutomaticMXCAllowed reports whether automatic MXC selection is allowed
// for the configuration (Rust #51547's `config_allows_mxc` opt-out clause).
// Go does not select MXC implicitly - the backend requires an explicit
// `windows.sandbox = "mxc"` - so this predicate only documents and freezes the
// opt-out semantics for the automatic path.
func WindowsAutomaticMXCAllowed(values map[string]any) bool {
	allow, configured := WindowsAllowMXCFromValues(values)
	return !configured || allow
}
