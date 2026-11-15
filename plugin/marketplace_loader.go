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
	Policy            marketplacePluginPolicy `json:"policy"`
	Interface         *PluginInterface        `json:"interface"`
	Keywords          []string                `json:"keywords"`
	Apps              []AppSummary            `json:"apps"`
	AppTemplates      []AppTemplateSummary    `json:"appTemplates"`
	AppTemplatesSnake []AppTemplateSummary    `json:"app_templates"`
}

// marketplacePluginPolicy mirrors Rust MarketplacePluginPolicy. Products is
// nil when the entry has no restriction, non-nil and empty when the entry is
// hidden from every product, and otherwise the allowed products.
type marketplacePluginPolicy struct {
	Products *[]string `json:"products"`
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
	// OnboardingSkill is the declared relative skill path
	// (`extensions["com.openai"].onboardingSkill`, Rust #46544).
	OnboardingSkill string `json:"onboardingSkill"`
	ManifestPath    string `json:"-"`
	SkillsPath      string `json:"-"`
	MCPServersPath  string `json:"-"`
	AgentPlugin     bool   `json:"-"`
}

func loadMarketplacePlugins(marketplaces []Marketplace, cache *manifestCache) ([]PluginDetail, []MarketplaceLoadErrorInfo) {
	var details []PluginDetail
	var errors []MarketplaceLoadErrorInfo
	for _, marketplace := range marketplaces {
		manifestPath := findMarketplaceManifestPath(marketplace.RootPath)
		if manifestPath == "" {
			continue
		}
		loaded, err := loadMarketplaceManifest(marketplace, manifestPath, cache)
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

// MarketplaceSelection tracks marketplace paths and plugin IDs already claimed
// by earlier project scopes in one request (Rust MarketplaceSelection, #61686).
// A nil selection keeps the single-scope behavior: plugin IDs are still
// deduplicated within the listing, but repeated paths are not suppressed.
type MarketplaceSelection struct {
	paths     map[string]bool
	pluginIDs map[string]bool
}

// NewMarketplaceSelection creates the request-wide selection shared across
// project scopes.
func NewMarketplaceSelection() *MarketplaceSelection {
	return &MarketplaceSelection{
		paths:     map[string]bool{},
		pluginIDs: map[string]bool{},
	}
}

// claimPath reports whether this scope may list the marketplace manifest at
// path, preserving first-scope precedence.
func (s *MarketplaceSelection) claimPath(path string) bool {
	if s == nil {
		return true
	}
	key := cleanPluginPathForCompare(path)
	if key == "" {
		key = path
	}
	if s.paths[key] {
		return false
	}
	s.paths[key] = true
	return true
}

// claimPluginID reports whether this scope may load the plugin, preserving the
// first-source precedence established across scopes.
func (s *MarketplaceSelection) claimPluginID(id string) bool {
	if s == nil {
		return true
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	if s.pluginIDs[id] {
		return false
	}
	s.pluginIDs[id] = true
	return true
}

// marketplacePluginEntry is a validated catalog row whose plugin manifest has
// not been read yet (Rust MarketplacePluginEntry).
type marketplacePluginEntry struct {
	name              string
	source            marketplacePluginSource
	sourceKind        string
	pluginRoot        string
	policyProducts    *[]string
	interfaceOverride *PluginInterface
	keywords          []string
	apps              []AppSummary
	appTemplates      []AppTemplateSummary
}

func (e marketplacePluginEntry) manifestPlugin() marketplaceManifestPlugin {
	return marketplaceManifestPlugin{
		Name:         e.name,
		Source:       e.source,
		Interface:    e.interfaceOverride,
		Keywords:     e.keywords,
		Apps:         e.apps,
		AppTemplates: e.appTemplates,
	}
}

// marketplacePluginManifestLoadHook observes selected plugin manifest loads in
// tests, so discarded rows can be proven unread (Rust logs the same reads).
var marketplacePluginManifestLoadHook func(pluginRoot string)

// loadConfiguredMarketplacePlugins lists configured marketplaces with Rust's
// #61686 ordering: validate entries, claim IDs before product visibility,
// claim marketplace paths, then load manifests only for the selected rows.
func loadConfiguredMarketplacePlugins(marketplaces []Marketplace, cache *manifestCache, selection *MarketplaceSelection, restrictionProduct string) ([]PluginMarketplaceEntry, []MarketplaceLoadErrorInfo) {
	var entries []PluginMarketplaceEntry
	var loadErrors []MarketplaceLoadErrorInfo
	seenPluginKeys := map[string]bool{}
	for _, marketplace := range marketplaces {
		manifestPath := findMarketplaceManifestPath(marketplace.RootPath)
		if manifestPath == "" {
			continue
		}
		marketplaceName, root, marketplaceInterface, rawEntries, err := readMarketplaceEntries(marketplace, manifestPath)
		if err != nil {
			loadErrors = append(loadErrors, MarketplaceLoadErrorInfo{MarketplacePath: manifestPath, Message: err.Error()})
			continue
		}
		selected := make([]marketplacePluginEntry, 0, len(rawEntries))
		for _, entry := range rawEntries {
			key := pluginID(entry.name, marketplaceName)
			if seenPluginKeys[key] {
				continue
			}
			// The first valid source claims its ID before product visibility.
			seenPluginKeys[key] = true
			if !marketplacePluginProductAllowed(entry.policyProducts, restrictionProduct) {
				continue
			}
			selected = append(selected, entry)
		}
		if len(selected) == 0 {
			continue
		}
		// A visible entry claims the path even if an earlier project claimed its ID.
		if !claimMarketplaceSelectionPath(selection, manifestPath) {
			continue
		}
		filtered := selected[:0]
		for _, entry := range selected {
			if !claimMarketplaceSelectionPluginID(selection, pluginID(entry.name, marketplaceName)) {
				continue
			}
			filtered = append(filtered, entry)
		}
		if len(filtered) == 0 {
			continue
		}
		loaded := loadPluginsConcurrently(len(filtered), func(index int) PluginDetail {
			return loadMarketplaceEntryDetail(marketplaceName, root, manifestPath, filtered[index], cache)
		})
		plugins := make([]PluginSummary, 0, len(loaded))
		for _, detail := range loaded {
			plugins = append(plugins, cloneSummary(detail.Summary))
		}
		path := manifestPath
		entries = append(entries, PluginMarketplaceEntry{
			Name:      marketplaceName,
			Path:      &path,
			Interface: marketplaceInterface,
			Plugins:   plugins,
		})
	}
	sort.SliceStable(loadErrors, func(i int, j int) bool {
		return loadErrors[i].MarketplacePath < loadErrors[j].MarketplacePath
	})
	return entries, loadErrors
}

func claimMarketplaceSelectionPath(selection *MarketplaceSelection, path string) bool {
	return selection == nil || selection.claimPath(path)
}

func claimMarketplaceSelectionPluginID(selection *MarketplaceSelection, id string) bool {
	return selection == nil || selection.claimPluginID(id)
}

func readMarketplaceEntries(marketplace Marketplace, manifestPath string) (string, string, *MarketplaceInterface, []marketplacePluginEntry, error) {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", "", nil, nil, err
	}
	var manifest marketplaceManifestFile
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "", "", nil, nil, err
	}
	marketplaceName := strings.TrimSpace(manifest.Name)
	if marketplaceName == "" {
		marketplaceName = marketplace.Name
	}
	root := marketplaceRootForManifest(marketplace.RootPath, manifestPath)
	entries := make([]marketplacePluginEntry, 0, len(manifest.Plugins))
	for _, plugin := range manifest.Plugins {
		name := strings.TrimSpace(plugin.Name)
		if name == "" {
			continue
		}
		sourceKind := marketplaceManifestPluginSourceKind(&plugin.Source)
		pluginRoot := ""
		if sourceKind == "" || sourceKind == "local" {
			pluginRoot = resolveMarketplacePluginPath(root, plugin.Source.Path)
		}
		entries = append(entries, marketplacePluginEntry{
			name:              name,
			source:            plugin.Source,
			sourceKind:        sourceKind,
			pluginRoot:        pluginRoot,
			policyProducts:    plugin.Policy.Products,
			interfaceOverride: clonePluginInterfacePtr(plugin.Interface),
			keywords:          trimPluginInterfaceStrings(plugin.Keywords),
			apps:              cloneAppSummaries(plugin.Apps),
			appTemplates:      cloneAppTemplateSummaries(firstMarketplacePluginAppTemplates(plugin)),
		})
	}
	return marketplaceName, root, manifest.Interface, entries, nil
}

func loadMarketplaceEntryDetail(marketplaceName string, marketplaceRoot string, manifestPath string, entry marketplacePluginEntry, cache *manifestCache) PluginDetail {
	var manifest *pluginManifestFile
	if entry.sourceKind == "" || entry.sourceKind == "local" {
		if marketplacePluginManifestLoadHook != nil {
			marketplacePluginManifestLoadHook(entry.pluginRoot)
		}
		if resolved, _ := parsePluginManifestAtRoot(entry.pluginRoot, cache); resolved != nil {
			manifest = &resolved.Manifest
		}
	}
	return marketplacePluginDetailFromManifest(entry.name, marketplaceName, marketplaceRoot, manifestPath, entry.pluginRoot, entry.manifestPlugin(), manifest)
}

func marketplacePluginProductAllowed(products *[]string, restrictionProduct string) bool {
	if products == nil {
		return true
	}
	if len(*products) == 0 {
		return false
	}
	restrictionProduct = normalizeMarketplaceProduct(restrictionProduct)
	if restrictionProduct == "" {
		return false
	}
	for _, product := range *products {
		if normalizeMarketplaceProduct(product) == restrictionProduct {
			return true
		}
	}
	return false
}

func normalizeMarketplaceProduct(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "chatgpt":
		return "chatgpt"
	case "codex":
		return "codex"
	case "atlas":
		return "atlas"
	default:
		return ""
	}
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

func loadMarketplaceManifest(marketplace Marketplace, manifestPath string, cache *manifestCache) ([]PluginDetail, error) {
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
	// Rust #57819: load independent plugin entries concurrently while keeping
	// manifest order, which decides duplicate MCP precedence.
	type loadedDetail struct {
		detail PluginDetail
		ok     bool
	}
	loaded := loadPluginsConcurrently(len(manifest.Plugins), func(index int) loadedDetail {
		detail, ok := loadMarketplacePluginDetail(root, marketplaceName, manifestPath, manifest.Plugins[index], cache)
		return loadedDetail{detail: detail, ok: ok}
	})
	details := make([]PluginDetail, 0, len(manifest.Plugins))
	for _, result := range loaded {
		if result.ok {
			details = append(details, result.detail)
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

func loadMarketplacePluginDetail(marketplaceRoot string, marketplaceName string, marketplacePath string, plugin marketplaceManifestPlugin, cache *manifestCache) (PluginDetail, bool) {
	pluginName := strings.TrimSpace(plugin.Name)
	if pluginName == "" {
		return PluginDetail{}, false
	}
	sourceKind := marketplaceManifestPluginSourceKind(&plugin.Source)
	if sourceKind != "" && sourceKind != "local" {
		return marketplacePluginDetailFromManifest(pluginName, marketplaceName, marketplaceRoot, marketplacePath, "", plugin, nil), true
	}
	pluginRoot := resolveMarketplacePluginPath(marketplaceRoot, plugin.Source.Path)
	resolved, _ := parsePluginManifestAtRoot(pluginRoot, cache)
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
		MarketplaceName:     marketplaceName,
		MarketplacePath:     stringPtrIfNotEmpty(marketplacePath),
		MarketplaceRoot:     marketplaceRoot,
		Summary:             summary,
		Description:         stringPtrIfNotEmpty(description),
		ManifestPath:        manifestPath,
		Skills:              marketplacePluginSkillsForManifest(pluginRoot, manifest),
		Apps:                apps,
		AppTemplates:        appTemplates,
		MCPServers:          mcpServers,
		onboardingSkillPath: resolveOnboardingSkillPath(pluginRoot, manifest),
	}
}

// resolveOnboardingSkillPath resolves the manifest's declared onboarding skill
// against the plugin root, mirroring Rust's resolve_openai_onboarding_skill
// (#46544): the declaration accepts a relative path with or without the legacy
// `./` prefix and must stay inside the plugin root.
func resolveOnboardingSkillPath(pluginRoot string, manifest *pluginManifestFile) string {
	if manifest == nil {
		return ""
	}
	declared := strings.TrimSpace(manifest.OnboardingSkill)
	if declared == "" || filepath.IsAbs(declared) {
		return ""
	}
	declared = "./" + strings.TrimPrefix(declared, "./")
	resolved := filepath.Join(pluginRoot, filepath.FromSlash(declared))
	if !pathWithinRoot(pluginRoot, resolved) {
		return ""
	}
	return filepath.Clean(resolved)
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

func readPluginManifestForRoot(pluginRoot string, cache *manifestCache) *pluginManifestFile {
	resolved, err := parsePluginManifestAtRoot(pluginRoot, cache)
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
