package plugin

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

// MarketplaceAllowedSourceKind mirrors Rust MarketplaceAllowedSourceKind.
type MarketplaceAllowedSourceKind string

const (
	MarketplaceAllowedSourceGit         MarketplaceAllowedSourceKind = "git"
	MarketplaceAllowedSourceHostPattern MarketplaceAllowedSourceKind = "host_pattern"
	MarketplaceAllowedSourceLocal       MarketplaceAllowedSourceKind = "local"
)

// MarketplaceAllowedSource is one raw marketplace rule from requirements. The
// pointer fields preserve omission so requirement layers can merge field by
// field, matching Rust MarketplaceAllowedSourceToml.
type MarketplaceAllowedSource struct {
	Source      *MarketplaceAllowedSourceKind
	URL         *string
	RefName     *string
	HostPattern *string
	Path        *string
}

// MarketplaceRequirements mirrors Rust MarketplaceRequirementsToml plus the
// source label Rust keeps in Sourced<MarketplaceRequirementsToml>.
type MarketplaceRequirements struct {
	RestrictToAllowedSources *bool
	AllowedSources           map[string]MarketplaceAllowedSource
	RequirementSource        string
}

func (r *MarketplaceRequirements) IsEmpty() bool {
	return r == nil || (r.RestrictToAllowedSources == nil && len(r.AllowedSources) == 0)
}

func CloneMarketplaceRequirements(requirements *MarketplaceRequirements) *MarketplaceRequirements {
	if requirements == nil {
		return nil
	}
	out := &MarketplaceRequirements{
		RestrictToAllowedSources: cloneBoolPointer(requirements.RestrictToAllowedSources),
		RequirementSource:        requirements.RequirementSource,
	}
	if requirements.AllowedSources != nil {
		out.AllowedSources = make(map[string]MarketplaceAllowedSource, len(requirements.AllowedSources))
		for key, source := range requirements.AllowedSources {
			out.AllowedSources[key] = cloneMarketplaceAllowedSource(source)
		}
	}
	return out
}

// MergeMarketplaceRequirements follows Rust's default TOML merge for
// requirements layers: a higher layer replaces scalar values it declares while
// retaining undeclared fields and unrelated allowed sources from lower layers.
func MergeMarketplaceRequirements(base *MarketplaceRequirements, overlay *MarketplaceRequirements) *MarketplaceRequirements {
	if overlay == nil {
		return CloneMarketplaceRequirements(base)
	}
	out := CloneMarketplaceRequirements(base)
	if out == nil {
		out = &MarketplaceRequirements{}
	}
	if overlay.RestrictToAllowedSources != nil {
		out.RestrictToAllowedSources = cloneBoolPointer(overlay.RestrictToAllowedSources)
	}
	if overlay.RequirementSource != "" {
		out.RequirementSource = overlay.RequirementSource
	}
	if overlay.AllowedSources != nil {
		if out.AllowedSources == nil {
			out.AllowedSources = map[string]MarketplaceAllowedSource{}
		}
		for key, source := range overlay.AllowedSources {
			merged := out.AllowedSources[key]
			if source.Source != nil {
				merged.Source = cloneMarketplaceAllowedSourceKind(source.Source)
			}
			if source.URL != nil {
				merged.URL = cloneStringPtr(source.URL)
			}
			if source.RefName != nil {
				merged.RefName = cloneStringPtr(source.RefName)
			}
			if source.HostPattern != nil {
				merged.HostPattern = cloneStringPtr(source.HostPattern)
			}
			if source.Path != nil {
				merged.Path = cloneStringPtr(source.Path)
			}
			out.AllowedSources[key] = merged
		}
	}
	return out
}

func cloneMarketplaceAllowedSource(source MarketplaceAllowedSource) MarketplaceAllowedSource {
	return MarketplaceAllowedSource{
		Source:      cloneMarketplaceAllowedSourceKind(source.Source),
		URL:         cloneStringPtr(source.URL),
		RefName:     cloneStringPtr(source.RefName),
		HostPattern: cloneStringPtr(source.HostPattern),
		Path:        cloneStringPtr(source.Path),
	}
}

func cloneMarketplaceAllowedSourceKind(kind *MarketplaceAllowedSourceKind) *MarketplaceAllowedSourceKind {
	if kind == nil {
		return nil
	}
	cloned := *kind
	return &cloned
}

func cloneBoolPointer(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

type allowedMarketplaceSource struct {
	kind        MarketplaceAllowedSourceKind
	url         string
	refName     *string
	hostPattern *regexp.Regexp
	localPath   string
}

type restrictedMarketplacePolicy struct {
	allowedSources []allowedMarketplaceSource
	compileError   error
	source         string
}

// MarketplacePolicy is the normalized policy used to decide whether a
// marketplace source may be added, discovered, or installed from.
type MarketplacePolicy struct {
	restricted *restrictedMarketplacePolicy
}

func NewMarketplacePolicy(requirements *MarketplaceRequirements) *MarketplacePolicy {
	policy := &MarketplacePolicy{}
	if requirements == nil || requirements.RestrictToAllowedSources == nil || !*requirements.RestrictToAllowedSources {
		return policy
	}
	source := strings.TrimSpace(requirements.RequirementSource)
	if source == "" {
		source = "managed requirements"
	}
	keys := make([]string, 0, len(requirements.AllowedSources))
	for key := range requirements.AllowedSources {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	restricted := &restrictedMarketplacePolicy{source: source}
	for _, key := range keys {
		allowed, err := compileAllowedMarketplaceSource(key, requirements.AllowedSources[key], source)
		if err != nil && restricted.compileError == nil {
			restricted.compileError = err
		}
		if err == nil {
			restricted.allowedSources = append(restricted.allowedSources, allowed)
		}
	}
	policy.restricted = restricted
	return policy
}

func (p *MarketplacePolicy) IsRestricted() bool {
	return p != nil && p.restricted != nil
}

func (p *MarketplacePolicy) ValidateSource(source *ParsedMarketplaceSource) error {
	if p == nil || p.restricted == nil {
		return nil
	}
	if p.restricted.compileError != nil {
		return p.restricted.compileError
	}
	for _, allowed := range p.restricted.allowedSources {
		if allowed.matches(source) {
			return nil
		}
	}
	return fmt.Errorf("marketplace source `%s` is not allowed by requirements from %s", source.Display(), p.restricted.source)
}

func (p *MarketplacePolicy) ValidateGitSource(source string, refName *string) (*ParsedMarketplaceSource, error) {
	if p == nil || p.restricted == nil {
		return nil, nil
	}
	parsed, err := ParseMarketplaceSource(source, refName)
	if err != nil {
		return nil, err
	}
	if parsed.Kind != MarketplaceSourceGit {
		return nil, fmt.Errorf("configured Git marketplace source is not a Git URL")
	}
	if err := p.ValidateSource(parsed); err != nil {
		return nil, err
	}
	return parsed, nil
}

func (s allowedMarketplaceSource) matches(source *ParsedMarketplaceSource) bool {
	if source == nil {
		return false
	}
	switch s.kind {
	case MarketplaceAllowedSourceGit:
		if source.Kind != MarketplaceSourceGit || source.URL != s.url {
			return false
		}
		return s.refName == nil || source.RefName != nil && strings.TrimSpace(*s.refName) == strings.TrimSpace(*source.RefName)
	case MarketplaceAllowedSourceHostPattern:
		if source.Kind != MarketplaceSourceGit {
			return false
		}
		hostname, ok := gitMarketplaceHostname(source.URL)
		return ok && s.hostPattern.MatchString(hostname)
	case MarketplaceAllowedSourceLocal:
		return source.Kind == MarketplaceSourceLocal && pathsMatchAfterNormalization(s.localPath, source.Path)
	default:
		return false
	}
}

func compileAllowedMarketplaceSource(key string, allowed MarketplaceAllowedSource, requirementSource string) (allowedMarketplaceSource, error) {
	invalid := func(reason string) (allowedMarketplaceSource, error) {
		return allowedMarketplaceSource{}, fmt.Errorf("invalid marketplace allowed source `%s` in %s: %s", key, requirementSource, reason)
	}
	if allowed.Source == nil {
		return invalid("missing source")
	}
	switch *allowed.Source {
	case MarketplaceAllowedSourceGit:
		rawURL := trimmedStringPointer(allowed.URL)
		if rawURL == "" {
			return invalid("missing url")
		}
		var refName *string
		if allowed.RefName != nil {
			trimmed := strings.TrimSpace(*allowed.RefName)
			if trimmed == "" {
				return invalid("ref must not be empty")
			}
			refName = &trimmed
		}
		parsed, err := ParseMarketplaceSource(rawURL, refName)
		if err != nil {
			return invalid(err.Error())
		}
		if parsed.Kind != MarketplaceSourceGit {
			return invalid("expected a Git URL")
		}
		return allowedMarketplaceSource{kind: MarketplaceAllowedSourceGit, url: parsed.URL, refName: cloneStringPtr(parsed.RefName)}, nil
	case MarketplaceAllowedSourceHostPattern:
		pattern := trimmedStringPointer(allowed.HostPattern)
		if pattern == "" {
			return invalid("missing host_pattern")
		}
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			return invalid(err.Error())
		}
		return allowedMarketplaceSource{kind: MarketplaceAllowedSourceHostPattern, hostPattern: compiled}, nil
	case MarketplaceAllowedSourceLocal:
		path := trimmedStringPointer(allowed.Path)
		if path == "" {
			return invalid("missing path")
		}
		if !filepath.IsAbs(path) && !looksLikeWindowsMarketplaceAbsolutePath(path) {
			return invalid("local path must be absolute")
		}
		return allowedMarketplaceSource{kind: MarketplaceAllowedSourceLocal, localPath: filepath.Clean(path)}, nil
	default:
		return invalid(fmt.Sprintf("unsupported source `%s`", *allowed.Source))
	}
}

func trimmedStringPointer(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

// ValidateInstall applies the managed-source and configured-root checks Rust
// runs before installing a plugin from a marketplace.
func (p *MarketplacePolicy) ValidateInstall(codexHome string, marketplacePath string, marketplaceName string, configValues map[string]any) error {
	root, err := marketplaceRootDir(marketplacePath)
	if err != nil {
		return err
	}
	if expectedName := managedMarketplaceName(codexHome, marketplacePath, root); expectedName != "" {
		if isOpenAICuratedMarketplaceName(expectedName) {
			if _, err := p.ValidateGitSource(OpenAIPluginsGitURL, nil); err != nil {
				return err
			}
		}
		return validateExpectedMarketplaceName(expectedName, marketplaceName)
	}
	if IsReservedMarketplaceName(marketplaceName) {
		return fmt.Errorf("marketplace `%s` is reserved and cannot be loaded from this source", marketplaceName)
	}
	if !p.IsRestricted() {
		return nil
	}
	marketplace, ok := configuredMarketplaceEntry(configValues, marketplaceName)
	if !ok {
		return fmt.Errorf("marketplace `%s` must be added to config before plugins can be installed while marketplace source restrictions are enabled", marketplaceName)
	}
	if err := p.ValidateConfiguredMarketplaceSource(marketplaceName, marketplace); err != nil {
		return err
	}
	configuredRoot, ok := resolveConfiguredMarketplaceRoot(marketplaceName, marketplace, marketplaceInstallRoot(codexHome))
	if !ok {
		return fmt.Errorf("configured marketplace `%s` does not have a usable root", marketplaceName)
	}
	if !pathsMatchAfterNormalization(configuredRoot, root) {
		return fmt.Errorf("marketplace path `%s` does not match configured marketplace `%s`", root, marketplaceName)
	}
	return nil
}

// ValidateConfiguredMarketplaceSource checks one effective-config entry.
func (p *MarketplacePolicy) ValidateConfiguredMarketplaceSource(marketplaceName string, marketplace map[string]any) error {
	source, err := configuredMarketplaceSource(marketplaceName, marketplace)
	if err != nil {
		return err
	}
	return p.ValidateSource(source)
}

// AllowedConfiguredMarketplaceNames filters the effective `[marketplaces]`
// table, preserving managed local entries only at their owned path.
func (p *MarketplacePolicy) AllowedConfiguredMarketplaceNames(codexHome string, configValues map[string]any) map[string]bool {
	table, ok := configuredMarketplacesTable(configValues)
	if !ok {
		return map[string]bool{}
	}
	out := make(map[string]bool, len(table))
	for name, rawEntry := range table {
		entry, ok := rawEntry.(map[string]any)
		if !ok {
			continue
		}
		allowed := false
		if expected := managedMarketplaceConfigName(codexHome, entry); expected != "" {
			allowed = expected == name
		} else if IsReservedMarketplaceName(name) {
			allowed = false
		} else if !p.IsRestricted() {
			allowed = true
		} else {
			allowed = p.ValidateConfiguredMarketplaceSource(name, entry) == nil
		}
		if allowed {
			out[name] = true
		}
	}
	return out
}

// ValidateMarketplaceSourceForAdd mirrors Rust's managed-local exemption and
// returns the expected managed name when the source is owned by Codex.
func (p *MarketplacePolicy) ValidateMarketplaceSourceForAdd(codexHome string, source *ParsedMarketplaceSource) (string, error) {
	if source != nil && source.Kind == MarketplaceSourceLocal {
		if expected := managedLocalMarketplaceName(codexHome, source.Path); expected != "" {
			return expected, nil
		}
	}
	if !p.IsRestricted() {
		return "", nil
	}
	if err := p.ValidateSource(source); err != nil {
		return "", err
	}
	return "", nil
}

func ValidateMarketplaceNameForAdd(expectedName string, marketplaceName string) error {
	if expectedName != "" {
		return validateExpectedMarketplaceName(expectedName, marketplaceName)
	}
	if IsReservedMarketplaceName(marketplaceName) {
		return fmt.Errorf("marketplace `%s` is reserved and cannot be added from this source", marketplaceName)
	}
	return nil
}

// FilterPluginConfig returns a projected copy of effective config with
// disallowed marketplaces and plugin entries removed (Rust
// policy_filtered_plugin_config).
func (p *MarketplacePolicy) FilterPluginConfig(codexHome string, configValues map[string]any) (map[string]any, bool) {
	marketplaceTable, hasMarketplaces := configuredMarketplacesTable(configValues)
	_, hasPlugins := configValues["plugins"].(map[string]any)
	if !hasMarketplaces && !hasPlugins {
		return nil, false
	}
	cloned, _ := cloneMarketplacePolicyValue(configValues).(map[string]any)
	if cloned == nil {
		cloned = map[string]any{}
	}
	allowedNames := p.AllowedConfiguredMarketplaceNames(codexHome, configValues)
	configuredNames := make(map[string]bool, len(marketplaceTable))
	for name := range marketplaceTable {
		configuredNames[name] = true
	}
	if projectedMarketplaces, ok := cloned["marketplaces"].(map[string]any); ok {
		for name := range projectedMarketplaces {
			if !allowedNames[name] {
				delete(projectedMarketplaces, name)
			}
		}
	}
	if projectedPlugins, ok := cloned["plugins"].(map[string]any); ok {
		for key := range projectedPlugins {
			if key == PluginsDefaultConfigKey {
				continue
			}
			id, err := ParsePluginId(key)
			if err != nil {
				if p.IsRestricted() {
					delete(projectedPlugins, key)
				}
				continue
			}
			if configuredNames[id.MarketplaceName] {
				if !allowedNames[id.MarketplaceName] {
					delete(projectedPlugins, key)
				}
				continue
			}
			if isOpenAICuratedMarketplaceName(id.MarketplaceName) {
				if _, err := p.ValidateGitSource(OpenAIPluginsGitURL, nil); err != nil {
					delete(projectedPlugins, key)
				}
				continue
			}
			if p.IsRestricted() {
				delete(projectedPlugins, key)
			}
		}
	}
	return cloned, true
}

func cloneMarketplacePolicyValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, nested := range typed {
			out[key] = cloneMarketplacePolicyValue(nested)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i := range typed {
			out[i] = cloneMarketplacePolicyValue(typed[i])
		}
		return out
	case []string:
		return append([]string(nil), typed...)
	default:
		return typed
	}
}

func configuredMarketplacesTable(configValues map[string]any) (map[string]any, bool) {
	if configValues == nil {
		return nil, false
	}
	table, ok := configValues["marketplaces"].(map[string]any)
	return table, ok
}

func configuredMarketplaceEntry(configValues map[string]any, name string) (map[string]any, bool) {
	table, ok := configuredMarketplacesTable(configValues)
	if !ok {
		return nil, false
	}
	entry, ok := table[name].(map[string]any)
	return entry, ok
}

func configuredMarketplaceSource(marketplaceName string, marketplace map[string]any) (*ParsedMarketplaceSource, error) {
	sourceType := stringConfigValue(marketplace["source_type"])
	if sourceType == "" {
		return nil, fmt.Errorf("configured marketplace `%s` is missing source_type", marketplaceName)
	}
	source := stringConfigValue(marketplace["source"])
	if source == "" {
		return nil, fmt.Errorf("configured marketplace `%s` is missing source", marketplaceName)
	}
	switch sourceType {
	case string(MarketplaceSourceLocal):
		return &ParsedMarketplaceSource{Kind: MarketplaceSourceLocal, Path: filepath.Clean(source)}, nil
	case string(MarketplaceSourceGit):
		refName := stringPtrIfNotEmpty(stringConfigValue(marketplace["ref"]))
		parsed, err := ParseMarketplaceSource(source, refName)
		if err != nil {
			return nil, fmt.Errorf("invalid source for marketplace `%s`: %v", marketplaceName, err)
		}
		if parsed.Kind != MarketplaceSourceGit {
			return nil, fmt.Errorf("configured marketplace `%s` source does not match source_type `git`", marketplaceName)
		}
		return parsed, nil
	default:
		return nil, fmt.Errorf("invalid config for marketplace `%s`: unknown source_type `%s`", marketplaceName, sourceType)
	}
}

func resolveConfiguredMarketplaceRoot(marketplaceName string, marketplace map[string]any, defaultInstallRoot string) (string, bool) {
	if stringConfigValue(marketplace["source_type"]) == string(MarketplaceSourceLocal) {
		source := stringConfigValue(marketplace["source"])
		if source == "" {
			return "", false
		}
		return filepath.Clean(source), true
	}
	if strings.TrimSpace(marketplaceName) == "" {
		return "", false
	}
	return filepath.Join(defaultInstallRoot, marketplaceName), true
}

func marketplaceInstallRoot(codexHome string) string {
	return filepath.Join(codexHome, ".tmp", "marketplaces")
}

func marketplaceRootDir(marketplacePath string) (string, error) {
	clean := filepath.Clean(strings.TrimSpace(marketplacePath))
	if clean == "" || clean == "." || (!filepath.IsAbs(clean) && !looksLikeWindowsMarketplaceAbsolutePath(clean)) {
		return "", fmt.Errorf("invalid marketplace file layout `%s`", marketplacePath)
	}
	for _, relative := range marketplaceManifestRelativePaths {
		suffix := string(filepath.Separator) + filepath.Clean(relative)
		if pathHasSuffixFold(clean, suffix) {
			root := strings.TrimSuffix(clean, suffix)
			if root == "" {
				root = string(filepath.Separator)
			}
			return filepath.Clean(root), nil
		}
	}
	return "", fmt.Errorf("invalid marketplace file layout `%s`", marketplacePath)
}

func pathHasSuffixFold(path string, suffix string) bool {
	if runtime.GOOS == "windows" {
		return strings.HasSuffix(strings.ToLower(path), strings.ToLower(suffix))
	}
	return strings.HasSuffix(path, suffix)
}

func managedMarketplaceName(codexHome string, marketplacePath string, root string) string {
	curatedRoot := filepath.Join(codexHome, ".tmp", "plugins")
	apiMarketplace := filepath.Join(curatedRoot, ".agents", "plugins", "api_marketplace.json")
	if isExpectedManagedPath(marketplacePath, apiMarketplace) && isExpectedManagedPath(root, curatedRoot) {
		return OpenAIAPICuratedMarketplaceName
	}
	if isExpectedManagedPath(root, curatedRoot) {
		return OpenAICuratedMarketplaceName
	}
	return managedLocalMarketplaceName(codexHome, root)
}

func managedMarketplaceConfigName(codexHome string, marketplace map[string]any) string {
	if stringConfigValue(marketplace["source_type"]) != string(MarketplaceSourceLocal) {
		return ""
	}
	path := stringConfigValue(marketplace["source"])
	if path == "" || (!filepath.IsAbs(path) && !looksLikeWindowsMarketplaceAbsolutePath(path)) {
		return ""
	}
	return managedLocalMarketplaceName(codexHome, path)
}

func managedLocalMarketplaceName(codexHome string, root string) string {
	for _, name := range []string{OpenAIBundledMarketplaceName, "openai-bundled-alpha"} {
		expectedRoot := filepath.Join(codexHome, ".tmp", "bundled-marketplaces", name)
		if isExpectedManagedPath(root, expectedRoot) {
			return name
		}
	}
	if runtimeRoot := primaryRuntimeMarketplaceRoot(); runtimeRoot != "" && isExpectedManagedPath(root, runtimeRoot) {
		return "openai-primary-runtime"
	}
	return ""
}

func primaryRuntimeMarketplaceRoot() string {
	var cacheRoot string
	switch runtime.GOOS {
	case "windows":
		if profile := strings.TrimSpace(os.Getenv("USERPROFILE")); profile != "" {
			cacheRoot = filepath.Join(profile, ".cache")
		}
	case "darwin":
		if home, err := os.UserHomeDir(); err == nil {
			cacheRoot = filepath.Join(home, ".cache")
		}
	default:
		cacheRoot, _ = os.UserCacheDir()
	}
	if cacheRoot == "" {
		return ""
	}
	return filepath.Join(cacheRoot, "codex-runtimes", "codex-primary-runtime", "plugins", "openai-primary-runtime")
}

func isExpectedManagedPath(path string, expected string) bool {
	if normalizeManagedPath(path) == normalizeManagedPath(expected) {
		return true
	}
	if runtime.GOOS == "windows" {
		// Resolve only the owned root. A path that merely canonicalizes to the
		// owned root (for example an arbitrary junction) must not inherit its
		// managed provenance.
		canonicalExpected, err := filepath.EvalSymlinks(filepath.Clean(strings.TrimSpace(expected)))
		if err != nil {
			return false
		}
		return normalizeManagedPath(path) == normalizeManagedPath(canonicalExpected)
	}
	if runtime.GOOS == "darwin" {
		return managedMacPathVariable(path) == normalizeManagedPath(expected) ||
			managedMacPathVariable(expected) == normalizeManagedPath(path)
	}
	return false
}

func normalizeManagedPath(path string) string {
	path = filepath.Clean(strings.TrimSpace(path))
	if runtime.GOOS == "windows" {
		path = strings.ReplaceAll(path, "/", `\`)
		for strings.HasPrefix(path, `\\?\`) {
			path = path[4:]
		}
		return strings.ToLower(path)
	}
	return path
}

func managedMacPathVariable(path string) string {
	clean := filepath.Clean(strings.TrimSpace(path))
	if strings.HasPrefix(clean, "/private/var/") {
		return filepath.Join("/var", strings.TrimPrefix(clean, "/private/var/"))
	}
	return clean
}

func validateExpectedMarketplaceName(expectedName string, marketplaceName string) error {
	if marketplaceName == expectedName {
		return nil
	}
	return fmt.Errorf("marketplace manifest name `%s` does not match managed marketplace `%s`", marketplaceName, expectedName)
}

func isOpenAICuratedMarketplaceName(name string) bool {
	return name == OpenAICuratedMarketplaceName || name == OpenAIAPICuratedMarketplaceName
}

func gitMarketplaceHostname(rawURL string) (string, bool) {
	rawURL = strings.TrimSpace(rawURL)
	authorityAndPath := ""
	if strings.HasPrefix(rawURL, "http://") {
		authorityAndPath = strings.TrimPrefix(rawURL, "http://")
	} else if strings.HasPrefix(rawURL, "https://") {
		authorityAndPath = strings.TrimPrefix(rawURL, "https://")
	}
	if authorityAndPath != "" {
		authorityAndPath = strings.TrimLeft(authorityAndPath, "/")
		authorityParts := strings.FieldsFunc(authorityAndPath, func(r rune) bool { return r == '/' || r == '?' || r == '#' })
		if len(authorityParts) == 0 {
			return "", false
		}
		authority := authorityParts[0]
		if at := strings.LastIndex(authority, "@"); at >= 0 && strings.Contains(authority[:at], "\\") {
			return "", false
		}
	}
	if parsed, err := url.Parse(rawURL); err == nil && parsed.Hostname() != "" {
		return strings.ToLower(parsed.Hostname()), true
	}
	at := strings.LastIndex(rawURL, "@")
	if at < 0 {
		return "", false
	}
	hostAndPath := rawURL[at+1:]
	colon := strings.Index(hostAndPath, ":")
	if colon <= 0 {
		return "", false
	}
	return strings.ToLower(hostAndPath[:colon]), true
}
