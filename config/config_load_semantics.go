package config

import (
	"errors"
	"fmt"
	"strings"

	"codex_go/model"
)

// ValidateLoadedConfigSemantics replays the Config::load checks Rust runs on the
// merged layers: a caller that only needs raw values (the mcp CLI) must still
// reject a document the full config loader would refuse.
func ValidateLoadedConfigSemantics(values map[string]any) error {
	if err := validateLegacyProfileSelector(values); err != nil {
		return err
	}
	if err := validateDefaultPermissionsSelector(values); err != nil {
		return err
	}
	return validateModelProviderSelection(values)
}

// validateLegacyProfileSelector rejects the retired `profile = "..."` selector.
// Rust moved profiles to `--profile x` plus `x.config.toml`, so any string value
// fails the load, including an empty one.
func validateLegacyProfileSelector(values map[string]any) error {
	raw, ok := values["profile"]
	if !ok {
		return nil
	}
	profile, _ := raw.(string)
	return fmt.Errorf(
		"legacy `profile = %q` config is no longer supported; use `--profile %s` with `%s.config.toml` instead",
		profile, profile, profile,
	)
}

// validateDefaultPermissionsSelector applies the same two rejections as Rust's
// permissions.rs: a custom profile needs a `[permissions]` table, and a
// `:`-prefixed ID must name a runtime built-in.
func validateDefaultPermissionsSelector(values map[string]any) error {
	profiles, hasProfiles := values["permissions"].(map[string]any)
	raw, ok := values["default_permissions"]
	if !ok {
		if hasProfiles && len(profiles) > 0 {
			return errors.New("config defines `[permissions]` profiles but does not set `default_permissions`")
		}
		return nil
	}
	profileID, _ := raw.(string)
	if strings.HasPrefix(profileID, ":") {
		if !isRuntimeBuiltinPermissionProfile(profileID) {
			return fmt.Errorf("default_permissions refers to unknown built-in profile `%s`", profileID)
		}
		return nil
	}
	if !hasProfiles {
		return errors.New("default_permissions requires a `[permissions]` table")
	}
	if _, ok := profiles[profileID]; !ok {
		return fmt.Errorf("default_permissions refers to undefined profile `%s`", profileID)
	}
	return nil
}

// validateModelProviderSelection mirrors Rust's provider lookup: built-in IDs
// plus any `[model_providers.<id>]` block, with the retired ollama-chat ID
// carrying its own migration message.
func validateModelProviderSelection(values map[string]any) error {
	raw, ok := values["model_provider"]
	if !ok {
		return nil
	}
	providerID, _ := raw.(string)
	if providerID == model.LegacyOllamaChatProviderID {
		return errors.New(model.OllamaChatProviderRemovedMessage)
	}
	providers := model.BuiltInProviders("")
	if configured, ok := values["model_providers"].(map[string]any); ok {
		for name := range configured {
			if _, exists := providers[name]; !exists {
				providers[name] = model.ProviderInfo{}
			}
		}
	}
	if _, exists := providers[providerID]; !exists {
		return fmt.Errorf("Model provider `%s` not found", providerID)
	}
	return nil
}
