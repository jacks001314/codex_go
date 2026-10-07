package tui

import (
	"strings"

	"codex_go/config"
	"codex_go/features"
	"codex_go/model"
)

// Service-tier and speed-policy resolution (Rust #51253, "Enforce Fast and Ultra
// Fast policies independently", upstream 7ac954ea24).
//
// Rust keeps this in codex-rs/tui/src/service_tier_resolution.rs together with
// codex_features::Features::service_tier_enabled:
//
//   - service_tier_enabled routes a tier id to the policy that owns it: "flex"
//     is always enabled, "ultrafast" follows features.ultrafast_mode, and every
//     other tier follows features.fast_mode.
//   - constrain_server_service_tiers filters the server model catalog by the
//     requirements-reported policies. Ultra Fast stays behind the shared Fast
//     gate when the server omits supportsIndependentSpeedModes (older servers),
//     which is what lets those servers keep enforcing one shared speed policy.
//   - the effect on the TUI: service-tier commands, the saved selection and the
//     fast-mode surfaces all follow the per-mode policy instead of a single
//     shared Fast gate.

const (
	// ServiceTierDefaultRequestValue mirrors Rust SERVICE_TIER_DEFAULT_REQUEST_VALUE.
	ServiceTierDefaultRequestValue = "default"
	// ServiceTierFastRequestValue is the Fast tier's request value (Rust
	// SPEED_TIER_FAST / ServiceTier::Fast).
	ServiceTierFastRequestValue = "priority"
	// ServiceTierFlexRequestValue is the flex tier, which no speed policy gates.
	ServiceTierFlexRequestValue = "flex"
	// ServiceTierUltrafastRequestValue is the Ultra Fast tier.
	ServiceTierUltrafastRequestValue = "ultrafast"

	// FeatureKeyFastMode gates every service tier except flex and ultrafast.
	FeatureKeyFastMode = "fast_mode"
	// FeatureKeyUltrafastMode gates the ultrafast tier (Rust
	// features.ultrafast_mode, added default-enabled by #51253).
	FeatureKeyUltrafastMode = "ultrafast_mode"
)

// ServiceTierEnabled mirrors codex_features::Features::service_tier_enabled
// (#51253): whether a routing tier is enabled by policy, independently of model
// catalog support.
func ServiceTierEnabled(featureSettings map[string]bool, serviceTier string) bool {
	switch strings.ToLower(strings.TrimSpace(serviceTier)) {
	case ServiceTierFlexRequestValue:
		return true
	case ServiceTierUltrafastRequestValue:
		return UltrafastModeEnabled(featureSettings)
	default:
		return features.Enabled(featureSettings, FeatureKeyFastMode)
	}
}

// UltrafastModeEnabled resolves the Ultra Fast policy from the feature settings.
// Rust's `ultrafast_mode` is default-enabled (FeatureSpec default_enabled: true),
// so an absent key means enabled rather than disabled.
func UltrafastModeEnabled(featureSettings map[string]bool) bool {
	if enabled, ok := featureSettings[FeatureKeyUltrafastMode]; ok {
		return enabled
	}
	return true
}

// ConstrainServerServiceTiers mirrors Rust
// service_tier_resolution::constrain_server_service_tiers (#51253): drop the
// catalog's service tiers (and clear a disabled default tier) that the server's
// reported policies do not allow.
//
// A response without feature requirements leaves the catalog untouched, matching
// Rust's early return. `supports_independent_speed_modes` absent (nil) keeps
// Ultra Fast behind the shared Fast gate, which is how older servers enforce it.
func ConstrainServerServiceTiers(models []model.ModelInfo, response *config.ConfigRequirementsReadResponse) []model.ModelInfo {
	if response == nil || response.Requirements == nil || response.Requirements.FeatureRequirements == nil {
		return models
	}
	requirements := response.Requirements.FeatureRequirements
	fastEnabled := true
	if enabled, ok := requirements[FeatureKeyFastMode]; ok {
		fastEnabled = enabled
	}
	ultrafastEnabled := true
	if enabled, ok := requirements[FeatureKeyUltrafastMode]; ok {
		ultrafastEnabled = enabled
	}
	independent := response.SupportsIndependentSpeedModes != nil && *response.SupportsIndependentSpeedModes
	ultrafastEnabled = ultrafastEnabled && (independent || fastEnabled)
	tierEnabled := func(tier string) bool {
		switch strings.ToLower(strings.TrimSpace(tier)) {
		case ServiceTierFlexRequestValue:
			return true
		case ServiceTierUltrafastRequestValue:
			return ultrafastEnabled
		default:
			return fastEnabled
		}
	}
	constrained := make([]model.ModelInfo, len(models))
	copy(constrained, models)
	for index := range constrained {
		tiers := make([]string, 0, len(constrained[index].ServiceTiers))
		for _, tier := range constrained[index].ServiceTiers {
			if tierEnabled(tier) {
				tiers = append(tiers, tier)
			}
		}
		constrained[index].ServiceTiers = tiers
		if constrained[index].DefaultServiceTier != "" && !tierEnabled(constrained[index].DefaultServiceTier) {
			constrained[index].DefaultServiceTier = ""
		}
	}
	return constrained
}

// ResolveServiceTier is the launch-configuration fallback used before a catalog
// is available.
func ResolveServiceTier(configured string, fallback string) string {
	if configured != "" && configured != "default" {
		return configured
	}
	return fallback
}
