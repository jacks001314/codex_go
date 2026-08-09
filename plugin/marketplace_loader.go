package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var marketplaceManifestRelativePaths = []string{
	filepath.Join(".agents", "plugins", "marketplace.json"),
	filepath.Join(".agents", "plugins", "api_marketplace.json"),
	filepath.Join(".claude-plugin", "marketplace.json"),
	filepath.Join(".cursor-plugin", "marketplace.json"),
}

type marketplaceManifestFile struct {
	Name      string                      `json:"name"`
	Interface *MarketplaceInterface       `json:"interface"`
	Plugins   []marketplaceManifestPlugin `json:"plugins"`
}

type marketplaceManifestPlugin struct {
	Name              string                  `json:"name"`
	Source            marketplacePluginSource `json:"source"`
	Interface         *PluginInterface        `json:"interface"`
	Keywords          []string                `json:"keywords"`
	Apps              []AppSummary            `json:"apps"`
	AppTemplates      []AppTemplateSummary    `json:"appTemplates"`
	AppTemplatesSnake []AppTemplateSummary    `json:"app_templates"`
}

type marketplacePluginSource struct {
	Source string  `json:"source"`
	Type   string  `json:"type"`
	Path   string  `json:"path"`
	URL    string  `json:"url"`
	Ref    *string `json:"ref"`
	SHA    *string `json:"sha"`
}

type pluginManifestFile struct {
	Name              string               `json:"name"`
	Version           string               `json:"version"`
	DisplayName       string               `json:"displayName"`
	Description       string               `json:"description"`
	Interface         *PluginInterface     `json:"interface"`
	Keywords          []string             `json:"keywords"`
	Apps              []AppSummary         `json:"apps"`
	AppTemplates      []AppTemplateSummary `json:"appTemplates"`
	AppTemplatesSnake []AppTemplateSummary `json:"app_templates"`
	ManifestPath      string               `json:"-"`
	SkillsPath        string               `json:"-"`
	MCPServersPath    string               `json:"-"`
	AgentPlugin       bool                 `json:"-"`
}

func loadMarketplacePlugins(marketplaces []Marketplace) ([]PluginDetail, []MarketplaceLoadErrorInfo) {
	var details []PluginDetail
	var errors []MarketplaceLoadErrorInfo
	for _, marketplace := range marketplaces {
		manifestPath := findMarketplaceManifestPath(marketplace.RootPath)
		if manifestPath == "" {
			continue
		}
		loaded, err := loadMarketplaceManifest(marketplace, manifestPath)
		if err != nil {
			errors = append(errors, MarketplaceLoadErrorInfo{MarketplacePath: manifestPath, Message: err.Error()})
			continue
		}
		details = append(details, loaded...)
	}
	sort.SliceStable(details, func(i int, j int) bool {
		return details[i].Summary.ID < details[j].Summary.ID
	})
	sort.SliceStable(errors, func(i int, j int) bool {
		return errors[i].MarketplacePath < errors[j].MarketplacePath
	})
	return details, errors
}

func findMarketplaceManifestPath(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	if info, err := os.Stat(root); err == nil && !info.IsDir() {
		return root
	}
	for _, relative := range marketplaceManifestRelativePaths {
		path := filepath.Join(root, relative)
		info, err := os.Stat(path)
		if err == nil && !info.IsDir() {
			return path
		}
	}
	return ""
}

func loadMarketplaceManifest(marketplace Marketplace, manifestPath string) ([]PluginDetail, error) {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, err
	}
	var manifest marketplaceManifestFile
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	marketplaceName := strings.TrimSpace(manifest.Name)
	if marketplaceName == "" {
		marketplaceName = marketplace.Name
	}
	root := marketplaceRootForManifest(marketplace.RootPath, manifestPath)
	details := make([]PluginDetail, 0, len(manifest.Plugins))
	for _, plugin := range manifest.Plugins {
		detail, ok := loadMarketplacePluginDetail(root, marketplaceName, manifestPath, plugin)
		if ok {
			details = append(details, detail)
		}
	}
	return details, nil
}

func marketplaceRootForManifest(configuredRoot string, manifestPath string) string {
	configuredRoot = strings.TrimSpace(configuredRoot)
	manifestPath = filepath.Clean(strings.TrimSpace(manifestPath))
	if info, err := os.Stat(configuredRoot); err != nil || info.IsDir() {
		return configuredRoot
	}
	dir := filepath.Dir(manifestPath)
	switch filepath.Base(dir) {
	case ".claude-plugin", ".cursor-plugin":
		return filepath.Dir(dir)
	case "plugins":
		if filepath.Base(filepath.Dir(dir)) == ".agents" {
			return filepath.Dir(filepath.Dir(dir))
		}
	}
	return dir
}

func loadMarketplacePluginDetail(marketplaceRoot string, marketplaceName string, marketplacePath string, plugin marketplaceManifestPlugin) (PluginDetail, bool) {
	pluginName := strings.TrimSpace(plugin.Name)
	if pluginName == "" {
		return PluginDetail{}, false
	}
	sourceKind := marketplaceManifestPluginSourceKind(&plugin.Source)
	if sourceKind != "" && sourceKind != "local" {
		return marketplacePluginDetailFromManifest(pluginName, marketplaceName, marketplaceRoot, marketplacePath, "", plugin, nil), true
	}
	pluginRoot := resolveMarketplacePluginPath(marketplaceRoot, plugin.Source.Path)
	resolved, _ := loadPluginManifest(pluginRoot)
	var manifest *pluginManifestFile
	if resolved != nil {
		manifest = &resolved.Manifest
	}
	return marketplacePluginDetailFromManifest(pluginName, marketplaceName, marketplaceRoot, marketplacePath, pluginRoot, plugin, manifest), true
}

func marketplacePluginDetailFromManifest(pluginName string, marketplaceName string, marketplaceRoot string, marketplacePath string, pluginRoot string, plugin marketplaceManifestPlugin, manifest *pluginManifestFile) PluginDetail {
	displayName := pluginName
	description := ""
	var iface *PluginInterface
	apps := cloneAppSummaries(plugin.Apps)
	appTemplates := cloneAppTemplateSummaries(firstMarketplacePluginAppTemplates(plugin))
	keywords := trimPluginInterfaceStrings(plugin.Keywords)
	if plugin.Interface != nil {
		iface = clonePluginInterfacePtr(plugin.Interface)
	}
	if manifest != nil {
		if strings.TrimSpace(manifest.Name) != "" {
			pluginName = strings.TrimSpace(manifest.Name)
		}
		if strings.TrimSpace(manifest.DisplayName) != "" {
			displayName = strings.TrimSpace(manifest.DisplayName)
		} else {
			displayName = pluginName
		}
		description = strings.TrimSpace(manifest.Description)
		if manifest.Interface != nil {
			iface = clonePluginInterfacePtr(manifest.Interface)
		}
		if len(manifest.Keywords) != 0 {
			keywords = trimPluginInterfaceStrings(manifest.Keywords)
		}
		if len(manifest.Apps) != 0 {
			apps = cloneAppSummaries(manifest.Apps)
		}
		if templates := firstPluginManifestAppTemplates(manifest); len(templates) != 0 {
			appTemplates = cloneAppTemplateSummaries(templates)
		}
	}
	manifestPath := ""
	if manifest != nil {
		manifestPath = manifest.ManifestPath
	}
	hasSkills := marketplacePluginHasSkillsForManifest(pluginRoot, manifest)
	mcpServers := marketplacePluginMCPServersForManifest(pluginRoot, manifest)
	var version *string
	if manifest != nil {
		version = stringPtrIfNotEmpty(manifest.Version)
	}
	summary := PluginSummary{
		ID:              pluginID(pluginName, marketplaceName),
		Name:            pluginName,
		DisplayName:     displayName,
		Description:     description,
		MarketplaceName: marketplaceName,
		Version:         version,
		Availability:    PluginAvailable,
		InstallPolicy:   InstallAllowed,
		AuthPolicy:      AuthNone,
		Interface:       iface,
		Source:          pluginSourceFromMarketplaceManifest(&plugin.Source, pluginRoot),
		HasSkills:       hasSkills,
		MCPServers:      append([]string(nil), mcpServers...),
		AppConnectors:   marketplacePluginAppConnectorIDs(apps, appTemplates),
		Keywords:        keywords,
	}
	return PluginDetail{
		MarketplaceName: marketplaceName,
		MarketplacePath: stringPtrIfNotEmpty(marketplacePath),
		MarketplaceRoot: marketplaceRoot,
		Summary:         summary,
		Description:     stringPtrIfNotEmpty(description),
		ManifestPath:    manifestPath,
		Skills:          marketplacePluginSkillsForManifest(pluginRoot, manifest),
		Apps:            apps,
		AppTemplates:    appTemplates,
		MCPServers:      mcpServers,
	}
}

func marketplaceManifestPluginSourceKind(source *marketplacePluginSource) string {
	if source == nil {
		return ""
	}
	return strings.TrimSpace(firstNonEmpty(source.Source, source.Type))
}

func firstMarketplacePluginAppTemplates(plugin marketplaceManifestPlugin) []AppTemplateSummary {
	if len(plugin.AppTemplates) != 0 {
		return plugin.AppTemplates
	}
	return plugin.AppTemplatesSnake
}

func firstPluginManifestAppTemplates(manifest *pluginManifestFile) []AppTemplateSummary {
	if manifest == nil {
		return nil
	}
	if len(manifest.AppTemplates) != 0 {
		return manifest.AppTemplates
	}
	return manifest.AppTemplatesSnake
}

func marketplacePluginAppConnectorIDs(apps []AppSummary, templates []AppTemplateSummary) []string {
	seen := map[string]bool{}
	ids := make([]string, 0, len(apps)+len(templates))
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		ids = append(ids, id)
	}
	for _, app := range apps {
		add(app.ID)
	}
	for _, template := range templates {
		if template.CanonicalConnectorID != nil {
			add(*template.CanonicalConnectorID)
		}
		for _, id := range template.MaterializedAppIDs {
			add(id)
		}
	}
	sort.Strings(ids)
	return ids
}

func pluginSourceFromMarketplaceManifest(source *marketplacePluginSource, pluginRoot string) PluginSource {
	kind := marketplaceManifestPluginSourceKind(source)
	if kind == "" || kind == "local" {
		return PluginSource{Type: "local", Path: pluginRoot}
	}
	out := PluginSource{
		Type:    kind,
		RefName: cloneStringPtr(source.Ref),
		SHA:     cloneStringPtr(source.SHA),
	}
	if source != nil {
		out.URL = strings.TrimSpace(source.URL)
		out.Path = strings.TrimSpace(source.Path)
	}
	if strings.TrimSpace(pluginRoot) != "" {
		out.Path = pluginRoot
	}
	return out
}

func pluginDetailNeedsMarketplaceMaterialization(detail *PluginDetail) bool {
	if detail == nil {
		return false
	}
	sourceType := strings.TrimSpace(detail.Summary.Source.Type)
	if sourceType == "" || sourceType == "local" {
		return false
	}
	manifestPath := strings.TrimSpace(detail.ManifestPath)
	if manifestPath == "" {
		return true
	}
	return readPluginManifestFile(manifestPath) == nil
}

func resolveMarketplacePluginPath(marketplaceRoot string, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return filepath.Clean(filepath.Join(marketplaceRoot, value))
}

func readPluginManifestFile(path string) *pluginManifestFile {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var manifest pluginManifestFile
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil
	}
	return &manifest
}

func readPluginManifestForRoot(pluginRoot string) *pluginManifestFile {
	resolved, err := loadPluginManifest(pluginRoot)
	if err != nil || resolved == nil {
		return nil
	}
	manifest := resolved.Manifest
	return &manifest
}

func marketplacePluginHasSkills(pluginRoot string) bool {
	if pluginRoot == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(pluginRoot, "skills"))
	return err == nil && info.IsDir()
}

func marketplacePluginHasSkillsForManifest(pluginRoot string, manifest *pluginManifestFile) bool {
	root := filepath.Join(pluginRoot, "skills")
	if manifest != nil && strings.TrimSpace(manifest.SkillsPath) != "" {
		root = manifest.SkillsPath
	}
	if manifest != nil && manifest.AgentPlugin {
		return len(marketplacePluginSkillsForManifest(pluginRoot, manifest)) > 0
	}
	info, err := os.Stat(root)
	return err == nil && info.IsDir()
}

func marketplacePluginSkills(pluginRoot string) []PluginSkill {
	return marketplacePluginSkillsForManifest(pluginRoot, nil)
}

func marketplacePluginSkillsForManifest(pluginRoot string, manifest *pluginManifestFile) []PluginSkill {
	skillsRoot := filepath.Join(pluginRoot, "skills")
	if manifest != nil && strings.TrimSpace(manifest.SkillsPath) != "" {
		skillsRoot = manifest.SkillsPath
	}
	if info, err := os.Stat(skillsRoot); err != nil || !info.IsDir() {
		return nil
	}
	var skills []PluginSkill
	_ = filepath.WalkDir(skillsRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if manifest != nil && manifest.AgentPlugin && path != skillsRoot {
				relative, relErr := filepath.Rel(skillsRoot, path)
				if relErr != nil || strings.Count(filepath.ToSlash(relative), "/") >= 1 {
					return filepath.SkipDir
				}
			}
			if path != skillsRoot && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "SKILL.md" {
			return nil
		}
		if manifest != nil && manifest.AgentPlugin {
			resolved, resolveErr := filepath.EvalSymlinks(path)
			if resolveErr != nil || !pathWithinRoot(pluginRoot, resolved) {
				return nil
			}
		}
		name := filepath.Base(filepath.Dir(path))
		if name == "skills" {
			name = filepath.Base(pluginRoot)
		}
		if data, err := os.ReadFile(path); err == nil {
			if frontmatterName := pluginSkillFrontmatterName(string(data)); frontmatterName != "" {
				name = frontmatterName
			}
		}
		skillPath := path
		skills = append(skills, PluginSkill{Name: name, Path: &skillPath, Enabled: true})
		return nil
	})
	sort.SliceStable(skills, func(i int, j int) bool {
		if skills[i].Name == skills[j].Name {
			return strings.Compare(stringValuePtr(skills[i].Path), stringValuePtr(skills[j].Path)) < 0
		}
		return skills[i].Name < skills[j].Name
	})
	if len(skills) == 0 && (manifest == nil || !manifest.AgentPlugin) {
		return []PluginSkill{{Name: filepath.Base(pluginRoot), Enabled: true}}
	}
	return skills
}

func marketplacePluginMCPServers(pluginRoot string) []string {
	return marketplacePluginMCPServersForManifest(pluginRoot, nil)
}

func marketplacePluginMCPServersForManifest(pluginRoot string, manifest *pluginManifestFile) []string {
	path := filepath.Join(pluginRoot, ".mcp.json")
	if manifest != nil && strings.TrimSpace(manifest.MCPServersPath) != "" {
		path = manifest.MCPServersPath
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var payload struct {
		MCPServers map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil
	}
	servers := make([]string, 0, len(payload.MCPServers))
	for name := range payload.MCPServers {
		servers = append(servers, name)
	}
	sort.Strings(servers)
	return servers
}

func pathWithinRoot(root string, path string) bool {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(rootAbs, pathAbs)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func marketplaceRootFromPluginRoot(pluginRoot string) string {
	if pluginRoot == "" {
		return ""
	}
	parent := filepath.Dir(pluginRoot)
	if filepath.Base(parent) == "plugins" {
		return filepath.Dir(parent)
	}
	return parent
}

func stringValuePtr(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
