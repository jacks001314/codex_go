package appserver

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"codex_go/config"
	"codex_go/install"
	"codex_go/systemskills"

	"gopkg.in/yaml.v3"
)

const (
	SkillFilename         = "SKILL.md"
	SkillMetadataDir      = "agents"
	SkillMetadataFilename = "openai.yaml"

	skillMaxNameLen          = 64
	skillMaxQualifiedNameLen = 128
	skillMaxDescriptionLen   = 1024
	skillMaxScanDepth        = 6
	skillMaxDirsPerRoot      = 2000
)

var ErrInvalidSkillsRequest = errors.New("invalid skills request")

type SkillsListParams struct {
	CWDs        []string      `json:"cwds,omitempty"`
	ForceReload bool          `json:"forceReload,omitempty"`
	Config      []ConfigEntry `json:"-"`
}

type SkillsListEntry struct {
	CWD    string            `json:"cwd,omitempty"`
	Skills []SkillsListEntry `json:"skills,omitempty"`
	Errors []SkillErrorInfo  `json:"errors,omitempty"`

	Name             string             `json:"name,omitempty"`
	Path             string             `json:"path,omitempty"`
	DisplayPath      string             `json:"-"`
	Scope            string             `json:"scope,omitempty"`
	Description      string             `json:"description,omitempty"`
	ShortDescription string             `json:"shortDescription,omitempty"`
	Interface        *SkillInterface    `json:"interface,omitempty"`
	Dependencies     *SkillDependencies `json:"dependencies,omitempty"`
	Enabled          bool               `json:"enabled"`
	PluginID         string             `json:"-"`
	Policy           *SkillPolicy       `json:"-"`
	Contents         string             `json:"-"`
	Root             string             `json:"-"`
	ApplicableCWDs   []string           `json:"-"`
}

func (e *SkillsListEntry) MarshalJSON() ([]byte, error) {
	if e == nil {
		return []byte("null"), nil
	}
	if e.CWD != "" || e.Skills != nil || e.Errors != nil {
		skills := cloneSkills(e.Skills)
		if skills == nil {
			skills = []SkillsListEntry{}
		}
		errors := append([]SkillErrorInfo(nil), e.Errors...)
		if errors == nil {
			errors = []SkillErrorInfo{}
		}
		return json.Marshal(struct {
			CWD    string            `json:"cwd"`
			Skills []SkillsListEntry `json:"skills"`
			Errors []SkillErrorInfo  `json:"errors"`
		}{
			CWD:    e.CWD,
			Skills: skills,
			Errors: errors,
		})
	}
	return json.Marshal(struct {
		Name             string             `json:"name"`
		Description      string             `json:"description"`
		ShortDescription string             `json:"shortDescription,omitempty"`
		Interface        *SkillInterface    `json:"interface,omitempty"`
		Dependencies     *SkillDependencies `json:"dependencies,omitempty"`
		Path             string             `json:"path"`
		Scope            string             `json:"scope"`
		Enabled          bool               `json:"enabled"`
	}{
		Name:             e.Name,
		Description:      e.Description,
		ShortDescription: e.ShortDescription,
		Interface:        e.Interface,
		Dependencies:     e.Dependencies,
		Path:             e.Path,
		Scope:            e.Scope,
		Enabled:          e.Enabled,
	})
}

type SkillsListResponse struct {
	Data   []SkillsListEntry `json:"data,omitempty"`
	Skills []SkillsListEntry `json:"skills"`
	Errors []SkillErrorInfo  `json:"-"`
}

func (r *SkillsListResponse) MarshalJSON() ([]byte, error) {
	data := cloneSkills(r.Data)
	if data == nil {
		data = []SkillsListEntry{}
	}
	return json.Marshal(struct {
		Data []SkillsListEntry `json:"data"`
	}{
		Data: data,
	})
}

type SkillInterface struct {
	DisplayName      string  `json:"displayName,omitempty"`
	ShortDescription string  `json:"shortDescription,omitempty"`
	IconSmall        *string `json:"iconSmall,omitempty"`
	IconLarge        *string `json:"iconLarge,omitempty"`
	BrandColor       *string `json:"brandColor,omitempty"`
	DefaultPrompt    *string `json:"defaultPrompt,omitempty"`
}

type SkillDependencies struct {
	Tools []SkillToolDependency `json:"tools"`
}

func (d *SkillDependencies) MarshalJSON() ([]byte, error) {
	tools := append([]SkillToolDependency(nil), d.Tools...)
	if tools == nil {
		tools = []SkillToolDependency{}
	}
	return json.Marshal(struct {
		Tools []SkillToolDependency `json:"tools"`
	}{Tools: tools})
}

type SkillToolDependency struct {
	Type        string  `json:"type,omitempty"`
	Value       string  `json:"value,omitempty"`
	Description string  `json:"description,omitempty"`
	Transport   string  `json:"transport,omitempty"`
	Command     *string `json:"command,omitempty"`
	URL         *string `json:"url,omitempty"`
}

func (d *SkillToolDependency) MarshalJSON() ([]byte, error) {
	if d == nil {
		return []byte("null"), nil
	}
	return json.Marshal(struct {
		Type        string  `json:"type"`
		Value       string  `json:"value"`
		Description string  `json:"description,omitempty"`
		Transport   string  `json:"transport,omitempty"`
		Command     *string `json:"command,omitempty"`
		URL         *string `json:"url,omitempty"`
	}{
		Type:        d.Type,
		Value:       d.Value,
		Description: d.Description,
		Transport:   d.Transport,
		Command:     cloneStringPtrAppserver(d.Command),
		URL:         cloneStringPtrAppserver(d.URL),
	})
}

type SkillPolicy struct {
	AllowImplicitInvocation *bool    `json:"-"`
	Products                []string `json:"-"`
}

func (e *SkillsListEntry) AllowsImplicitInvocation() bool {
	if e == nil || e.Policy == nil || e.Policy.AllowImplicitInvocation == nil {
		return true
	}
	return *e.Policy.AllowImplicitInvocation
}

func skillMatchesCodexProductRestriction(entry *SkillsListEntry) bool {
	if entry == nil || entry.Policy == nil || len(entry.Policy.Products) == 0 {
		return true
	}
	for _, product := range entry.Policy.Products {
		if strings.EqualFold(strings.TrimSpace(product), "codex") {
			return true
		}
	}
	return false
}

type SkillErrorInfo struct {
	Path           string   `json:"path"`
	Message        string   `json:"message"`
	ApplicableCWDs []string `json:"-"`
}

type SkillsExtraRootsSetParams struct {
	ExtraRoots []string `json:"extraRoots,omitempty"`
	Roots      []string `json:"roots,omitempty"`
}

type SkillsExtraRootsSetResponse struct{}

type SkillsConfigWriteParams struct {
	Name    string `json:"name,omitempty"`
	Path    string `json:"path,omitempty"`
	Enabled bool   `json:"enabled"`
}

type SkillsConfigWriteResponse struct {
	EffectiveEnabled bool `json:"effectiveEnabled"`
	Updated          bool `json:"-"`
}

type ConfigEntry struct {
	Name    string
	Path    string
	Enabled bool
}

type SkillsRoot struct {
	Path       string
	Scope      string
	PluginID   string
	PluginRoot string
	CWDs       []string
}

type SkillsServiceOptions struct {
	Roots                []string
	RootSpecs            []SkillsRoot
	Config               *config.ConfigService
	CodexHome            string
	HomeDir              string
	InstallContext       *install.InstallContext
	IncludeDefaultRoots  bool
	BundledSkillsEnabled *bool
	WatchInterval        time.Duration
}

type skillFrontmatter struct {
	Name        string                   `yaml:"name"`
	Description string                   `yaml:"description"`
	Metadata    skillFrontmatterMetadata `yaml:"metadata"`
}

type skillFrontmatterMetadata struct {
	ShortDescription      string `yaml:"short-description"`
	ShortDescriptionSnake string `yaml:"short_description"`
	ShortDescriptionCamel string `yaml:"shortDescription"`
}

type parsedSkillFrontmatter struct {
	Name             string
	Description      string
	ShortDescription string
}

type skillMetadataFile struct {
	Interface    *skillMetadataInterface    `yaml:"interface"`
	Dependencies *skillMetadataDependencies `yaml:"dependencies"`
	Policy       *skillMetadataPolicy       `yaml:"policy"`
}

type skillMetadataInterface struct {
	DisplayName           string `yaml:"display_name"`
	DisplayNameCamel      string `yaml:"displayName"`
	DisplayNameKebab      string `yaml:"display-name"`
	ShortDescription      string `yaml:"short_description"`
	ShortDescriptionCamel string `yaml:"shortDescription"`
	ShortDescriptionKebab string `yaml:"short-description"`
	IconSmall             string `yaml:"icon_small"`
	IconSmallCamel        string `yaml:"iconSmall"`
	IconSmallKebab        string `yaml:"icon-small"`
	IconLarge             string `yaml:"icon_large"`
	IconLargeCamel        string `yaml:"iconLarge"`
	IconLargeKebab        string `yaml:"icon-large"`
	BrandColor            string `yaml:"brand_color"`
	BrandColorCamel       string `yaml:"brandColor"`
	BrandColorKebab       string `yaml:"brand-color"`
	DefaultPrompt         string `yaml:"default_prompt"`
	DefaultPromptCamel    string `yaml:"defaultPrompt"`
	DefaultPromptKebab    string `yaml:"default-prompt"`
}

type skillMetadataDependencies struct {
	Tools []skillMetadataDependencyTool `yaml:"tools"`
}

type skillMetadataDependencyTool struct {
	Type           string `yaml:"type"`
	TypeKind       string `yaml:"kind"`
	Value          string `yaml:"value"`
	ValueName      string `yaml:"name"`
	ValueServer    string `yaml:"server"`
	ValueMCPServer string `yaml:"mcp_server"`
	ValueMCPCamel  string `yaml:"mcpServer"`
	Description    string `yaml:"description"`
	Transport      string `yaml:"transport"`
	Command        string `yaml:"command"`
	URL            string `yaml:"url"`
}

type skillMetadataPolicy struct {
	AllowImplicitInvocation      *bool    `yaml:"allow_implicit_invocation"`
	AllowImplicitInvocationCamel *bool    `yaml:"allowImplicitInvocation"`
	AllowImplicitInvocationKebab *bool    `yaml:"allow-implicit-invocation"`
	Products                     []string `yaml:"products"`
}

type SkillsService struct {
	mu                sync.Mutex
	roots             []SkillsRoot
	extraRoots        []string
	config            []ConfigEntry
	configFingerprint string
	configService     *config.ConfigService
	cache             map[string]skillsCacheEntry
	watchMu           sync.Mutex
	watchCallback     func()
	watchedRoots      map[string]string
	watchStop         chan struct{}
	watchDone         chan struct{}
	watchInterval     time.Duration
}

type skillsCacheEntry struct {
	skills []SkillsListEntry
	errors []SkillErrorInfo
}

func NewSkillsService(roots []string) *SkillsService {
	return NewSkillsServiceWithOptions(&SkillsServiceOptions{Roots: roots})
}

func NewSkillsServiceWithOptions(options *SkillsServiceOptions) *SkillsService {
	if options == nil {
		options = &SkillsServiceOptions{}
	}
	roots := skillsRootsFromPaths(options.Roots, "local")
	roots = append(roots, options.RootSpecs...)
	if options.IncludeDefaultRoots {
		bundledRoot := bundledSystemSkillsRoot(options.InstallContext)
		bundledEnabled := bundledSkillsEnabled(options)
		if !bundledEnabled {
			systemskills.Uninstall(options.CodexHome)
			bundledRoot = ""
		} else if strings.TrimSpace(options.CodexHome) != "" {
			if err := systemskills.Install(options.CodexHome); err != nil {
				slog.Warn("failed to install embedded system skills", "error", err)
			} else {
				bundledRoot = ""
			}
		}
		roots = append(roots, defaultSkillsRoots(options.CodexHome, options.HomeDir, bundledRoot, bundledEnabled)...)
		roots = append(roots, skillsRootsFromConfigLayers(options.Config)...)
	}
	watchInterval := options.WatchInterval
	if watchInterval <= 0 {
		watchInterval = 10 * time.Second
	}
	return &SkillsService{
		roots:         dedupeSkillsRoots(roots),
		configService: options.Config,
		cache:         map[string]skillsCacheEntry{},
		watchedRoots:  map[string]string{},
		watchInterval: watchInterval,
	}
}

func (s *SkillsService) List(params *SkillsListParams) (*SkillsListResponse, error) {
	if params == nil {
		params = &SkillsListParams{}
	}
	persistentConfig, persistentFingerprint, hasPersistentConfig, err := s.loadPersistentConfig()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if hasPersistentConfig && persistentFingerprint != s.configFingerprint {
		s.config = persistentConfig
		s.configFingerprint = persistentFingerprint
		s.cache = map[string]skillsCacheEntry{}
	}
	configEntries := cloneConfigEntries(s.config)
	configEntries = append(configEntries, cloneConfigEntries(params.Config)...)
	configFingerprint := s.configFingerprint
	if !hasPersistentConfig || len(params.Config) > 0 {
		configFingerprint = configEntriesFingerprint(configEntries)
	}
	key := strings.Join(params.CWDs, "\x00") + "\x00" + strings.Join(s.extraRoots, "\x00") + "\x00" + configFingerprint
	if !params.ForceReload {
		if cached, ok := s.cache[key]; ok {
			return skillsListResponse(cloneSkills(cached.skills), cloneSkillErrors(cached.errors), params.CWDs), nil
		}
	}
	roots := cloneSkillsRoots(s.roots)
	roots = append(roots, skillsRootsFromPaths(s.extraRoots, "user")...)
	roots = append(roots, skillsRootsForCWDs(params.CWDs)...)
	sort.SliceStable(roots, func(i int, j int) bool {
		return skillScopeRank(roots[i].Scope) < skillScopeRank(roots[j].Scope)
	})
	roots = dedupeSkillsRoots(roots)
	entries := make([]SkillsListEntry, 0)
	skillErrors := make([]SkillErrorInfo, 0)
	for _, root := range roots {
		found, foundErrors, err := discover(root)
		if err != nil {
			return nil, err
		}
		entries = append(entries, found...)
		skillErrors = append(skillErrors, foundErrors...)
	}
	entries = dedupeSkillsByPath(entries)
	entries = applyConfig(entries, configEntries)
	sort.SliceStable(entries, func(i int, j int) bool {
		if skillScopeRank(entries[i].Scope) != skillScopeRank(entries[j].Scope) {
			return skillScopeRank(entries[i].Scope) < skillScopeRank(entries[j].Scope)
		}
		if entries[i].Name == entries[j].Name {
			return entries[i].Path < entries[j].Path
		}
		return entries[i].Name < entries[j].Name
	})
	s.cache[key] = skillsCacheEntry{skills: cloneSkills(entries), errors: cloneSkillErrors(skillErrors)}
	return skillsListResponse(entries, skillErrors, params.CWDs), nil
}

func (s *SkillsService) ClearCache() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache = map[string]skillsCacheEntry{}
}

func (s *SkillsService) SetChangedCallback(callback func()) {
	if s == nil {
		return
	}
	s.watchMu.Lock()
	s.watchCallback = callback
	if callback != nil && s.watchStop == nil {
		s.watchStop = make(chan struct{})
		s.watchDone = make(chan struct{})
		go s.watchSkillsLoop(s.watchStop, s.watchDone)
	}
	s.watchMu.Unlock()
}

func (s *SkillsService) WatchCWDs(cwds []string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	roots := cloneSkillsRoots(s.roots)
	roots = append(roots, skillsRootsFromPaths(s.extraRoots, "user")...)
	s.mu.Unlock()
	roots = append(roots, skillsRootsForCWDs(cwds)...)
	sort.SliceStable(roots, func(i int, j int) bool {
		return skillScopeRank(roots[i].Scope) < skillScopeRank(roots[j].Scope)
	})
	s.registerWatchedRoots(dedupeSkillsRoots(roots))
}

func (s *SkillsService) Close() error {
	if s == nil {
		return nil
	}
	s.watchMu.Lock()
	stop := s.watchStop
	done := s.watchDone
	s.watchStop = nil
	s.watchDone = nil
	s.watchCallback = nil
	s.watchMu.Unlock()
	if stop != nil {
		close(stop)
		if done != nil {
			<-done
		}
	}
	return nil
}

func (s *SkillsService) registerWatchedRoots(roots []SkillsRoot) {
	if s == nil || len(roots) == 0 {
		return
	}
	for _, root := range roots {
		path := strings.TrimSpace(root.Path)
		if path == "" || strings.EqualFold(strings.TrimSpace(root.Scope), "environment") || strings.EqualFold(strings.TrimSpace(root.Scope), "plugin") {
			continue
		}
		path = filepath.Clean(path)
		s.watchMu.Lock()
		_, exists := s.watchedRoots[path]
		s.watchMu.Unlock()
		if exists {
			continue
		}
		fingerprint := skillWatchFingerprint(path)
		s.watchMu.Lock()
		if _, exists := s.watchedRoots[path]; !exists {
			s.watchedRoots[path] = fingerprint
		}
		s.watchMu.Unlock()
	}
}

func (s *SkillsService) watchSkillsLoop(stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	interval := s.watchInterval
	if interval <= 0 {
		interval = 10 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			s.pollWatchedSkills()
		}
	}
}

func (s *SkillsService) pollWatchedSkills() {
	s.watchMu.Lock()
	roots := make(map[string]string, len(s.watchedRoots))
	for path, fingerprint := range s.watchedRoots {
		roots[path] = fingerprint
	}
	s.watchMu.Unlock()
	changed := false
	updates := make(map[string]string, len(roots))
	for path, previous := range roots {
		current := skillWatchFingerprint(path)
		updates[path] = current
		if current != previous {
			changed = true
		}
	}
	if !changed {
		return
	}
	s.watchMu.Lock()
	for path, fingerprint := range updates {
		if _, exists := s.watchedRoots[path]; exists {
			s.watchedRoots[path] = fingerprint
		}
	}
	callback := s.watchCallback
	s.watchMu.Unlock()
	s.ClearCache()
	if callback != nil {
		callback()
	}
}

func skillWatchFingerprint(root string) string {
	hash := sha256.New()
	info, err := os.Stat(root)
	if err != nil {
		_, _ = fmt.Fprintf(hash, "missing:%v", err)
		return fmt.Sprintf("%x", hash.Sum(nil))
	}
	if !info.IsDir() {
		writeSkillWatchFile(hash, root, info)
		return fmt.Sprintf("%x", hash.Sum(nil))
	}
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			_, _ = fmt.Fprintf(hash, "error:%s:%v\x00", path, walkErr)
			return nil
		}
		if entry.IsDir() {
			if path != root && strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		fileInfo, statErr := entry.Info()
		if statErr != nil {
			_, _ = fmt.Fprintf(hash, "error:%s:%v\x00", path, statErr)
			return nil
		}
		writeSkillWatchFile(hash, path, fileInfo)
		return nil
	})
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func writeSkillWatchFile(hash interface{ Write([]byte) (int, error) }, path string, info os.FileInfo) {
	_, _ = fmt.Fprintf(hash, "%s\x00%d\x00%d\x00", filepath.Clean(path), info.Size(), info.ModTime().UnixNano())
	if contents, err := os.ReadFile(path); err == nil {
		_, _ = hash.Write(contents)
	}
	_, _ = hash.Write([]byte{0})
}

func (s *SkillsService) SetExtraRoots(params *SkillsExtraRootsSetParams) (*SkillsExtraRootsSetResponse, error) {
	if params == nil {
		return nil, fmt.Errorf("%w: params are nil", ErrInvalidSkillsRequest)
	}
	s.mu.Lock()
	roots := params.ExtraRoots
	if params.ExtraRoots == nil && params.Roots != nil {
		roots = params.Roots
	}
	s.extraRoots = cleanStringSlice(roots)
	s.cache = map[string]skillsCacheEntry{}
	registeredRoots := skillsRootsFromPaths(s.extraRoots, "user")
	s.mu.Unlock()
	s.registerWatchedRoots(registeredRoots)
	return &SkillsExtraRootsSetResponse{}, nil
}

func (s *SkillsService) WriteConfig(params *SkillsConfigWriteParams) (*SkillsConfigWriteResponse, error) {
	if params == nil {
		return nil, fmt.Errorf("%w: params are nil", ErrInvalidSkillsRequest)
	}
	hasName := strings.TrimSpace(params.Name) != ""
	hasPath := strings.TrimSpace(params.Path) != ""
	if hasName == hasPath {
		return nil, fmt.Errorf("%w: skills/config/write requires exactly one of path or name", ErrInvalidSkillsRequest)
	}
	if s.configService != nil {
		if _, err := s.configService.WriteSkillConfig(&config.SkillConfigWriteParams{Name: params.Name, Path: params.Path, Enabled: params.Enabled}); err != nil {
			if errors.Is(err, config.ErrInvalidConfigRequest) {
				return nil, fmt.Errorf("%w: %v", ErrInvalidSkillsRequest, err)
			}
			return nil, err
		}
		persistentConfig, persistentFingerprint, _, err := s.loadPersistentConfig()
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		s.config = persistentConfig
		s.configFingerprint = persistentFingerprint
		s.cache = map[string]skillsCacheEntry{}
		s.mu.Unlock()
		return &SkillsConfigWriteResponse{EffectiveEnabled: params.Enabled, Updated: true}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config = applyConfigEntryEdit(s.config, &ConfigEntry{Name: strings.TrimSpace(params.Name), Path: normalizeSkillConfigPathAppserver(params.Path), Enabled: params.Enabled})
	s.configFingerprint = configEntriesFingerprint(s.config)
	s.cache = map[string]skillsCacheEntry{}
	return &SkillsConfigWriteResponse{EffectiveEnabled: params.Enabled, Updated: true}, nil
}

func (s *SkillsService) loadPersistentConfig() ([]ConfigEntry, string, bool, error) {
	if s == nil || s.configService == nil {
		return nil, "", false, nil
	}
	read, err := s.configService.Read(&config.ConfigReadParams{})
	if err != nil {
		return nil, "", true, err
	}
	entries := skillConfigEntriesFromConfig(read)
	return entries, configEntriesFingerprint(entries), true, nil
}

func (s *SkillsService) applyConfigEntries(entries []SkillsListEntry, turnConfig []ConfigEntry) ([]SkillsListEntry, error) {
	if s == nil {
		return applyConfig(entries, turnConfig), nil
	}
	persistentConfig, persistentFingerprint, hasPersistentConfig, err := s.loadPersistentConfig()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if hasPersistentConfig && persistentFingerprint != s.configFingerprint {
		s.config = persistentConfig
		s.configFingerprint = persistentFingerprint
		s.cache = map[string]skillsCacheEntry{}
	}
	configEntries := cloneConfigEntries(s.config)
	s.mu.Unlock()
	configEntries = append(configEntries, cloneConfigEntries(turnConfig)...)
	return applyConfig(entries, configEntries), nil
}

func discover(root SkillsRoot) ([]SkillsListEntry, []SkillErrorInfo, error) {
	root.Path = strings.TrimSpace(root.Path)
	if root.Scope == "" {
		root.Scope = "local"
	}
	if root.Path == "" {
		return nil, nil, nil
	}
	rootPath := root.Path
	info, err := os.Stat(rootPath)
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if !info.IsDir() && filepath.Base(rootPath) == SkillFilename {
		entry, skillErr, ok := entryFromPath(rootPath, root.Scope, root.PluginID, root.PluginRoot)
		if skillErr != nil {
			skillErr.ApplicableCWDs = append([]string(nil), root.CWDs...)
			return nil, []SkillErrorInfo{*skillErr}, nil
		}
		if !ok {
			return nil, nil, nil
		}
		if !skillMatchesCodexProductRestriction(&entry) {
			return nil, nil, nil
		}
		entry.Root = canonicalSkillRootForIdentity(filepath.Dir(rootPath))
		entry.ApplicableCWDs = append([]string(nil), root.CWDs...)
		return []SkillsListEntry{entry}, nil, nil
	}
	if !info.IsDir() {
		return nil, nil, nil
	}
	entries, skillErrors, err := walkSkillRoot(rootPath, root.Scope, root.PluginID, root.PluginRoot, root.CWDs)
	return entries, skillErrors, err
}

type skillRootWalker struct {
	root           string
	scope          string
	pluginID       string
	pluginRoot     string
	applicableCWDs []string
	followSymlinks bool
	seenDirs       map[string]bool
	directoryCount int
	entries        []SkillsListEntry
	errors         []SkillErrorInfo
}

func walkSkillRoot(rootPath string, scope string, pluginID string, pluginRoot string, applicableCWDs []string) ([]SkillsListEntry, []SkillErrorInfo, error) {
	walker := &skillRootWalker{
		root:           rootPath,
		scope:          scope,
		pluginID:       pluginID,
		pluginRoot:     pluginRoot,
		applicableCWDs: append([]string(nil), applicableCWDs...),
		followSymlinks: !strings.EqualFold(strings.TrimSpace(scope), "system"),
		seenDirs:       map[string]bool{},
		directoryCount: 1,
	}
	walker.markSeen(rootPath)
	if err := walker.walkDir(rootPath, 0); err != nil {
		return nil, nil, err
	}
	return walker.entries, walker.errors, nil
}

func (w *skillRootWalker) walkDir(dir string, depth int) error {
	children, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	sort.SliceStable(children, func(i int, j int) bool {
		return children[i].Name() < children[j].Name()
	})
	for _, child := range children {
		name := child.Name()
		path := filepath.Join(dir, name)
		if child.Type()&os.ModeSymlink != 0 {
			if err := w.walkSymlink(path, name, depth); err != nil {
				return err
			}
			continue
		}
		if child.IsDir() {
			if strings.HasPrefix(name, ".") {
				continue
			}
			if err := w.walkChildDir(path, depth+1); err != nil {
				return err
			}
			continue
		}
		if name == SkillFilename {
			w.addSkill(path)
		}
	}
	return nil
}

func (w *skillRootWalker) walkSymlink(path string, name string, parentDepth int) error {
	if !w.followSymlinks || strings.HasPrefix(name, ".") {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return nil
	}
	return w.walkChildDir(path, parentDepth+1)
}

func (w *skillRootWalker) walkChildDir(path string, depth int) error {
	if depth > skillMaxScanDepth {
		return nil
	}
	if w.directoryCount >= skillMaxDirsPerRoot {
		return nil
	}
	identity := w.dirIdentity(path)
	if w.seenDirs[identity] {
		return nil
	}
	w.seenDirs[identity] = true
	w.directoryCount++
	return w.walkDir(path, depth)
}

func (w *skillRootWalker) addSkill(path string) {
	entry, skillErr, ok := entryFromPath(path, w.scope, w.pluginID, w.pluginRoot)
	if skillErr != nil {
		skillErr.ApplicableCWDs = append([]string(nil), w.applicableCWDs...)
		w.errors = append(w.errors, *skillErr)
		return
	}
	if ok && skillMatchesCodexProductRestriction(&entry) {
		entry.Root = canonicalSkillRootForIdentity(w.root)
		entry.ApplicableCWDs = append([]string(nil), w.applicableCWDs...)
		w.entries = append(w.entries, entry)
	}
}

func (w *skillRootWalker) markSeen(path string) {
	w.seenDirs[w.dirIdentity(path)] = true
}

func (w *skillRootWalker) dirIdentity(path string) string {
	if w.followSymlinks {
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			return filepath.Clean(resolved)
		}
	}
	return filepath.Clean(path)
}

func entryFromPath(path string, scope string, pluginID string, pluginRoot string) (SkillsListEntry, *SkillErrorInfo, bool) {
	skillDir := filepath.Dir(path)
	name := filepath.Base(skillDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if skillErr := skillParseErrorInfo(path, scope, "failed to read file: "+err.Error()); skillErr != nil {
			return SkillsListEntry{}, skillErr, false
		}
		return SkillsListEntry{}, nil, false
	}
	parsed, err := parseSkillFrontmatterResult(string(data), name)
	if err != nil {
		if skillErr := skillParseErrorInfo(path, scope, err.Error()); skillErr != nil {
			return SkillsListEntry{}, skillErr, false
		}
		return SkillsListEntry{}, nil, false
	}
	name = parsed.Name
	description := parsed.Description
	shortDescription := parsed.ShortDescription
	entry := SkillsListEntry{Name: name, Path: canonicalSkillPathForIdentity(path), Scope: firstNonEmpty(scope, "local"), Description: description, ShortDescription: shortDescription, Enabled: true, PluginID: pluginID}
	loadSkillMetadata(&entry, skillDir, pluginRoot)
	return entry, nil, true
}

func canonicalSkillPathForIdentity(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(path)
}

func canonicalSkillRootForIdentity(path string) string {
	return canonicalSkillPathForIdentity(path)
}

func skillParseErrorInfo(path string, scope string, message string) *SkillErrorInfo {
	if strings.EqualFold(strings.TrimSpace(scope), "system") {
		return nil
	}
	return &SkillErrorInfo{Path: filepath.Clean(path), Message: message}
}

func applyConfig(entries []SkillsListEntry, config []ConfigEntry) []SkillsListEntry {
	disabled := map[string]bool{}
	for _, cfg := range config {
		if strings.TrimSpace(cfg.Path) != "" {
			path := normalizeSkillConfigPathAppserver(cfg.Path)
			if cfg.Enabled {
				delete(disabled, path)
			} else {
				disabled[path] = true
			}
			continue
		}
		name := strings.TrimSpace(cfg.Name)
		if name == "" {
			continue
		}
		for _, entry := range entries {
			if entry.Name != name {
				continue
			}
			path := normalizeSkillConfigPathAppserver(entry.Path)
			if cfg.Enabled {
				delete(disabled, path)
			} else {
				disabled[path] = true
			}
		}
	}
	out := cloneSkills(entries)
	var applyEnabled func([]SkillsListEntry)
	applyEnabled = func(values []SkillsListEntry) {
		for i := range values {
			if len(values[i].Skills) > 0 {
				applyEnabled(values[i].Skills)
				continue
			}
			values[i].Enabled = !disabled[normalizeSkillConfigPathAppserver(values[i].Path)]
		}
	}
	applyEnabled(out)
	return out
}

func parseSkillFrontmatter(contents string, defaultName string) (*parsedSkillFrontmatter, bool) {
	parsed, err := parseSkillFrontmatterResult(contents, defaultName)
	if err != nil {
		return nil, false
	}
	return parsed, true
}

func parseSkillFrontmatterResult(contents string, defaultName string) (*parsedSkillFrontmatter, error) {
	frontmatter, _, ok := extractSkillFrontmatter(contents)
	if !ok {
		return nil, errors.New("missing YAML frontmatter delimited by ---")
	}
	var parsed skillFrontmatter
	if err := yaml.Unmarshal([]byte(frontmatter), &parsed); err != nil {
		originalErr := err
		repaired, repairedOK := repairSkillFrontmatterScalarFields(frontmatter)
		if !repairedOK {
			return nil, fmt.Errorf("invalid YAML: %v", originalErr)
		}
		if err := yaml.Unmarshal([]byte(repaired), &parsed); err != nil {
			return nil, fmt.Errorf("invalid YAML: %v", originalErr)
		}
	}
	name := sanitizeSkillSingleLine(parsed.Name)
	if name == "" {
		name = sanitizeSkillSingleLine(defaultName)
	}
	if name == "" {
		name = "skill"
	}
	if len([]rune(name)) > skillMaxNameLen {
		return nil, invalidSkillFieldError("name", skillMaxNameLen)
	}
	description := sanitizeSkillSingleLine(parsed.Description)
	if description == "" {
		return nil, errors.New("missing field `description`")
	}
	return &parsedSkillFrontmatter{
		Name:             name,
		Description:      description,
		ShortDescription: sanitizeSkillSingleLine(parsed.Metadata.shortDescription()),
	}, nil
}

func invalidSkillFieldError(field string, maxLen int) error {
	return fmt.Errorf("invalid %s: exceeds maximum length of %d characters", field, maxLen)
}

func (m skillFrontmatterMetadata) shortDescription() string {
	return m.ShortDescription
}

func extractSkillFrontmatter(contents string) (string, string, bool) {
	contents = strings.TrimPrefix(contents, "\ufeff")
	contents = strings.ReplaceAll(contents, "\r\n", "\n")
	contents = strings.ReplaceAll(contents, "\r", "\n")
	lines := strings.Split(contents, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", contents, false
	}
	frontmatterLines := make([]string, 0)
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return strings.Join(frontmatterLines, "\n"), strings.Join(lines[i+1:], "\n"), len(frontmatterLines) > 0
		}
		frontmatterLines = append(frontmatterLines, lines[i])
	}
	return "", contents, false
}

func repairSkillFrontmatterScalarFields(frontmatter string) (string, bool) {
	changed := false
	blockScalarIndent := -1
	repairedLines := make([]string, 0)
	for _, line := range strings.Split(frontmatter, "\n") {
		indent := leadingSpaces(line)
		if blockScalarIndent >= 0 {
			if strings.TrimSpace(line) == "" || indent > blockScalarIndent {
				repairedLines = append(repairedLines, line)
				continue
			}
			blockScalarIndent = -1
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(key) == "" || !startsWithNoRuneOrWhitespace(value) {
			repairedLines = append(repairedLines, line)
			continue
		}

		trimmedStart := strings.TrimLeftFunc(value, unicode.IsSpace)
		leadingWhitespace := value[:len(value)-len(trimmedStart)]
		scalar := trimmedStart
		comment := ""
		for index, character := range trimmedStart {
			if character == '#' && (index == 0 || previousRuneIsWhitespace(trimmedStart[:index])) {
				commentStart := len(strings.TrimRightFunc(trimmedStart[:index], unicode.IsSpace))
				scalar = trimmedStart[:commentStart]
				comment = trimmedStart[commentStart:]
				break
			}
		}
		scalar = strings.TrimRightFunc(scalar, unicode.IsSpace)
		if scalar == "" {
			repairedLines = append(repairedLines, line)
			continue
		}
		firstRune := []rune(scalar)[0]
		if firstRune == '|' || firstRune == '>' {
			blockScalarIndent = indent
			repairedLines = append(repairedLines, line)
			continue
		}
		if firstRune == '\'' || firstRune == '"' {
			repairedLines = append(repairedLines, line)
			continue
		}
		hasColonSeparator := scalarHasColonSeparator(scalar)
		invalidFlowLikeScalar := strings.ContainsRune("[{@`", firstRune) && yaml.Unmarshal([]byte(scalar), new(any)) != nil
		if !hasColonSeparator && !invalidFlowLikeScalar {
			repairedLines = append(repairedLines, line)
			continue
		}

		quotedScalar := "'" + strings.ReplaceAll(scalar, "'", "''") + "'"
		repairedLines = append(repairedLines, key+":"+leadingWhitespace+quotedScalar+comment)
		changed = true
	}
	if !changed {
		return "", false
	}
	return strings.Join(repairedLines, "\n"), true
}

func leadingSpaces(value string) int {
	count := 0
	for _, character := range value {
		if character != ' ' {
			break
		}
		count++
	}
	return count
}

func startsWithNoRuneOrWhitespace(value string) bool {
	for _, character := range value {
		return unicode.IsSpace(character)
	}
	return true
}

func previousRuneIsWhitespace(value string) bool {
	var previous rune
	found := false
	for _, character := range value {
		previous = character
		found = true
	}
	return !found || unicode.IsSpace(previous)
}

func scalarHasColonSeparator(value string) bool {
	previousWasColon := false
	for _, character := range value {
		if previousWasColon && unicode.IsSpace(character) {
			return true
		}
		previousWasColon = character == ':'
	}
	return false
}

func firstLineFromText(text string) string {
	if _, body, ok := extractSkillFrontmatter(text); ok {
		text = body
	}
	text = strings.TrimSpace(text)
	if index := strings.IndexByte(text, '\n'); index >= 0 {
		text = text[:index]
	}
	text = strings.TrimPrefix(strings.TrimSpace(text), "# ")
	return resolveSkillString(text, skillMaxDescriptionLen)
}

func loadSkillMetadata(entry *SkillsListEntry, skillDir string, pluginRoot string) {
	metadataPath := filepath.Join(skillDir, SkillMetadataDir, SkillMetadataFilename)
	info, err := os.Stat(metadataPath)
	if err != nil || info.IsDir() {
		return
	}
	data, err := os.ReadFile(metadataPath)
	if err != nil {
		return
	}
	var parsed skillMetadataFile
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		return
	}
	if !skillMetadataProductsValid(parsed.Policy) {
		return
	}
	entry.Interface = resolveSkillInterface(parsed.Interface, skillDir, pluginRoot)
	entry.Dependencies = resolveSkillDependencies(parsed.Dependencies)
	entry.Policy = resolveSkillPolicy(parsed.Policy)
}

func resolveSkillInterface(metadata *skillMetadataInterface, skillDir string, pluginRoot string) *SkillInterface {
	if metadata == nil {
		return nil
	}
	value := &SkillInterface{
		DisplayName:      resolveSkillString(metadata.displayName(), skillMaxNameLen),
		ShortDescription: resolveSkillString(metadata.shortDescription(), skillMaxDescriptionLen),
		IconSmall:        resolveSkillAssetPath(skillDir, pluginRoot, metadata.iconSmall()),
		IconLarge:        resolveSkillAssetPath(skillDir, pluginRoot, metadata.iconLarge()),
		BrandColor:       resolveSkillColor(metadata.brandColor()),
		DefaultPrompt:    optionalSkillString(metadata.defaultPrompt(), skillMaxDescriptionLen),
	}
	if value.DisplayName == "" && value.ShortDescription == "" && value.IconSmall == nil && value.IconLarge == nil && value.BrandColor == nil && value.DefaultPrompt == nil {
		return nil
	}
	return value
}

func (m *skillMetadataInterface) displayName() string {
	if m == nil {
		return ""
	}
	return m.DisplayName
}

func (m *skillMetadataInterface) shortDescription() string {
	if m == nil {
		return ""
	}
	return m.ShortDescription
}

func (m *skillMetadataInterface) iconSmall() string {
	if m == nil {
		return ""
	}
	return m.IconSmall
}

func (m *skillMetadataInterface) iconLarge() string {
	if m == nil {
		return ""
	}
	return m.IconLarge
}

func (m *skillMetadataInterface) brandColor() string {
	if m == nil {
		return ""
	}
	return m.BrandColor
}

func (m *skillMetadataInterface) defaultPrompt() string {
	if m == nil {
		return ""
	}
	return m.DefaultPrompt
}

func (m *skillMetadataPolicy) allowImplicitInvocation() *bool {
	if m == nil {
		return nil
	}
	return m.AllowImplicitInvocation
}

func (t *skillMetadataDependencyTool) kind() string {
	if t == nil {
		return ""
	}
	return t.Type
}

func (t *skillMetadataDependencyTool) value() string {
	if t == nil {
		return ""
	}
	return t.Value
}

func resolveSkillDependencies(metadata *skillMetadataDependencies) *SkillDependencies {
	if metadata == nil {
		return nil
	}
	tools := make([]SkillToolDependency, 0, len(metadata.Tools))
	for _, tool := range metadata.Tools {
		kind := resolveSkillString(tool.kind(), skillMaxNameLen)
		value := resolveSkillString(tool.value(), skillMaxDescriptionLen)
		if kind == "" || value == "" {
			continue
		}
		tools = append(tools, SkillToolDependency{
			Type:        kind,
			Value:       value,
			Description: resolveSkillString(tool.Description, skillMaxDescriptionLen),
			Transport:   resolveSkillString(tool.Transport, skillMaxNameLen),
			Command:     optionalSkillString(tool.Command, skillMaxDescriptionLen),
			URL:         optionalSkillString(tool.URL, skillMaxDescriptionLen),
		})
	}
	if len(tools) == 0 {
		return nil
	}
	return &SkillDependencies{Tools: tools}
}

func resolveSkillPolicy(metadata *skillMetadataPolicy) *SkillPolicy {
	if metadata == nil {
		return nil
	}
	policy := &SkillPolicy{
		AllowImplicitInvocation: cloneLocalBoolPtr(metadata.allowImplicitInvocation()),
	}
	for _, product := range metadata.Products {
		product = strings.ToLower(resolveSkillString(product, skillMaxNameLen))
		switch product {
		case "chatgpt", "codex", "atlas":
			policy.Products = append(policy.Products, product)
		}
	}
	return policy
}

func skillScopeRank(scope string) int {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "repo":
		return 0
	case "system":
		return 2
	case "admin":
		return 3
	default:
		return 1
	}
}

func dedupeSkillsByPath(entries []SkillsListEntry) []SkillsListEntry {
	out := make([]SkillsListEntry, 0, len(entries))
	seen := map[string]int{}
	for _, entry := range entries {
		key := normalizeSkillConfigPathAppserver(entry.Path)
		if index, ok := seen[key]; ok {
			out[index].ApplicableCWDs = mergeSkillCWDs(out[index].ApplicableCWDs, entry.ApplicableCWDs)
			continue
		}
		seen[key] = len(out)
		out = append(out, entry)
	}
	return out
}

func skillMetadataProductsValid(metadata *skillMetadataPolicy) bool {
	if metadata == nil {
		return true
	}
	for _, product := range metadata.Products {
		switch product {
		case "chatgpt", "codex", "atlas", "CHATGPT", "CODEX", "ATLAS":
		default:
			return false
		}
	}
	return true
}

func resolveSkillAssetPath(skillDir string, pluginRoot string, value string) *string {
	value = strings.TrimSpace(value)
	if value == "" || filepath.IsAbs(value) || filepath.VolumeName(value) != "" {
		return nil
	}
	normalized := strings.ReplaceAll(value, "\\", "/")
	cleaned := pathpkg.Clean(normalized)
	if cleaned == "." {
		return nil
	}
	if strings.HasPrefix(cleaned, "../") || cleaned == ".." {
		return resolvePluginSharedSkillAssetPath(skillDir, pluginRoot, cleaned)
	}
	parts := strings.Split(cleaned, "/")
	if len(parts) == 0 || parts[0] != "assets" {
		return nil
	}
	for _, part := range parts {
		if part == "" || part == ".." {
			return nil
		}
	}
	resolved := filepath.Join(append([]string{skillDir}, parts...)...)
	return &resolved
}

func resolvePluginSharedSkillAssetPath(skillDir string, pluginRoot string, value string) *string {
	pluginRoot = strings.TrimSpace(pluginRoot)
	if pluginRoot == "" {
		return nil
	}
	pluginAssets := filepath.Clean(filepath.Join(pluginRoot, "assets"))
	resolved := filepath.Clean(filepath.Join(skillDir, filepath.FromSlash(value)))
	relative, err := filepath.Rel(pluginAssets, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil
	}
	if absolute, err := filepath.Abs(resolved); err == nil {
		resolved = absolute
	}
	return &resolved
}

func resolveSkillColor(value string) *string {
	value = strings.TrimSpace(value)
	if len(value) != 7 || value[0] != '#' {
		return nil
	}
	for _, character := range value[1:] {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') || (character >= 'A' && character <= 'F')) {
			return nil
		}
	}
	return &value
}

func optionalSkillString(value string, maxLen int) *string {
	value = resolveSkillString(value, maxLen)
	if value == "" {
		return nil
	}
	return &value
}

func sanitizeSkillSingleLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func resolveSkillString(value string, maxLen int) string {
	value = sanitizeSkillSingleLine(value)
	if value == "" || len([]rune(value)) > maxLen {
		return ""
	}
	return value
}

func defaultSkillsRoots(codexHome string, homeDir string, bundledSystemRoot string, bundledSkillsEnabled bool) []SkillsRoot {
	var roots []SkillsRoot
	codexHome = strings.TrimSpace(codexHome)
	if codexHome != "" {
		roots = append(roots, SkillsRoot{Path: filepath.Join(codexHome, "skills"), Scope: "user"})
		if bundledSkillsEnabled {
			roots = append(roots, SkillsRoot{Path: filepath.Join(codexHome, "skills", ".system"), Scope: "system"})
		}
	}
	homeDir = strings.TrimSpace(homeDir)
	if homeDir == "" {
		if detected, err := os.UserHomeDir(); err == nil {
			homeDir = detected
		}
	}
	if homeDir != "" {
		roots = append(roots, SkillsRoot{Path: filepath.Join(homeDir, ".agents", "skills"), Scope: "user"})
	}
	if bundledSkillsEnabled {
		bundledSystemRoot = strings.TrimSpace(bundledSystemRoot)
	}
	if bundledSkillsEnabled && bundledSystemRoot != "" {
		roots = append(roots, SkillsRoot{Path: bundledSystemRoot, Scope: "system"})
	}
	return roots
}

func skillsRootsFromConfigLayers(service *config.ConfigService) []SkillsRoot {
	if service == nil {
		return nil
	}
	read, err := service.Read(&config.ConfigReadParams{IncludeLayers: true})
	if err != nil || read == nil {
		return nil
	}
	roots := make([]SkillsRoot, 0, len(read.Layers))
	for _, layer := range read.Layers {
		file := strings.TrimSpace(layer.Name.File)
		if file == "" {
			continue
		}
		switch layer.Name.Type {
		case config.LayerSourceUser:
			roots = append(roots, SkillsRoot{Path: filepath.Join(filepath.Dir(file), "skills"), Scope: "user"})
		case config.LayerSourceSystem:
			roots = append(roots, SkillsRoot{Path: filepath.Join(filepath.Dir(file), "skills"), Scope: "admin"})
		}
	}
	return roots
}

func bundledSkillsEnabled(options *SkillsServiceOptions) bool {
	if options == nil {
		return true
	}
	if options.BundledSkillsEnabled != nil {
		return *options.BundledSkillsEnabled
	}
	if options.Config == nil {
		return true
	}
	read, err := options.Config.Read(&config.ConfigReadParams{})
	if err != nil || read == nil {
		return true
	}
	skills, ok := read.Config["skills"].(map[string]any)
	if !ok {
		return true
	}
	bundled, ok := skills["bundled"].(map[string]any)
	if !ok {
		return true
	}
	enabled, ok := bundled["enabled"].(bool)
	return !ok || enabled
}

func bundledSystemSkillsRoot(context *install.InstallContext) string {
	if context == nil {
		context = install.Current()
	}
	if context == nil {
		return ""
	}
	if dir := context.BundledResourceDir(filepath.Join("skills", ".system")); dir != nil {
		return *dir
	}
	return ""
}

func skillsRootsFromPaths(paths []string, scope string) []SkillsRoot {
	out := make([]SkillsRoot, 0, len(paths))
	for _, path := range cleanStringSlice(paths) {
		out = append(out, SkillsRoot{Path: path, Scope: firstNonEmpty(scope, "local")})
	}
	return out
}

func skillsRootsForCWDs(cwds []string) []SkillsRoot {
	var roots []SkillsRoot
	for _, cwd := range cleanStringSlice(cwds) {
		for _, dotCodex := range config.ProjectDotCodexFolders(cwd) {
			roots = append(roots, SkillsRoot{Path: filepath.Join(dotCodex, "skills"), Scope: "repo", CWDs: []string{cwd}})
		}
		for _, agentsRoot := range repoAgentsSkillsRoots(cwd) {
			roots = append(roots, SkillsRoot{Path: agentsRoot, Scope: "repo", CWDs: []string{cwd}})
		}
	}
	return dedupeSkillsRoots(roots)
}

func repoAgentsSkillsRoots(cwd string) []string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return nil
	}
	current, err := filepath.Abs(cwd)
	if err != nil {
		current = filepath.Clean(cwd)
	}
	if info, statErr := os.Stat(current); statErr == nil && !info.IsDir() {
		current = filepath.Dir(current)
	}
	projectRoot := filepath.Clean(config.ActiveProjectRoot(current))
	var roots []string
	for {
		roots = append(roots, filepath.Join(current, ".agents", "skills"))
		if filepath.Clean(current) == projectRoot {
			break
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return roots
}

func dedupeSkillsRoots(roots []SkillsRoot) []SkillsRoot {
	out := make([]SkillsRoot, 0, len(roots))
	seen := map[string]int{}
	for _, root := range roots {
		root.Path = strings.TrimSpace(root.Path)
		root.Scope = strings.TrimSpace(root.Scope)
		if root.Scope == "" {
			root.Scope = "local"
		}
		if root.Path == "" {
			continue
		}
		key := filepath.Clean(root.Path)
		if index, ok := seen[key]; ok {
			out[index].CWDs = mergeSkillCWDs(out[index].CWDs, root.CWDs)
			continue
		}
		seen[key] = len(out)
		root.CWDs = mergeSkillCWDs(nil, root.CWDs)
		out = append(out, root)
	}
	return out
}

func mergeSkillCWDs(left []string, right []string) []string {
	out := append([]string(nil), left...)
	seen := map[string]bool{}
	for _, cwd := range out {
		seen[canonicalSkillCWD(cwd)] = true
	}
	for _, cwd := range right {
		cwd = strings.TrimSpace(cwd)
		key := canonicalSkillCWD(cwd)
		if cwd == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, cwd)
	}
	return out
}

func canonicalSkillCWD(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if absolute, err := filepath.Abs(cwd); err == nil {
		cwd = absolute
	}
	return filepath.Clean(cwd)
}

func cloneSkillsRoots(roots []SkillsRoot) []SkillsRoot {
	out := make([]SkillsRoot, len(roots))
	for i := range roots {
		out[i] = roots[i]
		out[i].CWDs = append([]string(nil), roots[i].CWDs...)
	}
	return out
}

func cleanStringSlice(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func skillConfigEntriesFromConfig(response *config.ConfigReadResponse) []ConfigEntry {
	if response == nil || response.Config == nil {
		return nil
	}
	skills, ok := response.Config["skills"].(map[string]any)
	if !ok {
		return nil
	}
	return skillConfigEntriesFromValue(skills["config"])
}

func skillConfigEntriesFromValues(values map[string]any) []ConfigEntry {
	if values == nil {
		return nil
	}
	skills, ok := values["skills"].(map[string]any)
	if !ok {
		return nil
	}
	return skillConfigEntriesFromValue(skills["config"])
}

func skillConfigEntriesFromValue(value any) []ConfigEntry {
	tables := skillConfigTablesAppserver(value)
	out := make([]ConfigEntry, 0, len(tables))
	for _, table := range tables {
		enabled, ok := table["enabled"].(bool)
		if !ok {
			continue
		}
		name, hasName := table["name"].(string)
		path, hasPath := table["path"].(string)
		name = strings.TrimSpace(name)
		path = strings.TrimSpace(path)
		switch {
		case hasName && !hasPath && name != "":
			out = append(out, ConfigEntry{Name: name, Enabled: enabled})
		case hasPath && !hasName && path != "":
			out = append(out, ConfigEntry{Path: normalizeSkillConfigPathAppserver(path), Enabled: enabled})
		}
	}
	return out
}

func skillConfigTablesAppserver(value any) []map[string]any {
	switch v := value.(type) {
	case []map[string]any:
		out := make([]map[string]any, 0, len(v))
		for _, item := range v {
			out = append(out, cloneAnyMapAppserver(item))
		}
		return out
	case []any:
		out := make([]map[string]any, 0, len(v))
		for _, item := range v {
			if table, ok := item.(map[string]any); ok {
				out = append(out, cloneAnyMapAppserver(table))
			}
		}
		return out
	default:
		return nil
	}
}

func applyConfigEntryEdit(entries []ConfigEntry, entry *ConfigEntry) []ConfigEntry {
	if entry == nil {
		return cloneConfigEntries(entries)
	}
	normalized := ConfigEntry{
		Name:    strings.TrimSpace(entry.Name),
		Path:    normalizeSkillConfigPathAppserver(entry.Path),
		Enabled: entry.Enabled,
	}
	if normalized.Name == "" && normalized.Path == "" {
		return cloneConfigEntries(entries)
	}
	out := cloneConfigEntries(entries)
	index := -1
	for i := range out {
		if configEntrySameSelector(&out[i], &normalized) {
			index = i
			break
		}
	}
	if normalized.Enabled {
		if index >= 0 {
			return append(out[:index], out[index+1:]...)
		}
		return out
	}
	if index >= 0 {
		out[index] = normalized
		return out
	}
	return append(out, normalized)
}

func configEntrySameSelector(left *ConfigEntry, right *ConfigEntry) bool {
	if left == nil || right == nil {
		return false
	}
	if strings.TrimSpace(left.Name) != "" || strings.TrimSpace(right.Name) != "" {
		return strings.TrimSpace(left.Name) != "" &&
			strings.TrimSpace(right.Name) != "" &&
			strings.TrimSpace(left.Name) == strings.TrimSpace(right.Name) &&
			strings.TrimSpace(left.Path) == "" &&
			strings.TrimSpace(right.Path) == ""
	}
	return normalizeSkillConfigPathAppserver(left.Path) != "" &&
		normalizeSkillConfigPathAppserver(left.Path) == normalizeSkillConfigPathAppserver(right.Path)
}

func cloneConfigEntries(entries []ConfigEntry) []ConfigEntry {
	out := make([]ConfigEntry, len(entries))
	copy(out, entries)
	return out
}

func configEntriesFingerprint(entries []ConfigEntry) string {
	data, err := json.Marshal(entries)
	if err != nil {
		return ""
	}
	return string(data)
}

func normalizeSkillConfigPathAppserver(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	} else if absolute, absErr := filepath.Abs(path); absErr == nil {
		path = absolute
	}
	return filepath.Clean(path)
}

func cloneAnyMapAppserver(values map[string]any) map[string]any {
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func cloneSkills(skills []SkillsListEntry) []SkillsListEntry {
	if skills == nil {
		return nil
	}
	out := make([]SkillsListEntry, len(skills))
	for i := range skills {
		out[i] = cloneSkill(skills[i])
	}
	return out
}

func cloneSkillErrors(errors []SkillErrorInfo) []SkillErrorInfo {
	if errors == nil {
		return nil
	}
	out := append([]SkillErrorInfo(nil), errors...)
	for i := range out {
		out[i].ApplicableCWDs = append([]string(nil), errors[i].ApplicableCWDs...)
	}
	return out
}

func skillsListResponse(skills []SkillsListEntry, skillErrors []SkillErrorInfo, cwds []string) *SkillsListResponse {
	response := &SkillsListResponse{Skills: cloneSkills(skills), Errors: cloneSkillErrors(skillErrors)}
	if len(cwds) == 0 {
		response.Data = []SkillsListEntry{{
			CWD:    "",
			Skills: cloneSkills(skills),
			Errors: cloneSkillErrors(skillErrors),
		}}
		if response.Data[0].Errors == nil {
			response.Data[0].Errors = []SkillErrorInfo{}
		}
		return response
	}
	data := make([]SkillsListEntry, 0, len(cwds))
	for _, cwd := range cwds {
		cwd = strings.TrimSpace(cwd)
		if cwd == "" {
			continue
		}
		var scoped []SkillsListEntry
		for _, skill := range skills {
			if skillAppliesToCWD(skill, cwd) {
				scoped = append(scoped, cloneSkill(skill))
			}
		}
		data = append(data, SkillsListEntry{CWD: cwd, Skills: scoped, Errors: skillErrorsForCWD(skillErrors, cwd)})
	}
	response.Data = data
	return response
}

func skillErrorsForCWD(errors []SkillErrorInfo, cwd string) []SkillErrorInfo {
	if len(errors) == 0 {
		return []SkillErrorInfo{}
	}
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return cloneSkillErrors(errors)
	}
	cleanCWD := filepath.Clean(cwd)
	out := make([]SkillErrorInfo, 0, len(errors))
	for _, skillErr := range errors {
		if skillCWDMatches(skillErr.ApplicableCWDs, cwd) {
			out = append(out, skillErr)
			continue
		}
		if len(skillErr.ApplicableCWDs) > 0 {
			continue
		}
		path := filepath.Clean(skillErr.Path)
		if path == cleanCWD || strings.HasPrefix(path, cleanCWD+string(os.PathSeparator)) {
			out = append(out, skillErr)
		}
	}
	if out == nil {
		out = []SkillErrorInfo{}
	}
	return out
}

func skillAppliesToCWD(skill SkillsListEntry, cwd string) bool {
	if len(skill.ApplicableCWDs) > 0 {
		return skillCWDMatches(skill.ApplicableCWDs, cwd)
	}
	if !strings.EqualFold(strings.TrimSpace(skill.Scope), "repo") {
		return true
	}
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return true
	}
	cleanCWD := filepath.Clean(cwd)
	return strings.HasPrefix(skill.Path, cwd+string(os.PathSeparator)) ||
		skill.Path == cwd ||
		strings.HasPrefix(skill.Path, cleanCWD+string(os.PathSeparator)) ||
		skill.Path == cleanCWD
}

func skillCWDMatches(applicable []string, cwd string) bool {
	want := canonicalSkillCWD(cwd)
	for _, candidate := range applicable {
		if canonicalSkillCWD(candidate) == want {
			return true
		}
	}
	return false
}

func cloneSkill(skill SkillsListEntry) SkillsListEntry {
	out := skill
	out.ApplicableCWDs = append([]string(nil), skill.ApplicableCWDs...)
	out.Skills = cloneSkills(skill.Skills)
	out.Errors = cloneSkillErrors(skill.Errors)
	if skill.Interface != nil {
		value := *skill.Interface
		value.IconSmall = cloneLocalStringPtr(skill.Interface.IconSmall)
		value.IconLarge = cloneLocalStringPtr(skill.Interface.IconLarge)
		value.BrandColor = cloneLocalStringPtr(skill.Interface.BrandColor)
		value.DefaultPrompt = cloneLocalStringPtr(skill.Interface.DefaultPrompt)
		out.Interface = &value
	}
	if skill.Dependencies != nil {
		value := &SkillDependencies{Tools: make([]SkillToolDependency, len(skill.Dependencies.Tools))}
		for i := range skill.Dependencies.Tools {
			value.Tools[i] = skill.Dependencies.Tools[i]
			value.Tools[i].Command = cloneLocalStringPtr(skill.Dependencies.Tools[i].Command)
			value.Tools[i].URL = cloneLocalStringPtr(skill.Dependencies.Tools[i].URL)
		}
		out.Dependencies = value
	}
	out.Policy = cloneSkillPolicy(skill.Policy)
	out.Contents = skill.Contents
	return out
}

func cloneLocalStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneLocalBoolPtr(value *bool) *bool {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneSkillPolicy(value *SkillPolicy) *SkillPolicy {
	if value == nil {
		return nil
	}
	return &SkillPolicy{
		AllowImplicitInvocation: cloneLocalBoolPtr(value.AllowImplicitInvocation),
		Products:                append([]string(nil), value.Products...),
	}
}
