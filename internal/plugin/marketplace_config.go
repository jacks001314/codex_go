package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

const ConfigTOMLFilename = "config.toml"

func (s *PluginService) SetCodexHome(codexHome string) {
	codexHome = strings.TrimSpace(codexHome)
	if codexHome == "" {
		return
	}
	s.SetMarketplaceInstallRoot(filepath.Join(codexHome, InstalledMarketplacesDir))
	s.SetMarketplaceConfigPath(filepath.Join(codexHome, ConfigTOMLFilename))
}

func (s *PluginService) SetMarketplaceConfigPath(path string) {
	s.mu.Lock()
	s.marketplaceConfigPath = strings.TrimSpace(path)
	s.mu.Unlock()
	_ = s.ReloadConfig()
}

func (s *PluginService) ReloadConfig() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	configPath := s.marketplaceConfigPath
	installRoot := s.marketplaceInstallRoot
	now := s.now
	s.mu.Unlock()
	values, err := readMarketplaceConfig(configPath)
	if err != nil {
		return err
	}
	marketplaces := configuredMarketplacesFromConfig(values, installRoot, now)
	plugins := configuredPluginsFromConfig(values)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, marketplace := range marketplaces {
		if _, ok := s.marketplaces[marketplace.Name]; !ok {
			s.marketplaces[marketplace.Name] = marketplace
		}
	}
	for _, detail := range plugins {
		if detail.Summary.ID != "" {
			s.plugins[detail.Summary.ID] = cloneDetail(detail)
		}
	}
	return nil
}

func recordMarketplaceConfig(configPath string, marketplace *Marketplace) error {
	configPath = strings.TrimSpace(configPath)
	if configPath == "" || marketplace == nil {
		return nil
	}
	values, err := readMarketplaceConfig(configPath)
	if err != nil {
		return err
	}
	marketplaces := ensureMarketplaceConfigTable(values)
	entry := map[string]any{
		"last_updated": marketplaceConfigTimestamp(marketplace.AddedAt),
		"source_type":  marketplace.SourceType,
		"source":       marketplaceConfigSource(marketplace),
	}
	if marketplace.RefName != nil && strings.TrimSpace(*marketplace.RefName) != "" {
		entry["ref"] = strings.TrimSpace(*marketplace.RefName)
	}
	if marketplace.LastRevision != nil && strings.TrimSpace(*marketplace.LastRevision) != "" {
		entry["last_revision"] = strings.TrimSpace(*marketplace.LastRevision)
	}
	if len(marketplace.SparsePaths) > 0 {
		entry["sparse_paths"] = append([]string(nil), marketplace.SparsePaths...)
	}
	marketplaces[marketplace.Name] = entry
	return writeMarketplaceConfig(configPath, values)
}

func removeMarketplaceConfig(configPath string, marketplaceName string) error {
	configPath = strings.TrimSpace(configPath)
	marketplaceName = strings.TrimSpace(marketplaceName)
	if configPath == "" || marketplaceName == "" {
		return nil
	}
	values, err := readMarketplaceConfig(configPath)
	if err != nil {
		return err
	}
	marketplaces, ok := values["marketplaces"].(map[string]any)
	if !ok {
		return nil
	}
	delete(marketplaces, marketplaceName)
	if len(marketplaces) == 0 {
		delete(values, "marketplaces")
	}
	return writeMarketplaceConfig(configPath, values)
}

func recordInstalledPluginConfig(configPath string, detail *PluginDetail) error {
	configPath = strings.TrimSpace(configPath)
	if configPath == "" || detail == nil || strings.TrimSpace(detail.Summary.ID) == "" {
		return nil
	}
	values, err := readMarketplaceConfig(configPath)
	if err != nil {
		return err
	}
	plugins := ensurePluginConfigTable(values)
	entry := map[string]any{
		"installed":   detail.Summary.Installed,
		"enabled":     detail.Summary.Enabled,
		"marketplace": detail.Summary.MarketplaceName,
		"name":        detail.Summary.Name,
	}
	if path := pluginDetailConfigPath(detail); path != "" {
		entry["path"] = path
	}
	if detail.MarketplacePath != nil && strings.TrimSpace(*detail.MarketplacePath) != "" {
		entry["marketplace_path"] = strings.TrimSpace(*detail.MarketplacePath)
	}
	source := detail.Summary.Source
	if strings.TrimSpace(source.Type) != "" {
		entry["source_type"] = strings.TrimSpace(source.Type)
	}
	if strings.TrimSpace(source.URL) != "" {
		entry["source"] = strings.TrimSpace(source.URL)
	}
	if source.RefName != nil && strings.TrimSpace(*source.RefName) != "" {
		entry["ref"] = strings.TrimSpace(*source.RefName)
	}
	if source.SHA != nil && strings.TrimSpace(*source.SHA) != "" {
		entry["sha"] = strings.TrimSpace(*source.SHA)
	}
	plugins[detail.Summary.ID] = entry
	return writeMarketplaceConfig(configPath, values)
}

func removeInstalledPluginConfig(configPath string, pluginID string) error {
	configPath = strings.TrimSpace(configPath)
	pluginID = strings.TrimSpace(pluginID)
	if configPath == "" || pluginID == "" {
		return nil
	}
	values, err := readMarketplaceConfig(configPath)
	if err != nil {
		return err
	}
	plugins, ok := values["plugins"].(map[string]any)
	if !ok {
		return nil
	}
	delete(plugins, pluginID)
	if len(plugins) == 0 {
		delete(values, "plugins")
	}
	return writeMarketplaceConfig(configPath, values)
}

func removeInstalledPluginConfigsForMarketplace(configPath string, marketplaceName string) error {
	configPath = strings.TrimSpace(configPath)
	marketplaceName = strings.TrimSpace(marketplaceName)
	if configPath == "" || marketplaceName == "" {
		return nil
	}
	values, err := readMarketplaceConfig(configPath)
	if err != nil {
		return err
	}
	plugins, ok := values["plugins"].(map[string]any)
	if !ok {
		return nil
	}
	for id, raw := range plugins {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if stringConfigValue(entry["marketplace"]) == marketplaceName || pluginMarketplaceFromID(id) == marketplaceName {
			delete(plugins, id)
		}
	}
	if len(plugins) == 0 {
		delete(values, "plugins")
	}
	return writeMarketplaceConfig(configPath, values)
}

func readMarketplaceConfig(configPath string) (map[string]any, error) {
	data, err := os.ReadFile(configPath)
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	values := map[string]any{}
	if len(data) == 0 {
		return values, nil
	}
	if err := toml.Unmarshal(data, &values); err != nil {
		return nil, err
	}
	return values, nil
}

func writeMarketplaceConfig(configPath string, values map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		return err
	}
	data, err := toml.Marshal(values)
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, data, 0o600)
}

func ensureMarketplaceConfigTable(values map[string]any) map[string]any {
	if values == nil {
		values = map[string]any{}
	}
	if marketplaces, ok := values["marketplaces"].(map[string]any); ok {
		return marketplaces
	}
	marketplaces := map[string]any{}
	values["marketplaces"] = marketplaces
	return marketplaces
}

func ensurePluginConfigTable(values map[string]any) map[string]any {
	if values == nil {
		values = map[string]any{}
	}
	if plugins, ok := values["plugins"].(map[string]any); ok {
		return plugins
	}
	plugins := map[string]any{}
	values["plugins"] = plugins
	return plugins
}

func marketplaceConfigTimestamp(value time.Time) string {
	if value.IsZero() {
		value = time.Now()
	}
	return value.UTC().Format(time.RFC3339)
}

func marketplaceConfigSource(marketplace *Marketplace) string {
	source := strings.TrimSpace(marketplace.SourceURL)
	if marketplace.SourceType == string(MarketplaceSourceGit) && marketplace.RefName != nil {
		refSuffix := "#" + strings.TrimSpace(*marketplace.RefName)
		source = strings.TrimSuffix(source, refSuffix)
	}
	return source
}

func configuredMarketplacesFromConfig(values map[string]any, installRoot string, now func() time.Time) []Marketplace {
	table, ok := values["marketplaces"].(map[string]any)
	if !ok {
		return nil
	}
	out := make([]Marketplace, 0, len(table))
	for name, raw := range table {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		sourceType := stringConfigValue(entry["source_type"])
		source := stringConfigValue(entry["source"])
		refName := stringPtrIfNotEmpty(stringConfigValue(entry["ref"]))
		rootPath := source
		if sourceType == string(MarketplaceSourceGit) {
			rootPath = filepath.Join(installRoot, sanitize(name))
		}
		if strings.TrimSpace(rootPath) == "" {
			continue
		}
		addedAt := timeConfigValue(entry["last_updated"])
		if addedAt.IsZero() {
			if now == nil {
				addedAt = time.Now()
			} else {
				addedAt = now()
			}
		}
		out = append(out, Marketplace{
			Name:         name,
			SourceURL:    source,
			SourceType:   sourceType,
			RefName:      refName,
			LastRevision: stringPtrIfNotEmpty(stringConfigValue(entry["last_revision"])),
			SparsePaths:  stringSliceConfigValue(entry["sparse_paths"]),
			RootPath:     filepath.Clean(rootPath),
			AddedAt:      addedAt.UTC(),
		})
	}
	return out
}

func configuredPluginsFromConfig(values map[string]any) []PluginDetail {
	table, ok := values["plugins"].(map[string]any)
	if !ok {
		return nil
	}
	out := make([]PluginDetail, 0, len(table))
	for id, raw := range table {
		entry, ok := raw.(map[string]any)
		if !ok || !boolConfigValue(entry["installed"], true) {
			continue
		}
		name := firstNonEmpty(stringConfigValue(entry["name"]), pluginNameFromID(id))
		marketplaceName := firstNonEmpty(stringConfigValue(entry["marketplace"]), pluginMarketplaceFromID(id))
		pluginRoot := stringConfigValue(entry["path"])
		marketplacePath := stringConfigValue(entry["marketplace_path"])
		sourceType := firstNonEmpty(stringConfigValue(entry["source_type"]), "local")
		plugin := marketplaceManifestPlugin{
			Name: name,
			Source: marketplacePluginSource{
				Source: sourceType,
				URL:    stringConfigValue(entry["source"]),
				Ref:    stringPtrIfNotEmpty(stringConfigValue(entry["ref"])),
				SHA:    stringPtrIfNotEmpty(stringConfigValue(entry["sha"])),
			},
		}
		manifest := readPluginManifestFile(filepath.Join(pluginRoot, ".codex-plugin", "plugin.json"))
		detail := marketplacePluginDetailFromManifest(name, marketplaceName, "", marketplacePath, pluginRoot, plugin, manifest)
		detail.Summary.ID = id
		detail.Summary.Installed = true
		detail.Summary.Enabled = boolConfigValue(entry["enabled"], true)
		if detail.MarketplaceName == "" {
			detail.MarketplaceName = marketplaceName
		}
		out = append(out, detail)
	}
	return out
}

func pluginDetailConfigPath(detail *PluginDetail) string {
	if detail == nil {
		return ""
	}
	if path := strings.TrimSpace(detail.Summary.Source.Path); path != "" {
		return path
	}
	return pluginRootFromManifestPath(detail.ManifestPath)
}

func pluginNameFromID(id string) string {
	name, _, ok := strings.Cut(strings.TrimSpace(id), "@")
	if !ok {
		return strings.TrimSpace(id)
	}
	return name
}

func pluginMarketplaceFromID(id string) string {
	_, marketplace, ok := strings.Cut(strings.TrimSpace(id), "@")
	if !ok {
		return ""
	}
	return marketplace
}

func stringConfigValue(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	default:
		return ""
	}
}

func boolConfigValue(value any, fallback bool) bool {
	switch v := value.(type) {
	case bool:
		return v
	default:
		return fallback
	}
}

func stringSliceConfigValue(value any) []string {
	switch v := value.(type) {
	case []string:
		return append([]string(nil), v...)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s := stringConfigValue(item); s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func timeConfigValue(value any) time.Time {
	text := stringConfigValue(value)
	if text == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339, text)
	if err != nil {
		return time.Time{}
	}
	return parsed
}
