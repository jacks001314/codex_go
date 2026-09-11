package tui

// Rust parity: codex-rs/tui/src/managed_new_thread_defaults.rs (#44693).
// Managed new-thread values are defaults rather than enforcement: explicit
// launch choices from `-m` (the harness model override), generic
// `-c key=value` settings, and explicitly selected profiles must win.

import (
	"strings"

	"codex_go/config"
)

// ManagedNewThreadDefaults mirrors codex_app_server_protocol::NewThreadModelDefaults:
// the requirements-provided defaults for a new thread.
type ManagedNewThreadDefaults struct {
	Model           string
	ReasoningEffort string
	ServiceTier     string
}

// ManagedNewThreadDefaultsTarget is the effective launch selection a new thread
// would use; the managed defaults fill only fields the user did not choose.
type ManagedNewThreadDefaultsTarget struct {
	Model           string
	ReasoningEffort string
	ServiceTier     string
}

// ApplyManagedNewThreadDefaults mirrors Rust apply_managed_new_thread_defaults
// (#44693): model and reasoning effort are a compatibility-sensitive pair, so an
// explicit launch choice for either opts out of both managed values, while
// service tier is independent. A profile only counts when it supplies the
// highest-precedence active value (config.HasLaunchSetting), so project or
// managed layers that shadow a profile do not block the defaults.
//
// harnessModelSet reports whether a dedicated `-m`/model flag was passed, and
// harnessServiceTierSet whether a dedicated service-tier flag was passed.
func ApplyManagedNewThreadDefaults(
	target *ManagedNewThreadDefaultsTarget,
	defaults *ManagedNewThreadDefaults,
	layers []config.Layer,
	cliKVOverrides []string,
	harnessModelSet bool,
	harnessServiceTierSet bool,
) {
	if target == nil || defaults == nil {
		return
	}
	hasExplicitModelSettings := harnessModelSet ||
		config.HasLaunchSetting(layers, cliKVOverrides, "model") ||
		config.HasLaunchSetting(layers, cliKVOverrides, "model_reasoning_effort")

	if !hasExplicitModelSettings {
		if model := strings.TrimSpace(defaults.Model); model != "" {
			target.Model = model
		}
		if effort := strings.TrimSpace(defaults.ReasoningEffort); effort != "" {
			target.ReasoningEffort = effort
		}
	}
	if !harnessServiceTierSet &&
		!config.HasLaunchSetting(layers, cliKVOverrides, "service_tier") {
		if tier := strings.TrimSpace(defaults.ServiceTier); tier != "" {
			target.ServiceTier = NormalizeManagedServiceTier(tier)
		}
	}
}

// NormalizeManagedServiceTier mirrors Rust ServiceTier::from_request_value:
// "fast"/"priority" canonicalize to "priority" and "flex" stays "flex"; an
// unrecognized value is preserved verbatim (Rust's unwrap_or_else fallback).
func NormalizeManagedServiceTier(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "fast", "priority":
		return "priority"
	case "flex":
		return "flex"
	default:
		return value
	}
}
