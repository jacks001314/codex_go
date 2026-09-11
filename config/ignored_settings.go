package config

// Rust parity: codex-rs/config/src/strict_config.rs ignored_config_warning
// (#44691). Unrecognized settings in the effective configuration and
// requirements layers are surfaced as a bounded startup/project warning.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	featureflags "codex_go/features"
)

const ignoredConfigLabelLimit = 200

// knownRequirementsTopLevelFields lists the requirements.toml top-level keys
// recognized by configRequirementsFromMap (snake_case and camelCase aliases).
// A key outside this set is ignored by the requirements parser, so warning about
// it cannot produce a false positive relative to Go's own behavior.
var knownRequirementsTopLevelFields = func() map[string]bool {
	keys := []string{
		"allowed_approval_policies", "allowedApprovalPolicies",
		"allowed_approvals_reviewers", "allowedApprovalsReviewers",
		"allowed_sandbox_modes", "allowedSandboxModes",
		"allowed_windows_sandbox_implementations", "allowedWindowsSandboxImplementations",
		"allowed_permission_profiles", "allowedPermissionProfiles",
		"default_permissions", "defaultPermissions",
		"additional_developer_instructions", "additionalDeveloperInstructions",
		"permissions",
		"allowed_web_search_modes", "allowedWebSearchModes",
		"allow_managed_hooks_only", "allowManagedHooksOnly",
		"allow_browser_and_computer_use", "allowBrowserAndComputerUse",
		"allow_appshots", "allowAppshots",
		"allow_remote_control", "allowRemoteControl",
		"computer_use", "computerUse",
		"allowed_login_methods", "allowedLoginMethods",
		"allowed_chatgpt_workspaces", "allowedChatGPTWorkspaces",
		"cli_auth_credentials_store", "cliAuthCredentialsStore",
		"chatgpt_base_url", "chatgptBaseUrl",
		"model_provider", "modelProvider",
		"model_providers", "modelProviders",
		"browser_use", "browserUse",
		"in_app_browser", "inAppBrowser",
		"auto_review", "autoReview",
		"feature_requirements", "featureRequirements", "features",
		"hooks",
		"enforce_residency", "enforceResidency",
		"experimental_network",
		"application",
		"models",
		"mcp_servers", "mcpServers",
		"plugins",
	}
	out := make(map[string]bool, len(keys))
	for _, key := range keys {
		out[key] = true
	}
	return out
}()

// IgnoredConfigWarning mirrors Rust ignored_config_warning: it collects
// unrecognized fields from the merged configuration (plus unknown feature keys)
// and from the raw requirements values, attributes each to a source layer, and
// returns a bounded warning. Values are never included. Returns "" when nothing
// is ignored.
func IgnoredConfigWarning(layers []Layer, rawRequirements map[string]any) string {
	type entry struct {
		source string
		key    string
	}
	seen := map[string]bool{}
	entries := []entry{}
	add := func(source string, key string) {
		signature := source + "\x00" + key
		if seen[signature] {
			return
		}
		seen[signature] = true
		entries = append(entries, entry{source: source, key: key})
	}

	// Validate the merged config so incomplete layer fragments (e.g. a provider
	// override) do not hide diagnostics.
	merged := mergedConfigValues(layers)
	for _, path := range unknownConfigFieldPaths(merged) {
		key := strings.Join(path, ".")
		for _, layer := range layersHighToLow(layers) {
			if configPathExists(layerConfigMap(layer), path) {
				add(configLayerSourceLabel(layer.Name), key)
			}
		}
	}
	for _, key := range sortedAnyKeys(rawRequirements) {
		if !knownRequirementsTopLevelFields[key] {
			add("requirements (requirements.toml)", key)
		}
	}
	if len(entries) == 0 {
		return ""
	}
	sort.SliceStable(entries, func(i int, j int) bool {
		if entries[i].source != entries[j].source {
			return entries[i].source < entries[j].source
		}
		return entries[i].key < entries[j].key
	})

	count := len(entries)
	setting := "settings"
	if count == 1 {
		setting = "setting"
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "Codex is ignoring %d unrecognized configuration %s. Check for typos or deprecated settings.", count, setting)
	limit := 3
	if count < limit {
		limit = count
	}
	for _, item := range entries[:limit] {
		fmt.Fprintf(&builder, "\n  %s: `%s` is ignored.%s",
			boundedConfigLabel(item.source), boundedConfigLabel(item.key), ignoredSettingHint(item.key))
	}
	if count > 3 {
		fmt.Fprintf(&builder, "\n  ... and %d more ignored settings.", count-3)
	}
	return builder.String()
}

// mergedConfigValues deep-merges every layer (low to high precedence).
func mergedConfigValues(layers []Layer) map[string]any {
	merged := map[string]any{}
	for _, layer := range layersLowToHigh(layers) {
		values := layerConfigMap(layer)
		if values == nil {
			continue
		}
		cloudConfigMergeMap(merged, values)
	}
	return merged
}

func unknownConfigFieldPaths(values map[string]any) [][]string {
	if values == nil {
		return nil
	}
	out := [][]string{}
	for _, key := range sortedAnyKeys(values) {
		if _, ok := knownTopLevelConfigFields[key]; !ok {
			out = append(out, []string{key})
		}
	}
	out = append(out, unknownFeatureFieldPaths(values["features"], []string{"features"})...)
	if profiles, ok := values["profiles"].(map[string]any); ok {
		for _, name := range sortedAnyKeys(profiles) {
			profile, ok := profiles[name].(map[string]any)
			if !ok {
				continue
			}
			out = append(out, unknownFeatureFieldPaths(profile["features"], []string{"profiles", name, "features"})...)
		}
	}
	sort.SliceStable(out, func(i int, j int) bool {
		return strings.Join(out[i], ".") < strings.Join(out[j], ".")
	})
	return out
}

func unknownFeatureFieldPaths(value any, prefix []string) [][]string {
	features, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	out := [][]string{}
	for _, key := range sortedAnyKeys(features) {
		if featureflags.Known(key) || key == "tool_registry" {
			continue
		}
		path := append(append([]string(nil), prefix...), key)
		out = append(out, path)
	}
	return out
}

func configPathExists(values map[string]any, path []string) bool {
	if values == nil || len(path) == 0 {
		return false
	}
	current := any(values)
	for _, segment := range path {
		table, ok := current.(map[string]any)
		if !ok {
			return false
		}
		next, ok := table[segment]
		if !ok {
			return false
		}
		current = next
	}
	return true
}

func sortedAnyKeys(values map[string]any) []string {
	if values == nil {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// ignoredSettingHint mirrors the fixed migration hints from Rust #44691.
func ignoredSettingHint(key string) string {
	switch key {
	case "network_proxy":
		return " Use [permissions.<name>.network] for network settings, or [experimental_network] in requirements.toml for enforced network policy."
	case "allowed_permissions":
		return " Use [allowed_permission_profiles] and default_permissions; the old allowlist is ignored even when both forms are present."
	case "include_view_image_tool", "features.include_view_image_tool":
		return " Use [features].view_image to configure the image tool."
	}
	segments := strings.Split(key, ".")
	if len(segments) >= 2 && segments[0] == "profiles" && segments[len(segments)-1] == "include_view_image_tool" {
		return " Use [features].view_image to configure the image tool."
	}
	return ""
}

// boundedConfigLabel mirrors Rust bounded_label: control characters are escaped
// and the result is capped at 200 bytes with a trailing ellipsis.
func boundedConfigLabel(value string) string {
	var builder strings.Builder
	for _, character := range value {
		if builder.Len() >= ignoredConfigLabelLimit {
			builder.WriteString("…")
			break
		}
		if character < 0x20 || character == 0x7f {
			fmt.Fprintf(&builder, "\\u{%x}", character)
			continue
		}
		builder.WriteRune(character)
	}
	return builder.String()
}

// configLayerSourceLabel renders a human-readable source label for a layer.
func configLayerSourceLabel(source LayerSource) string {
	switch source.Type {
	case LayerSourcePackagedDefaults:
		return "packaged defaults"
	case LayerSourceMDM:
		return "managed configuration (MDM)"
	case LayerSourceSystem:
		return "system"
	case LayerSourceEnterpriseManaged:
		if name := strings.TrimSpace(source.Name); name != "" {
			return "enterprise managed (" + name + ")"
		}
		return "enterprise managed"
	case LayerSourceUser:
		return "user"
	case LayerSourceProject:
		return "project"
	case LayerSourceSessionFlags:
		return "session flags"
	case LayerSourceLegacyManagedConfigFromFile:
		return "managed configuration (file)"
	case LayerSourceLegacyManagedConfigFromMDM:
		return "managed configuration (MDM)"
	default:
		return string(source.Type)
	}
}

// IgnoredSettingsWarning returns the ignored-configuration warning for the
// layers visible from cwd (empty selects the startup layers: packaged defaults,
// user, and managed configuration). Returns "" when nothing is ignored.
func (s *ConfigService) IgnoredSettingsWarning(cwd string) string {
	if s == nil {
		return ""
	}
	layers, err := s.configLayersForWarning(cwd)
	if err != nil {
		return ""
	}
	return IgnoredConfigWarning(layers, s.rawRequirementsValues())
}

func (s *ConfigService) configLayersForWarning(cwd string) ([]Layer, error) {
	profile := s.currentProfile()
	if strings.TrimSpace(cwd) != "" {
		return s.readLayersForCWD(cwd, profile)
	}
	userConfigPath, err := s.currentUserConfigPath()
	if err != nil {
		return nil, err
	}
	userValues, err := loadConfigFile(ConfigPath(s.codexHome))
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(profile) != "" {
		if err := applyProfileLayer(s.codexHome, userValues, profile); err != nil {
			return nil, err
		}
	}
	userLayer := Layer{
		Name:    LayerSource{Type: LayerSourceUser, File: userConfigPath, Profile: stringPtrIfNotEmpty(profile)},
		Version: configVersion(userValues),
		Config:  cloneMap(userValues),
	}
	return s.readLayers(userLayer), nil
}

func (s *ConfigService) rawRequirementsValues() map[string]any {
	if s == nil {
		return nil
	}
	home := strings.TrimSpace(s.codexHome)
	if home == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(home, "requirements.toml"))
	if err != nil {
		return nil
	}
	values, err := parseRequirementsTOMLValues(data)
	if err != nil {
		return nil
	}
	return values
}
