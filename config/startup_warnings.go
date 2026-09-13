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
		// render formats the required value the way Rust's `{:?}` does; nil
		// means a plain quoted string.
		render func(string) string
	}{
		{"cli_auth_credentials_store", requiredEnumValue(requirements.CliAuthCredentialsStore), requirements.CliAuthCredentialsStore != nil, func() string { return configStringValue(values, "cli_auth_credentials_store") }, nil},
		{"chatgpt_base_url", requiredStringValue(requirements.ChatgptBaseURL), requirements.ChatgptBaseURL != nil, func() string { return configStringValue(values, "chatgpt_base_url") }, nil},
		{"sqlite_home", requiredStringValue(requirements.SQLiteHome), requirements.SQLiteHome != nil, func() string { return configStringValue(values, "sqlite_home") }, renderAbsolutePathBuf},
		{"log_dir", requiredStringValue(requirements.LogDir), requirements.LogDir != nil, func() string { return configStringValue(values, "log_dir") }, renderAbsolutePathBuf},
		{"model_catalog_json", requiredStringValue(requirements.ModelCatalogJSON), requirements.ModelCatalogJSON != nil, func() string { return configStringValue(values, "model_catalog_json") }, renderAbsolutePathBuf},
		{"model_provider", requiredStringValue(requirements.ModelProvider), requirements.ModelProvider != nil, func() string { return configStringValue(values, "model_provider") }, nil},
		{"check_for_update_on_startup", requiredBoolValue(requirements.CheckForUpdateOnStartup), requirements.CheckForUpdateOnStartup != nil, func() string { return configBoolValue(values, "check_for_update_on_startup") }, renderBool},
		{"allow_login_shell", requiredBoolValue(requirements.AllowLoginShell), requirements.AllowLoginShell != nil, func() string { return configBoolValue(values, "allow_login_shell") }, renderBool},
	} {
		if !field.hasRequired {
			continue
		}
		configured := field.configured()
		if strings.TrimSpace(configured) == "" || configured == field.required {
			continue
		}
		rendered := field.required
		if field.render != nil {
			rendered = field.render(field.required)
		} else {
			rendered = fmt.Sprintf("%q", field.required)
		}
		warnings = append(warnings, fmt.Sprintf(
			"Configured value for `%s` is overridden by the required value %s from %s.",
			field.name, rendered, managedRequirementSource))
	}
	// Feedback is a structured requirement: a conflicting configured
	// `feedback.enabled` is replaced wholesale.
	if requirements.Feedback != nil && requirements.Feedback.Enabled != nil {
		if configured, ok := configuredFeedbackEnabled(values); ok && configured != *requirements.Feedback.Enabled {
			warnings = append(warnings, fmt.Sprintf(
				"Configured values under `feedback` are overridden by requirements from %s.",
				managedRequirementSource))
		}
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
	// Requirement-constrained enum values: an explicit disallowed approval
	// policy or approvals reviewer falls back to the requirement default (the
	// implicit default is corrected silently, as in Rust), and the resolved
	// web-search mode is constrained whether or not it was configured.
	if configured := configuredApprovalPolicyValue(values["approval_policy"]); configured != "" {
		if _, warning, err := resolveRequirementConstrainedApprovalPolicy(configured, requirements.AllowedApprovalPolicies); err == nil && warning != "" {
			warnings = append(warnings, warning)
		}
	}
	if configured := configuredApprovalsReviewerValue(values["approvals_reviewer"]); configured != "" {
		if _, warning, err := resolveRequirementConstrainedApprovalsReviewer(configured, requirements.AllowedApprovalsReviewers); err == nil && warning != "" {
			warnings = append(warnings, warning)
		}
	}
	// A configured default_permissions the managed allow-list disallows falls
	// back to the required default.
	if warning := (&Config{Values: values, Requirements: requirements}).PermissionProfileRequirementWarning(); warning != "" {
		warnings = append(warnings, warning)
	}
	if _, warning, err := resolveRequirementConstrainedWebSearchMode(webSearchModeForConfigValues(values), requirements.AllowedWebSearchModes); err == nil && warning != "" {
		warnings = append(warnings, warning)
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

// requiredBoolValue renders a required boolean for comparison.
func requiredBoolValue(value *bool) string {
	if value == nil {
		return ""
	}
	return fmt.Sprintf("%t", *value)
}

// configBoolValue renders a configured boolean, or the empty string when the
// key is absent or not a boolean.
func configBoolValue(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	raw, ok := values[key]
	if !ok || raw == nil {
		return ""
	}
	if value, ok := raw.(bool); ok {
		return fmt.Sprintf("%t", value)
	}
	return strings.TrimSpace(fmt.Sprint(raw))
}

// renderBool formats a required boolean the way Rust's `{:?}` does.
func renderBool(required string) string {
	return required
}

// renderAbsolutePathBuf formats a required path the way Rust's
// `AbsolutePathBuf` Debug impl does.
func renderAbsolutePathBuf(required string) string {
	return fmt.Sprintf("AbsolutePathBuf(%q)", required)
}

// configuredFeedbackEnabled reports the explicitly configured
// `feedback.enabled` value.
func configuredFeedbackEnabled(values map[string]any) (bool, bool) {
	if values == nil {
		return false, false
	}
	feedback, ok := values["feedback"].(map[string]any)
	if !ok {
		return false, false
	}
	return boolAnyKey(feedback, "enabled")
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
