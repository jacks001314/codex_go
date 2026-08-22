package config

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

var ErrInvalidCloudConfig = errors.New("invalid cloud config")

type CloudConfigFragment struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Contents string `json:"contents"`
}

type CloudConfigFragmentSource struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (s *CloudConfigFragmentSource) String() string {
	if s == nil {
		return ""
	}
	return s.Name + " (" + s.ID + ")"
}

type CloudConfigLayerSourceType string

const (
	CloudConfigLayerEnterpriseManaged CloudConfigLayerSourceType = "enterpriseManaged"
)

type CloudConfigLayerSource struct {
	Type CloudConfigLayerSourceType `json:"type"`
	ID   string                     `json:"id,omitempty"`
	Name string                     `json:"name,omitempty"`
}

type CloudConfigLayer struct {
	Source  CloudConfigLayerSource `json:"source"`
	Values  map[string]any         `json:"values"`
	RawTOML string                 `json:"rawToml"`
	BaseDir string                 `json:"baseDir"`
}

type CloudConfigRequirementsLayer struct {
	Source  CloudConfigLayerSource `json:"source"`
	Values  map[string]string      `json:"values"`
	RawTOML string                 `json:"rawToml"`
	BaseDir string                 `json:"baseDir"`
}

type CloudConfigTOMLBundle struct {
	EnterpriseManaged []CloudConfigFragment `json:"enterprise_managed"`
}

type CloudConfigRequirementsTOMLBundle struct {
	EnterpriseManaged []CloudConfigFragment `json:"enterprise_managed"`
}

type CloudConfigBundle struct {
	ConfigTOML       CloudConfigTOMLBundle             `json:"config_toml"`
	RequirementsTOML CloudConfigRequirementsTOMLBundle `json:"requirements_toml"`
}

func (b *CloudConfigBundle) IsEmpty() bool {
	return b == nil ||
		(len(b.ConfigTOML.EnterpriseManaged) == 0 && len(b.RequirementsTOML.EnterpriseManaged) == 0)
}

type CloudConfigBundleLayers struct {
	EnterpriseManagedConfig       []CloudConfigLayer             `json:"enterpriseManagedConfig"`
	EnterpriseManagedRequirements []CloudConfigRequirementsLayer `json:"enterpriseManagedRequirements"`
}

func CloudConfigLayersFromFragments(fragments []CloudConfigFragment, baseDir string) ([]CloudConfigLayer, error) {
	layers := make([]CloudConfigLayer, 0, len(fragments))
	for _, fragment := range fragments {
		source := CloudConfigFragmentSource{ID: fragment.ID, Name: fragment.Name}
		values, err := ParseCloudConfigSimpleTOML(fragment.Contents)
		if err != nil {
			return nil, fmt.Errorf("%w: failed to parse cloud config fragment %s: %s", ErrInvalidCloudConfig, source.String(), err)
		}
		ResolveCloudConfigRelativePaths(values, baseDir)
		layers = append(layers, CloudConfigLayer{
			Source:  CloudConfigLayerSource{Type: CloudConfigLayerEnterpriseManaged, ID: fragment.ID, Name: fragment.Name},
			Values:  values,
			RawTOML: fragment.Contents,
			BaseDir: baseDir,
		})
	}
	reverseCloudConfigLayers(layers)
	return layers, nil
}

func CloudConfigLayersFromBundle(bundle CloudConfigBundle, baseDir string) (*CloudConfigBundleLayers, error) {
	configLayers, err := CloudConfigLayersFromFragments(bundle.ConfigTOML.EnterpriseManaged, baseDir)
	if err != nil {
		return nil, err
	}
	requirements := make([]CloudConfigRequirementsLayer, 0, len(bundle.RequirementsTOML.EnterpriseManaged))
	for _, fragment := range bundle.RequirementsTOML.EnterpriseManaged {
		values, err := ParseCloudConfigFlatTOML(fragment.Contents)
		if err != nil {
			source := CloudConfigFragmentSource{ID: fragment.ID, Name: fragment.Name}
			return nil, fmt.Errorf("%w: failed to parse cloud requirements fragment %s: %s", ErrInvalidCloudConfig, source.String(), err)
		}
		requirements = append(requirements, CloudConfigRequirementsLayer{
			Source:  CloudConfigLayerSource{Type: CloudConfigLayerEnterpriseManaged, ID: fragment.ID, Name: fragment.Name},
			Values:  values,
			RawTOML: fragment.Contents,
			BaseDir: baseDir,
		})
	}
	reverseCloudConfigRequirements(requirements)
	return &CloudConfigBundleLayers{
		EnterpriseManagedConfig:       configLayers,
		EnterpriseManagedRequirements: requirements,
	}, nil
}

type CloudConfigLoadErrorCode string

const (
	CloudConfigLoadAuth          CloudConfigLoadErrorCode = "auth"
	CloudConfigLoadTimeout       CloudConfigLoadErrorCode = "timeout"
	CloudConfigLoadRequestFailed CloudConfigLoadErrorCode = "request_failed"
	CloudConfigLoadInvalidBundle CloudConfigLoadErrorCode = "invalid_bundle"
	CloudConfigLoadInternal      CloudConfigLoadErrorCode = "internal"
)

type CloudConfigLoadError struct {
	Code       CloudConfigLoadErrorCode
	Message    string
	StatusCode *int
}

func NewCloudConfigLoadError(code CloudConfigLoadErrorCode, statusCode *int, message string) *CloudConfigLoadError {
	return &CloudConfigLoadError{Code: code, StatusCode: statusCode, Message: message}
}

func (e *CloudConfigLoadError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

type CloudConfigLoader struct {
	mu     sync.Mutex
	load   func() (*CloudConfigBundle, error)
	bundle *CloudConfigBundle
	err    error
}

func NewCloudConfigLoader(load func() (*CloudConfigBundle, error)) *CloudConfigLoader {
	if load == nil {
		load = func() (*CloudConfigBundle, error) { return nil, nil }
	}
	return &CloudConfigLoader{load: load}
}

func (l *CloudConfigLoader) Get() (*CloudConfigBundle, error) {
	if l == nil {
		return nil, nil
	}
	// Rust 070a26a1f0: retrieve the latest shared bundle on each
	// configuration load so later sessions observe refreshed bundles instead
	// of the startup snapshot. The last successful bundle is preserved when a
	// refresh fails.
	bundle, err := l.load()
	l.mu.Lock()
	defer l.mu.Unlock()
	if err == nil {
		l.bundle = bundle
		l.err = nil
		return bundle, nil
	}
	if l.bundle != nil && l.err == nil {
		return l.bundle, nil
	}
	l.err = err
	return nil, err
}

func ParseCloudConfigSimpleTOML(input string) (map[string]any, error) {
	root := map[string]any{}
	var section []string
	for _, line := range strings.Split(strings.ReplaceAll(input, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(cloudConfigStripComment(line))
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			section = cloudConfigSplitDottedPath(strings.TrimSpace(trimmed[1 : len(trimmed)-1]))
			continue
		}
		key, value, ok := strings.Cut(trimmed, "=")
		if !ok {
			return nil, fmt.Errorf("expected key = value")
		}
		path := append(append([]string{}, section...), cloudConfigSplitDottedPath(strings.TrimSpace(key))...)
		cloudConfigSetAtPath(root, path, cloudConfigParseValue(strings.TrimSpace(value)))
	}
	return root, nil
}

func ParseCloudConfigFlatTOML(input string) (map[string]string, error) {
	values := map[string]string{}
	parsed, err := ParseCloudConfigSimpleTOML(input)
	if err != nil {
		return nil, err
	}
	cloudConfigFlatten("", parsed, values)
	return values, nil
}

func ResolveCloudConfigRelativePaths(values map[string]any, baseDir string) {
	if strings.TrimSpace(baseDir) == "" {
		return
	}
	for key, value := range values {
		nested, ok := value.(map[string]any)
		if ok {
			ResolveCloudConfigRelativePaths(nested, baseDir)
			continue
		}
		if !cloudConfigLooksLikePathKey(key) {
			continue
		}
		if path, ok := value.(string); ok && path != "" && !filepath.IsAbs(path) {
			values[key] = filepath.Clean(filepath.Join(baseDir, path))
		}
	}
}

func MergeCloudConfigLayers(layers []CloudConfigLayer) map[string]any {
	out := map[string]any{}
	for _, layer := range layers {
		cloudConfigMergeMap(out, layer.Values)
	}
	return out
}

func applyCloudConfigBundle(values map[string]any, requirements *ConfigRequirements, bundle CloudConfigBundle, baseDir string) (*ConfigRequirements, error) {
	layers, err := CloudConfigLayersFromBundle(bundle, baseDir)
	if err != nil {
		return nil, err
	}
	mergeConfigMaps(values, MergeCloudConfigLayers(layers.EnterpriseManagedConfig))

	managedRequirementValues := map[string]any{}
	for _, layer := range layers.EnterpriseManagedRequirements {
		parsed, err := parseRequirementsTOMLValues([]byte(layer.RawTOML))
		if err != nil {
			return nil, fmt.Errorf("%w: failed to parse cloud requirements fragment %s: %s", ErrInvalidCloudConfig, layer.Source.Name, err)
		}
		mergeConfigMaps(managedRequirementValues, parsed)
	}
	managedRequirements, err := configRequirementsFromValidatedMap(managedRequirementValues)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid cloud requirements: %s", ErrInvalidCloudConfig, err)
	}
	// Rust 0f21cb3413 (#39043): cli_auth_credentials_store and chatgpt_base_url
	// are local-only authentication requirements and are ignored in
	// cloud-managed requirement layers.
	if managedRequirements != nil {
		managedRequirements.CliAuthCredentialsStore = nil
		managedRequirements.ChatgptBaseURL = nil
	}

	// Permission profiles in requirements are executable policy definitions,
	// while the remaining fields constrain which config values may be selected.
	if rawProfiles, ok := managedRequirementValues["permissions"].(map[string]any); ok {
		mergeConfigMaps(values, map[string]any{"permissions": rawProfiles})
	}
	if defaultProfile, ok := managedRequirementValues["default_permissions"].(string); ok && strings.TrimSpace(defaultProfile) != "" {
		values["default_permissions"] = strings.TrimSpace(defaultProfile)
	}
	return mergeConfigRequirements(requirements, managedRequirements), nil
}

func mergeConfigRequirements(base, overlay *ConfigRequirements) *ConfigRequirements {
	out := cloneRequirements(base)
	if overlay == nil {
		return out
	}
	if out == nil {
		return cloneRequirements(overlay)
	}
	if overlay.AllowedApprovalPolicies != nil {
		out.AllowedApprovalPolicies = cloneSlice(overlay.AllowedApprovalPolicies)
	}
	if overlay.AllowedApprovalsReviewers != nil {
		out.AllowedApprovalsReviewers = cloneSlice(overlay.AllowedApprovalsReviewers)
	}
	if overlay.AllowedSandboxModes != nil {
		out.AllowedSandboxModes = cloneSlice(overlay.AllowedSandboxModes)
	}
	if overlay.AllowedWindowsSandboxImplementations != nil {
		out.AllowedWindowsSandboxImplementations = cloneSlice(overlay.AllowedWindowsSandboxImplementations)
	}
	if overlay.AllowedPermissionProfiles != nil {
		out.AllowedPermissionProfiles = cloneBoolMap(overlay.AllowedPermissionProfiles)
	}
	if overlay.DefaultPermissions != nil {
		out.DefaultPermissions = cloneStringPtr(overlay.DefaultPermissions)
	}
	if overlay.AllowedWebSearchModes != nil {
		out.AllowedWebSearchModes = cloneSlice(overlay.AllowedWebSearchModes)
	}
	if overlay.AllowManagedHooksOnly != nil {
		out.AllowManagedHooksOnly = cloneBoolPtr(overlay.AllowManagedHooksOnly)
	}
	if overlay.AllowBrowserAndComputerUse != nil {
		out.AllowBrowserAndComputerUse = cloneBoolPtr(overlay.AllowBrowserAndComputerUse)
	}
	if overlay.AllowAppshots != nil {
		out.AllowAppshots = cloneBoolPtr(overlay.AllowAppshots)
	}
	if overlay.AllowRemoteControl != nil {
		out.AllowRemoteControl = cloneBoolPtr(overlay.AllowRemoteControl)
	}
	if overlay.ComputerUse != nil {
		out.ComputerUse = cloneComputerUse(overlay.ComputerUse)
	}
	if overlay.BrowserUse != nil {
		out.BrowserUse = cloneBrowserUse(overlay.BrowserUse)
	}
	if overlay.InAppBrowser != nil {
		out.InAppBrowser = cloneInAppBrowser(overlay.InAppBrowser)
	}
	if overlay.AutoReview != nil {
		// Rust 208f05b233: `auto_review.required_on_models` unions model slugs
		// across requirement layers so protected models stay protected even
		// when a lower layer omits the setting. `ignore_rules` follows the
		// first-wins layer semantics for the app-server protocol exposure.
		if out.AutoReview == nil {
			out.AutoReview = cloneAutoReview(overlay.AutoReview)
		} else {
			out.AutoReview.RequiredOnModels = stringUnion(
				out.AutoReview.RequiredOnModels,
				overlay.AutoReview.RequiredOnModels,
			)
			if len(out.AutoReview.IgnoreRules) == 0 {
				out.AutoReview.IgnoreRules = append([]string(nil), overlay.AutoReview.IgnoreRules...)
			}
		}
	}
	if overlay.FeatureRequirements != nil {
		out.FeatureRequirements = cloneBoolMap(overlay.FeatureRequirements)
	}
	if overlay.Hooks != nil {
		out.Hooks = cloneManagedHooks(overlay.Hooks)
	}
	if overlay.EnforceResidency != nil {
		out.EnforceResidency = cloneResidencyRequirementPtr(overlay.EnforceResidency)
	}
	if overlay.Network != nil {
		out.Network = cloneNetwork(overlay.Network)
	}
	if overlay.Models != nil {
		out.Models = cloneModels(overlay.Models)
	}
	if overlay.MCPServers != nil {
		out.MCPServers = cloneMCPServerRequirements(overlay.MCPServers)
	}
	if overlay.Plugins != nil {
		out.Plugins = clonePluginRequirements(overlay.Plugins)
	}
	return out
}

func stringUnion(values ...[]string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, slice := range values {
		for _, value := range slice {
			value = strings.TrimSpace(value)
			if value == "" || seen[value] {
				continue
			}
			seen[value] = true
			out = append(out, value)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cloudConfigStripComment(line string) string {
	inQuote := byte(0)
	for i := 0; i < len(line); i++ {
		ch := line[i]
		if inQuote != 0 {
			if ch == inQuote {
				inQuote = 0
			}
			continue
		}
		if ch == '"' || ch == '\'' {
			inQuote = ch
			continue
		}
		if ch == '#' {
			return line[:i]
		}
	}
	return line
}

func cloudConfigSplitDottedPath(path string) []string {
	parts := strings.Split(path, ".")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.Trim(strings.TrimSpace(part), `"'`)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func cloudConfigParseValue(raw string) any {
	raw = strings.TrimSpace(raw)
	if len(raw) >= 2 {
		if (raw[0] == '"' && raw[len(raw)-1] == '"') || (raw[0] == '\'' && raw[len(raw)-1] == '\'') {
			return raw[1 : len(raw)-1]
		}
	}
	switch raw {
	case "true":
		return true
	case "false":
		return false
	}
	if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
		content := strings.TrimSpace(raw[1 : len(raw)-1])
		if content == "" {
			return []any{}
		}
		parts := cloudConfigSplitCSV(content)
		values := make([]any, 0, len(parts))
		for _, part := range parts {
			values = append(values, cloudConfigParseValue(part))
		}
		return values
	}
	return raw
}

func cloudConfigSplitCSV(input string) []string {
	var parts []string
	var current strings.Builder
	inQuote := byte(0)
	for i := 0; i < len(input); i++ {
		ch := input[i]
		if inQuote != 0 {
			current.WriteByte(ch)
			if ch == inQuote {
				inQuote = 0
			}
			continue
		}
		if ch == '"' || ch == '\'' {
			inQuote = ch
			current.WriteByte(ch)
			continue
		}
		if ch == ',' {
			parts = append(parts, strings.TrimSpace(current.String()))
			current.Reset()
			continue
		}
		current.WriteByte(ch)
	}
	parts = append(parts, strings.TrimSpace(current.String()))
	return parts
}

func cloudConfigSetAtPath(root map[string]any, parts []string, value any) {
	if len(parts) == 0 {
		return
	}
	current := root
	for _, part := range parts[:len(parts)-1] {
		next, ok := current[part].(map[string]any)
		if !ok {
			next = map[string]any{}
			current[part] = next
		}
		current = next
	}
	current[parts[len(parts)-1]] = value
}

func cloudConfigFlatten(prefix string, values map[string]any, out map[string]string) {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		if nested, ok := values[key].(map[string]any); ok {
			cloudConfigFlatten(path, nested, out)
			continue
		}
		out[path] = fmt.Sprint(values[key])
	}
}

func cloudConfigLooksLikePathKey(key string) bool {
	key = strings.ToLower(key)
	return strings.HasSuffix(key, "path") || strings.HasSuffix(key, "dir") || strings.HasSuffix(key, "file")
}

func cloudConfigMergeMap(target map[string]any, source map[string]any) {
	for key, value := range source {
		sourceNested, ok := value.(map[string]any)
		if ok {
			targetNested, _ := target[key].(map[string]any)
			if targetNested == nil {
				targetNested = map[string]any{}
				target[key] = targetNested
			}
			cloudConfigMergeMap(targetNested, sourceNested)
			continue
		}
		target[key] = value
	}
}

func reverseCloudConfigLayers(values []CloudConfigLayer) {
	for i, j := 0, len(values)-1; i < j; i, j = i+1, j-1 {
		values[i], values[j] = values[j], values[i]
	}
}

func reverseCloudConfigRequirements(values []CloudConfigRequirementsLayer) {
	for i, j := 0, len(values)-1; i < j; i, j = i+1, j-1 {
		values[i], values[j] = values[j], values[i]
	}
}
