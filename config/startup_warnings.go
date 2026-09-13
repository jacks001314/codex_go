package config

import (
	"fmt"
	"strings"
)

// Rust parity: codex-rs/core/src/config/requirements.rs (apply_exact_requirement
// and the requirement-constrained fallbacks) plus codex-rs/core/src/config/mod.rs
// (apply_requirement_constrained_value). Managed requirements replace configured
// values, and a conflict produces a source-aware startup warning that the
// app-server surfaces as a config warning.
//
// Go's requirements model does not carry Rust's typed RequirementSource, so the
// warnings name the managed requirement rather than the exact origin.

// managedRequirementSource labels a warning whose override comes from the
// managed requirements layer.
const managedRequirementSource = "managed requirements"

// StartupWarnings reports the requirement-driven overrides and fallbacks that
// apply to the given effective configuration, in a stable order.
func StartupWarnings(values map[string]any, requirements *ConfigRequirements) []string {
	if values == nil || requirements == nil {
		return nil
	}
	var warnings []string
	// Exact requirements replace the configured value outright.
	for _, field := range []struct {
		name        string
		required    string
		hasRequired bool
		configured  func() string
	}{
		{"cli_auth_credentials_store", requiredEnumValue(requirements.CliAuthCredentialsStore), requirements.CliAuthCredentialsStore != nil, func() string { return configStringValue(values, "cli_auth_credentials_store") }},
		{"chatgpt_base_url", requiredStringValue(requirements.ChatgptBaseURL), requirements.ChatgptBaseURL != nil, func() string { return configStringValue(values, "chatgpt_base_url") }},
		{"model_provider", requiredStringValue(requirements.ModelProvider), requirements.ModelProvider != nil, func() string { return configStringValue(values, "model_provider") }},
	} {
		if !field.hasRequired {
			continue
		}
		configured := field.configured()
		if strings.TrimSpace(configured) == "" || configured == field.required {
			continue
		}
		warnings = append(warnings, fmt.Sprintf(
			"Configured value for `%s` is overridden by the required value %q from %s.",
			field.name, field.required, managedRequirementSource))
	}
	// windows.sandbox_private_desktop is an exact requirement too, but the
	// configured value defaults to true, so only an explicit conflict warns.
	if requirements.WindowsSandboxPrivateDesktop != nil {
		if configured, ok := configuredWindowsSandboxPrivateDesktop(values); ok && configured != *requirements.WindowsSandboxPrivateDesktop {
			warnings = append(warnings, fmt.Sprintf(
				"Configured value for `windows.sandbox_private_desktop` is overridden by the required value %t from %s.",
				*requirements.WindowsSandboxPrivateDesktop, managedRequirementSource))
		}
	}
	// A disallowed (or absent) windows.sandbox falls back to the constrained
	// initial mode.
	if mode, fellBack, ok := ResolveWindowsSandboxMode(values, requirements); ok && fellBack {
		warnings = append(warnings, fmt.Sprintf(
			"Configured value for `windows.sandbox` is disallowed by requirements; falling back to required value %q.",
			mode))
	}
	return warnings
}

func configStringValue(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	raw, ok := values[key]
	if !ok || raw == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(raw))
}

func requiredStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

// requiredEnumValue renders a required enum value as its wire string.
func requiredEnumValue[T ~string](value *T) string {
	if value == nil {
		return ""
	}
	return string(*value)
}

// configuredWindowsSandboxPrivateDesktop reports the explicitly configured
// `[windows] sandbox_private_desktop` value (the legacy `permissions` shape is
// honored as Go does elsewhere). The implicit default is not a conflict.
func configuredWindowsSandboxPrivateDesktop(values map[string]any) (bool, bool) {
	if permissions, ok := values["permissions"].(map[string]any); ok {
		if value, ok := boolAnyKey(permissions, "windows_sandbox_private_desktop", "windowsSandboxPrivateDesktop"); ok {
			return value, true
		}
	}
	if windows, ok := values["windows"].(map[string]any); ok {
		if value, ok := boolAnyKey(windows, "sandbox_private_desktop", "sandboxPrivateDesktop"); ok {
			return value, true
		}
	}
	return false, false
}
