package config

import (
	"errors"
	"fmt"
	"strings"
)

// RuntimeAuthModeRequirement is the managed
// `features.runtime_agent_instances.mode` requirement (Rust
// FeatureRequirementsToml.runtime_auth_mode, #60014). It is internal, like
// Permissions, and stays off the app-server wire schema.
type RuntimeAuthModeRequirement string

const (
	RuntimeAuthModeDualHeader    RuntimeAuthModeRequirement = "dual_header"
	RuntimeAuthModeAssertionOnly RuntimeAuthModeRequirement = "assertion_only"
)

// ParseRuntimeAuthModeRequirement maps the serde spelling of Rust's
// `RuntimeAuthMode` to the Go requirement value.
func ParseRuntimeAuthModeRequirement(value string) (RuntimeAuthModeRequirement, bool) {
	switch RuntimeAuthModeRequirement(strings.ToLower(strings.TrimSpace(value))) {
	case RuntimeAuthModeDualHeader:
		return RuntimeAuthModeDualHeader, true
	case RuntimeAuthModeAssertionOnly:
		return RuntimeAuthModeAssertionOnly, true
	default:
		return "", false
	}
}

// runtimeAuthModeFromValues reads `features.runtime_agent_instances` from the
// effective config values. Rust's `FeatureToml` accepts a boolean gate (which
// selects the default mode) and the table form, which is the only one that
// carries `mode`.
func runtimeAuthModeFromValues(values map[string]any) (RuntimeAuthModeRequirement, bool) {
	if values == nil {
		return "", false
	}
	features, ok := values["features"].(map[string]any)
	if !ok {
		return "", false
	}
	table, ok := features["runtime_agent_instances"].(map[string]any)
	if !ok {
		return "", false
	}
	mode, ok := table["mode"].(string)
	if !ok {
		return "", false
	}
	return ParseRuntimeAuthModeRequirement(mode)
}

// RuntimeAuthMode resolves the effective runtime assertion mode, mirroring Rust
// `resolve_runtime_auth_mode`: a managed requirement wins, the default is
// dual_header, and the second result reports that the requirement overrode a
// different configured mode.
func (c *Config) RuntimeAuthMode() (RuntimeAuthModeRequirement, bool) {
	if c == nil {
		return RuntimeAuthModeDualHeader, false
	}
	configured, hasConfigured := runtimeAuthModeFromValues(c.Values)
	if c.Requirements != nil && c.Requirements.RuntimeAuthMode != nil {
		required := *c.Requirements.RuntimeAuthMode
		return required, hasConfigured && configured != required
	}
	if hasConfigured {
		return configured, false
	}
	return RuntimeAuthModeDualHeader, false
}

// ValidateRuntimeAuthModeRequirement rejects a configured
// `features.runtime_agent_instances.mode` that contradicts the managed
// requirement, mirroring Rust #60014's `ConstraintError::InvalidValue` at
// configuration load time. The message is the same text the startup warning
// carries, so a load failure and a warning never disagree.
func ValidateRuntimeAuthModeRequirement(values map[string]any, requirements *ConfigRequirements) error {
	if requirements == nil || requirements.RuntimeAuthMode == nil {
		return nil
	}
	configured, ok := runtimeAuthModeFromValues(values)
	if !ok {
		return nil
	}
	required := *requirements.RuntimeAuthMode
	if configured == required {
		return nil
	}
	return errors.New(RuntimeAuthModeConflictWarning(configured, required))
}

// RuntimeAuthModeConflictWarning renders the detail Rust builds from
// `ConstraintError::InvalidValue` when a configured mode contradicts the
// managed requirement.
func RuntimeAuthModeConflictWarning(configured, required RuntimeAuthModeRequirement) string {
	return fmt.Sprintf(
		"Configured value for `features.runtime_agent_instances.mode` is disallowed by requirements; "+
			"falling back to required value %s. Details: invalid value for `features.runtime_agent_instances.mode`: "+
			"`%s` is not in the allowed set [%s] (set by %s)",
		runtimeAuthModeRustDebug(required), runtimeAuthModeRustDebug(configured),
		runtimeAuthModeRustDebug(required), managedRequirementSource)
}

// runtimeAuthModeRustDebug renders Rust's `{:?}` form of `RuntimeAuthMode`.
func runtimeAuthModeRustDebug(mode RuntimeAuthModeRequirement) string {
	switch mode {
	case RuntimeAuthModeDualHeader:
		return "DualHeader"
	case RuntimeAuthModeAssertionOnly:
		return "AssertionOnly"
	default:
		return string(mode)
	}
}

// runtimeAuthModeRequirementFromMap reads the typed
// `features.runtime_agent_instances` table from requirements.toml. The boolean
// gate form stays in FeatureRequirements; only the table's `mode` is typed.
func runtimeAuthModeRequirementFromMap(values map[string]any) (*RuntimeAuthModeRequirement, *bool, error) {
	features, ok := mapAnyKey(values, "feature_requirements", "featureRequirements", "features")
	if !ok {
		return nil, nil, nil
	}
	table, ok := mapAnyKey(features, "runtime_agent_instances", "runtimeAgentInstances")
	if !ok {
		return nil, nil, nil
	}
	var enabled *bool
	if value, ok := boolAnyKey(table, "enabled"); ok {
		enabled = &value
	}
	mode, ok := stringAnyKey(table, "mode")
	if !ok {
		return nil, enabled, nil
	}
	parsed, ok := ParseRuntimeAuthModeRequirement(mode)
	if !ok {
		return nil, nil, fmt.Errorf(
			"invalid value for features.runtime_agent_instances.mode: `%s` is not in the allowed set [dual_header, assertion_only]",
			mode)
	}
	return &parsed, enabled, nil
}

// runtimeAuthModeStartupWarnings reports a configured mode that the managed
// requirement overrode.
func runtimeAuthModeStartupWarnings(values map[string]any, requirements *ConfigRequirements) []string {
	if requirements == nil || requirements.RuntimeAuthMode == nil {
		return nil
	}
	configured, ok := runtimeAuthModeFromValues(values)
	if !ok || configured == *requirements.RuntimeAuthMode {
		return nil
	}
	return []string{RuntimeAuthModeConflictWarning(configured, *requirements.RuntimeAuthMode)}
}
