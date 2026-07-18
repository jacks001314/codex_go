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
	once   sync.Once
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
	l.once.Do(func() {
		l.bundle, l.err = l.load()
	})
	return l.bundle, l.err
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
