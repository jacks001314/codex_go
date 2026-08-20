package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	codexplugin "codex_go/plugin"

	"github.com/pelletier/go-toml/v2"
)

const (
	externalKnownMarketplacesPath       = "plugins/known_marketplaces.json"
	externalOfficialMarketplaceName     = "claude-plugins-official"
	externalOfficialMarketplaceSource   = "anthropics/claude-plugins-official"
	externalClaudeCodeMarketplaceName   = "claude-code-plugins"
	externalClaudeCodeMarketplaceSource = "anthropics/claude-code"
)

type externalMarketplaceImportSource struct {
	Source  string
	RefName *string
}

type externalPluginID struct {
	Name        string
	Marketplace string
}

func (s *ConfigService) detectExternalPluginsMigration(scope externalMigrationScope) (ExternalAgentConfigMigrationItem, bool) {
	// Rust #39663: plugin imports persist user-global enabled state, so
	// repository-controlled settings must never be plugin installation
	// authority.
	if !scope.home() {
		return ExternalAgentConfigMigrationItem{}, false
	}
	settingsPath := s.externalSettingsPath(scope)
	settings, ok := readEffectiveClaudeSettings(settingsPath)
	if !ok {
		return ExternalAgentConfigMigrationItem{}, false
	}
	sources := s.externalMarketplaceImportSources(scope, settings)
	configuredIDs, configuredMarketplaces := s.externalConfiguredPlugins()
	groups := map[string][]string{}
	enabledPlugins, _ := settings["enabledPlugins"].(map[string]any)
	for key, enabled := range enabledPlugins {
		if enabled != true || configuredIDs[key] {
			continue
		}
		pluginID, ok := parseExternalPluginID(key)
		if !ok {
			continue
		}
		if available, configured := configuredMarketplaces[pluginID.Marketplace]; configured {
			if !available[pluginID.Name] {
				continue
			}
		} else {
			source, found := sources[pluginID.Marketplace]
			if !found {
				continue
			}
			if _, err := codexplugin.ParseMarketplaceSource(source.Source, source.RefName); err != nil {
				continue
			}
		}
		groups[pluginID.Marketplace] = append(groups[pluginID.Marketplace], pluginID.Name)
	}
	if len(groups) == 0 {
		return ExternalAgentConfigMigrationItem{}, false
	}
	details := NewMigrationDetails()
	for _, marketplace := range sortedExternalMapKeys(groups) {
		pluginNames := groups[marketplace]
		sort.Strings(pluginNames)
		details.Plugins = append(details.Plugins, PluginMigration{MarketplaceName: marketplace, PluginNames: pluginNames})
	}
	return ExternalAgentConfigMigrationItem{
		ItemType:    MigrationPlugins,
		Description: fmt.Sprintf("Migrate enabled plugins from %s", settingsPath),
		CWD:         externalScopeCWD(scope),
		Details:     details,
	}, true
}

func (s *ConfigService) importExternalPluginsMigration(item ExternalAgentConfigMigrationItem) ExternalAgentConfigImportTypeResult {
	result := ExternalAgentConfigImportTypeResult{
		ItemType:  MigrationPlugins,
		Successes: []ExternalAgentConfigImportItemTypeSuccess{},
		Failures:  []ExternalAgentConfigImportItemTypeFailure{},
	}
	if item.Details == nil {
		return externalCoreImportFailure(result, item, "plugin_import", fmt.Errorf("plugins migration item is missing details"))
	}
	scope, ok := externalScopeFromCWD(item.CWD)
	if !ok {
		return externalCoreImportFailure(result, item, "scope_resolution", fmt.Errorf("migration working directory does not exist"))
	}
	if !scope.home() {
		return externalCoreImportFailure(result, item, "plugin_import", fmt.Errorf("repository-scoped plugin migration is not allowed"))
	}
	settings, _ := readEffectiveClaudeSettings(s.externalSettingsPath(scope))
	sources := s.externalMarketplaceImportSources(scope, settings)
	service := codexplugin.NewPluginService()
	service.SetCodexHome(s.codexHome)
	configuredMarketplaces := map[string]bool{}
	for _, marketplace := range service.List(&codexplugin.PluginListParams{IncludeInstalled: true}).Marketplaces {
		configuredMarketplaces[marketplace.Name] = true
	}

	for _, group := range item.Details.Plugins {
		marketplace := strings.TrimSpace(group.MarketplaceName)
		if marketplace == "" {
			for _, pluginName := range group.PluginNames {
				appendExternalPluginFailure(&result, item.CWD, strings.TrimSpace(pluginName), "plugin marketplace name is required")
			}
			continue
		}
		if !configuredMarketplaces[marketplace] {
			source, found := sources[marketplace]
			if !found {
				for _, pluginName := range group.PluginNames {
					pluginID := strings.TrimSpace(pluginName) + "@" + marketplace
					appendExternalPluginFailure(&result, item.CWD, pluginID, "external agent plugin marketplace source was not found: "+marketplace)
				}
				continue
			}
			if _, err := service.AddMarketplace(&codexplugin.MarketplaceAddParams{Name: marketplace, Source: source.Source, RefName: source.RefName}); err != nil {
				for _, pluginName := range group.PluginNames {
					pluginID := strings.TrimSpace(pluginName) + "@" + marketplace
					appendExternalPluginFailure(&result, item.CWD, pluginID, err.Error())
				}
				continue
			}
			configuredMarketplaces[marketplace] = true
		}
		for _, pluginName := range group.PluginNames {
			pluginName = strings.TrimSpace(pluginName)
			pluginID := pluginName + "@" + marketplace
			if _, ok := parseExternalPluginID(pluginID); !ok {
				appendExternalPluginFailure(&result, item.CWD, pluginID, "invalid plugin id")
				continue
			}
			installed, err := service.Install(&codexplugin.PluginInstallParams{PluginID: pluginID})
			if err != nil {
				appendExternalPluginFailure(&result, item.CWD, pluginID, err.Error())
				continue
			}
			target := pluginID
			if installed != nil && strings.TrimSpace(installed.PluginID) != "" {
				target = strings.TrimSpace(installed.PluginID)
			}
			result.Successes = append(result.Successes, externalMigrationSuccess(MigrationPlugins, pluginID, target, externalScopeCWD(scope)))
		}
	}
	return result
}

func appendExternalPluginFailure(result *ExternalAgentConfigImportTypeResult, cwd *string, pluginID string, message string) {
	errorType := "plugin_import"
	if strings.Contains(strings.ToLower(message), "not found") {
		errorType = "plugin_not_found"
	}
	result.Failures = append(result.Failures, ExternalAgentConfigImportItemTypeFailure{
		ItemType:     MigrationPlugins,
		ErrorType:    &errorType,
		FailureStage: "plugin_import",
		Message:      message,
		CWD:          cloneStringPtr(cwd),
		Source:       stringPtrIfNotEmpty(pluginID),
	})
}

func (s *ConfigService) externalConfiguredPlugins() (map[string]bool, map[string]map[string]bool) {
	configuredIDs := map[string]bool{}
	if data, err := os.ReadFile(filepath.Join(s.codexHome, "config.toml")); err == nil {
		values := map[string]any{}
		if toml.Unmarshal(stripUTF8BOM(data), &values) == nil {
			if plugins, ok := values["plugins"].(map[string]any); ok {
				for pluginID := range plugins {
					configuredIDs[pluginID] = true
				}
			}
		}
	}
	service := codexplugin.NewPluginService()
	service.SetCodexHome(s.codexHome)
	configuredMarketplaces := map[string]map[string]bool{}
	for _, marketplace := range service.List(&codexplugin.PluginListParams{IncludeInstalled: true}).Marketplaces {
		available := map[string]bool{}
		for _, plugin := range marketplace.Plugins {
			if plugin.InstallPolicy != codexplugin.InstallBlocked {
				available[plugin.Name] = true
			}
		}
		configuredMarketplaces[marketplace.Name] = available
	}
	return configuredIDs, configuredMarketplaces
}

func (s *ConfigService) externalMarketplaceImportSources(scope externalMigrationScope, settings map[string]any) map[string]externalMarketplaceImportSource {
	known, _ := readJSONObject(filepath.Join(s.externalAgentHome, externalKnownMarketplacesPath))
	sources := collectExternalMarketplaceSources(known, s.externalAgentHome)
	sourceRoot := s.externalAgentHome
	if !scope.home() {
		sourceRoot = scope.repoRoot
	}
	if extras, ok := settings["extraKnownMarketplaces"].(map[string]any); ok {
		for name, raw := range extras {
			delete(sources, name)
			extra, _ := raw.(map[string]any)
			knownEntry, _ := known[name].(map[string]any)
			if extra != nil && knownEntry != nil && reflect.DeepEqual(extra["source"], knownEntry["source"]) {
				if installLocation := externalString(knownEntry["installLocation"]); installLocation != "" {
					path := installLocation
					if !filepath.IsAbs(path) {
						path = filepath.Join(s.externalAgentHome, path)
					}
					if info, err := os.Stat(path); err == nil && info.IsDir() {
						sources[name] = externalMarketplaceImportSource{Source: filepath.Clean(path)}
						continue
					}
				}
			}
			if source, ok := collectExternalMarketplaceSource(extra, sourceRoot); ok {
				sources[name] = source
			}
		}
	}
	for name, source := range map[string]string{
		externalOfficialMarketplaceName:   externalOfficialMarketplaceSource,
		externalClaudeCodeMarketplaceName: externalClaudeCodeMarketplaceSource,
	} {
		if _, found := sources[name]; !found && externalSettingsEnableMarketplace(settings, name) {
			sources[name] = externalMarketplaceImportSource{Source: source}
		}
	}
	return sources
}

func collectExternalMarketplaceSources(values map[string]any, sourceRoot string) map[string]externalMarketplaceImportSource {
	out := map[string]externalMarketplaceImportSource{}
	for name, raw := range values {
		entry, _ := raw.(map[string]any)
		if source, ok := collectExternalMarketplaceSource(entry, sourceRoot); ok {
			out[name] = source
		}
	}
	return out
}

func collectExternalMarketplaceSource(value map[string]any, sourceRoot string) (externalMarketplaceImportSource, bool) {
	if value == nil {
		return externalMarketplaceImportSource{}, false
	}
	fields := value
	if nested, ok := value["source"].(map[string]any); ok {
		fields = nested
	}
	kind := strings.TrimSpace(externalString(fields["source"]))
	var declared string
	switch kind {
	case "github":
		declared = externalString(fields["repo"])
	case "git":
		declared = externalString(fields["url"])
	case "directory", "local":
		declared = externalString(fields["path"])
	case "file", "url", "npm", "settings":
		declared = ""
	default:
		declared = externalString(fields["source"])
		if declared == "" {
			for _, key := range []string{"repo", "url", "path"} {
				if declared = externalString(fields[key]); declared != "" {
					break
				}
			}
			if declared == "" {
				declared = externalString(value["source"])
			}
		}
	}
	declared = strings.TrimSpace(declared)
	if declared != "" {
		if (kind == "directory" || kind == "local" || externalLooksRelativeLocalPath(declared)) && !filepath.IsAbs(declared) {
			declared = filepath.Join(sourceRoot, declared)
		}
		refName := stringPtrIfNotEmpty(externalString(fields["ref"]))
		if refName == nil {
			refName = stringPtrIfNotEmpty(externalString(value["ref"]))
		}
		return externalMarketplaceImportSource{Source: declared, RefName: refName}, true
	}
	if installLocation := strings.TrimSpace(externalString(value["installLocation"])); installLocation != "" {
		path := installLocation
		if !filepath.IsAbs(path) {
			path = filepath.Join(sourceRoot, path)
		}
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			return externalMarketplaceImportSource{Source: filepath.Clean(path)}, true
		}
	}
	return externalMarketplaceImportSource{}, false
}

func externalSettingsEnableMarketplace(settings map[string]any, marketplace string) bool {
	enabled, _ := settings["enabledPlugins"].(map[string]any)
	for key, value := range enabled {
		pluginID, ok := parseExternalPluginID(key)
		if ok && value == true && pluginID.Marketplace == marketplace {
			return true
		}
	}
	return false
}

func parseExternalPluginID(value string) (externalPluginID, bool) {
	index := strings.LastIndex(value, "@")
	if index <= 0 || index == len(value)-1 {
		return externalPluginID{}, false
	}
	id := externalPluginID{Name: value[:index], Marketplace: value[index+1:]}
	if !externalValidPluginSegment(id.Name) || !externalValidPluginSegment(id.Marketplace) {
		return externalPluginID{}, false
	}
	return id, true
}

func externalValidPluginSegment(value string) bool {
	if value == "" {
		return false
	}
	for _, ch := range value {
		if ch > 127 || !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '-') {
			return false
		}
	}
	return true
}

func externalLooksRelativeLocalPath(value string) bool {
	return value == "." || value == ".." || strings.HasPrefix(value, "./") || strings.HasPrefix(value, "../") || strings.HasPrefix(value, `.\`) || strings.HasPrefix(value, `..\`)
}
