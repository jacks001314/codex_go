package app

import (
	"strings"

	"codex_go/auth"
	"codex_go/cli"
	"codex_go/config"
	"codex_go/features"
)

// Rust #49799 (606b139565, codex-rs/tui/src/app_server_session/web_search.rs):
// the TUI must not unconditionally forward its resolved web-search mode, because
// an implicit client setting would then replace the destination server's default
// or a saved thread's setting. Only a winning launch choice travels with the
// request:
//
//   - a canonical `web_search` value whose winning layer is a launch origin
//     (session flags or a selected profile), or
//   - a legacy `[features] web_search*` flag supplied by a launch origin,
//     translated into the canonical `web_search` override.
//
// Losing legacy flags are dropped from the forwarded feature table so they cannot
// independently change the destination's search mode. Rust tests:
// codex-rs/tui/src/app_server_session/web_search_tests.rs.

// webSearchParamsMode mirrors Rust tui::app_server_session::ThreadParamsMode for
// the search override: an embedded session honors the shared feature
// requirements, while a remote server enforces its own requirements on the raw
// choice.
type webSearchParamsMode int

const (
	webSearchParamsEmbedded webSearchParamsMode = iota
	webSearchParamsRemote
)

// webSearchLegacyFeatureKeys are the legacy search flags Rust #49799 retires in
// favor of the canonical `web_search` setting.
var webSearchLegacyFeatureKeys = []string{"web_search", "web_search_cached", "web_search_request"}

// webSearchLaunchView is the resolved configuration one launch decision reads:
// the effective values, the layer stack that supplies their origins, the launch
// override paths (`-c` keys and `--enable`/`--disable` features), the launch's
// `--search` flag, and the resolved search feature state (which already reflects
// the shared feature requirements).
type webSearchLaunchView struct {
	values     map[string]any
	layers     []config.Layer
	cliKeys    []string
	searchFlag bool
	cached     bool
	live       bool
}

// loadWebSearchLaunchView mirrors the loading half of Rust #50811's
// config_request_overrides_from_config: the effective values carry the `-c`
// overrides and the selected profile, the layer stack identifies which of them
// were launch choices, and the feature settings carry the requirement-merged
// search feature state (Rust `config.features`).
func loadWebSearchLaunchView(root *cli.RootOptions) *webSearchLaunchView {
	loaded, err := config.LoadEffectiveWithOptions(auth.DefaultCodexHome(), interactiveKeymapLoadOptions(root))
	if err != nil || loaded == nil {
		return nil
	}
	read, err := interactiveConfigService(root).Read(&config.ConfigReadParams{IncludeLayers: true})
	if err != nil || read == nil {
		return nil
	}
	settings := loaded.FeatureSettings()
	return &webSearchLaunchView{
		values:     loaded.Values,
		layers:     read.Layers,
		cliKeys:    append(remoteCLIConfigOverrideKeys(root), featureToggleConfigPaths(root)...),
		searchFlag: root != nil && root.Shared.Search,
		cached:     features.Enabled(settings, "web_search_cached"),
		live:       features.Enabled(settings, "web_search_request"),
	}
}

// featureToggleConfigPaths renders the CLI `--enable` / `--disable` feature
// flags as the `features.<name>` config paths Rust records in its session-flags
// layer, so they count as launch origins like a generic `-c` override.
func featureToggleConfigPaths(root *cli.RootOptions) []string {
	if root == nil {
		return nil
	}
	paths := make([]string, 0, len(root.EnableFeatures)+len(root.DisableFeatures))
	for _, feature := range append(append([]string(nil), root.EnableFeatures...), root.DisableFeatures...) {
		if name := strings.TrimSpace(feature); name != "" {
			paths = append(paths, "features."+name)
		}
	}
	return paths
}

// isLaunch ports Rust #49799's `is_launch`: the winning layer must be session
// flags or a selected profile. `--search` is Go's session-flags origin for the
// canonical `web_search` setting, mirroring the Rust CLI.
func (v *webSearchLaunchView) isLaunch(key string) bool {
	if v == nil {
		return false
	}
	if key == "web_search" && v.searchFlag {
		return true
	}
	return launchSettingForKey(v.layers, v.cliKeys, key)
}

// canonicalWebSearch returns the effective canonical `web_search` value. The
// launch's `--search` flag is recorded as a session-flags setting, which outranks
// every file layer, so it wins over a configured value.
func (v *webSearchLaunchView) canonicalWebSearch() (string, bool) {
	if v == nil {
		return "", false
	}
	if v.searchFlag {
		return "live", true
	}
	raw, present := v.values["web_search"]
	if !present || raw == nil {
		return "", false
	}
	text, ok := raw.(string)
	if !ok {
		return "", false
	}
	if text = strings.TrimSpace(text); text == "" {
		return "", false
	}
	return text, true
}

// resolveWebSearchLaunchChoice mirrors the precedence in Rust #49799's
// apply_launch_override. It reports the canonical `web_search` value to forward,
// or false when the destination keeps its own default or saved thread setting.
func (v *webSearchLaunchView) resolveWebSearchLaunchChoice(mode webSearchParamsMode) (string, bool) {
	if v == nil || v.values == nil {
		return "", false
	}
	// A canonical setting always takes precedence, even when its origin is implicit.
	if choice, present := v.canonicalWebSearch(); present {
		if v.isLaunch("web_search") {
			return choice, true
		}
		return "", false
	}
	features, _ := v.values["features"].(map[string]any)
	if features == nil {
		return "", false
	}
	requestKey := "web_search"
	if _, present := features["web_search_request"]; present {
		requestKey = "web_search_request"
	}
	key, choice := "", ""
	for _, candidate := range []struct {
		key  string
		mode string
	}{
		{"web_search_cached", "cached"},
		{requestKey, "live"},
	} {
		if !webSearchFeatureFlag(features[candidate.key], true) {
			continue
		}
		// An embedded session keeps the requirement-merged state Rust uses; a
		// remote server enforces its own requirements on the raw choice.
		if mode == webSearchParamsRemote || webSearchFeatureActive(candidate.mode, v.cached, v.live) {
			key, choice = candidate.key, candidate.mode
			break
		}
	}
	if key == "" {
		if mode == webSearchParamsEmbedded && (v.cached || v.live) {
			return "", false
		}
		// Explicitly disabling every legacy search feature falls back to cached.
		for _, candidate := range []string{"web_search_cached", requestKey} {
			if !webSearchFeatureFlag(features[candidate], false) || !v.isLaunch("features."+candidate) {
				continue
			}
			key, choice = candidate, "cached"
			break
		}
		if key == "" {
			return "", false
		}
	}
	if v.isLaunch("features."+key) ||
		(choice == "live" && webSearchFeatureFlag(features["web_search_cached"], false) && v.isLaunch("features.web_search_cached")) {
		return choice, true
	}
	return "", false
}

// webSearchFeatureFlag reports whether a feature table entry is exactly the
// boolean `want` (Rust compares `toml::Value::as_bool()`); an absent key or
// another shape never matches.
func webSearchFeatureFlag(value any, want bool) bool {
	enabled, ok := value.(bool)
	return ok && enabled == want
}

// webSearchFeatureActive reports whether the requirement-merged feature state
// makes a legacy mode active for an embedded session.
func webSearchFeatureActive(mode string, cached, live bool) bool {
	if mode == "cached" {
		return cached
	}
	return live && !cached
}

// stripLegacyWebSearchFeatures drops the losing legacy search flags from a
// forwarded `features` table, removing the table when nothing else remains
// (Rust #49799 removes the same keys from the built overrides).
func stripLegacyWebSearchFeatures(values map[string]any) {
	if values == nil {
		return
	}
	features, ok := values["features"].(map[string]any)
	if !ok {
		return
	}
	for _, key := range webSearchLegacyFeatureKeys {
		delete(features, key)
	}
	if len(features) == 0 {
		delete(values, "features")
	}
}

// applyWebSearchLaunchOverride mirrors Rust #49799's apply_launch_override on the
// request's forwarded override table: the losing legacy flags are dropped and a
// winning launch choice becomes the canonical `web_search` value.
func applyWebSearchLaunchOverride(root *cli.RootOptions, mode webSearchParamsMode, values map[string]any) {
	if values == nil {
		return
	}
	view := loadWebSearchLaunchView(root)
	stripLegacyWebSearchFeatures(values)
	if choice, ok := view.resolveWebSearchLaunchChoice(mode); ok {
		values["web_search"] = choice
	}
}

// interactiveLaunchWebSearchOverrides mirrors the embedded half of Rust #49799
// (config_request_overrides_from_config with ThreadParamsMode::Embedded): only a
// launch-origin search choice is forwarded as the canonical `web_search`
// override, so the embedded runner and the model keep their defaults otherwise.
func interactiveLaunchWebSearchOverrides(root *cli.RootOptions) []string {
	view := loadWebSearchLaunchView(root)
	choice, ok := view.resolveWebSearchLaunchChoice(webSearchParamsEmbedded)
	if !ok {
		return nil
	}
	return []string{"web_search=" + choice}
}
