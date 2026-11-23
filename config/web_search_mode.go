package config

import (
	"codex_go/codexapi"
	featureflags "codex_go/features"
)

// Rust #62894 (codex-rs/core/src/config/mod.rs resolve_web_search_mode and
// resolve_web_search_mode_for_turn): the configured web search preference is
// resolved once from the explicit setting, the legacy feature flags and the
// managed requirement default, and the per-turn value is then derived from the
// turn's permission profile and the provider capabilities.

// WebSearchModePreference resolves the configured preference or selects an
// allowed implicit default. The explicit `web_search` setting always wins, the
// legacy `web_search_cached` / `web_search_request` flags follow in that order,
// and an implicit cached default upgrades to indexed when
// `features.web_search_indexed_default` is enabled and managed requirements
// allow indexed search.
func WebSearchModePreference(values map[string]any, featureSettings map[string]bool, allowed []WebSearchMode) codexapi.WebSearchMode {
	if values != nil {
		if raw, ok := values["web_search"]; ok && raw != nil {
			return codexapi.WebSearchModeFromValue(raw)
		}
	}
	if featureflags.Enabled(featureSettings, "web_search_cached") {
		return codexapi.WebSearchModeCached
	}
	if featureflags.Enabled(featureSettings, "web_search_request") {
		return codexapi.WebSearchModeLive
	}
	defaultMode := WebSearchModeConstrainedDefault(allowed)
	if defaultMode == codexapi.WebSearchModeCached &&
		featureflags.Enabled(featureSettings, "web_search_indexed_default") &&
		WebSearchModeCanSet(codexapi.WebSearchModeIndexed, allowed) {
		return codexapi.WebSearchModeIndexed
	}
	return defaultMode
}

// WebSearchModePreference returns the configured preference for this config.
func (c *Config) WebSearchModePreference() codexapi.WebSearchMode {
	if c == nil {
		return codexapi.WebSearchModeCached
	}
	return WebSearchModePreference(c.Values, c.FeatureSettings(), c.AllowedWebSearchModes())
}

// AllowedWebSearchModes returns the managed requirement allow-list, or nil when
// requirements do not constrain web search.
func (c *Config) AllowedWebSearchModes() []WebSearchMode {
	if c == nil || c.Requirements == nil {
		return nil
	}
	return c.Requirements.AllowedWebSearchModes
}

// WebSearchModeWasExplicit mirrors Rust Config::web_search_mode_was_explicit: a
// value counts as explicit when the raw configuration selected it and the
// managed constraint kept it. Configurations that do not travel through the
// loader fall back to the presence of the effective `web_search` value.
func (c *Config) WebSearchModeWasExplicit() bool {
	if c == nil {
		return false
	}
	if c.webSearchModeExplicitKnown {
		return c.webSearchModeWasExplicit
	}
	if c.Values == nil {
		return false
	}
	raw, ok := c.Values["web_search"]
	return ok && raw != nil
}

// WebSearchModeConstrainedDefault mirrors the default of Rust's
// Constrained<WebSearchMode>: without managed requirements it is cached, with an
// allow-list it is the preferred allowed mode (cached, then indexed, then live,
// else disabled).
func WebSearchModeConstrainedDefault(allowed []WebSearchMode) codexapi.WebSearchMode {
	if allowed == nil {
		return codexapi.WebSearchModeCached
	}
	accepted := map[codexapi.WebSearchMode]bool{codexapi.WebSearchModeDisabled: true}
	for _, mode := range allowed {
		accepted[codexapi.WebSearchModeFromValue(string(mode))] = true
	}
	for _, candidate := range []codexapi.WebSearchMode{
		codexapi.WebSearchModeCached,
		codexapi.WebSearchModeIndexed,
		codexapi.WebSearchModeLive,
	} {
		if accepted[candidate] {
			return candidate
		}
	}
	return codexapi.WebSearchModeDisabled
}

// WebSearchModeCanSet mirrors Constrained<WebSearchMode>::can_set: disabled is
// always accepted and every other mode must appear in the managed allow-list.
func WebSearchModeCanSet(mode codexapi.WebSearchMode, allowed []WebSearchMode) bool {
	if allowed == nil || mode == codexapi.WebSearchModeDisabled {
		return true
	}
	for _, candidate := range allowed {
		if codexapi.WebSearchModeFromValue(string(candidate)) == mode {
			return true
		}
	}
	return false
}

// ResolveWebSearchModeForTurn mirrors Rust resolve_web_search_mode_for_turn: a
// full-access turn promotes cached/live to live when the provider and the
// managed requirements allow it, an implicit indexed default follows that same
// promotion, and an explicit indexed selection keeps its precedence. Modes the
// provider cannot use (live or indexed without external web access) fall back
// to cached, then live, then disabled.
func ResolveWebSearchModeForTurn(preferred codexapi.WebSearchMode, wasExplicit, fullAccess bool, allowed []WebSearchMode, externalWebAccess bool) codexapi.WebSearchMode {
	isAllowed := func(mode codexapi.WebSearchMode) bool {
		providerSupportsMode := mode != codexapi.WebSearchModeLive && mode != codexapi.WebSearchModeIndexed || externalWebAccess
		return providerSupportsMode && WebSearchModeCanSet(mode, allowed)
	}
	fullAccessPromotesToLive := fullAccess
	if fullAccessPromotesToLive {
		switch preferred {
		case codexapi.WebSearchModeCached, codexapi.WebSearchModeLive:
		case codexapi.WebSearchModeIndexed:
			fullAccessPromotesToLive = !wasExplicit && WebSearchModeCanSet(codexapi.WebSearchModeCached, allowed)
		default:
			fullAccessPromotesToLive = false
		}
	}
	if fullAccessPromotesToLive && isAllowed(codexapi.WebSearchModeLive) {
		return codexapi.WebSearchModeLive
	}
	if isAllowed(preferred) {
		return preferred
	}
	for _, mode := range []codexapi.WebSearchMode{
		codexapi.WebSearchModeCached,
		codexapi.WebSearchModeLive,
		codexapi.WebSearchModeDisabled,
	} {
		if isAllowed(mode) {
			return mode
		}
	}
	return codexapi.WebSearchModeDisabled
}
