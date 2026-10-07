package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	AgentPluginManifestRelativePath = "plugin.json"
	AgentPluginSchemaURI            = "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json"
	agentPluginSchemaPrefix         = "https://agent-plugins.org/schemas/"
)

var legacyPluginManifestRelativePaths = []string{
	filepath.Join(".codex-plugin", "plugin.json"),
	filepath.Join(".claude-plugin", "plugin.json"),
	filepath.Join(".cursor-plugin", "plugin.json"),
}

type pluginManifestKind uint8

const (
	pluginManifestLegacy pluginManifestKind = iota
	pluginManifestAgent
)

type resolvedPluginManifest struct {
	Path     string
	Kind     pluginManifestKind
	Manifest pluginManifestFile
}

type rawAgentPluginManifest struct {
	Schema      string                     `json:"$schema"`
	Name        string                     `json:"name"`
	Version     *string                    `json:"version"`
	Description *string                    `json:"description"`
	Author      *rawAgentPluginAuthor      `json:"author"`
	Homepage    *string                    `json:"homepage"`
	Keywords    []string                   `json:"keywords"`
	Extensions  map[string]json.RawMessage `json:"extensions"`
}

type rawAgentPluginAuthor struct {
	Name *string `json:"name"`
}

func findPluginManifestPath(pluginRoot string) (string, error) {
	pluginRoot = strings.TrimSpace(pluginRoot)
	if pluginRoot == "" {
		return "", nil
	}
	rootPath := filepath.Join(pluginRoot, AgentPluginManifestRelativePath)
	if data, err := os.ReadFile(rootPath); err == nil {
		status, statusErr := agentPluginSchemaStatus(data)
		if statusErr != nil {
			return "", statusErr
		}
		switch status {
		case AgentPluginSchemaSupported:
			return rootPath, nil
		case AgentPluginSchemaUnsupported:
			var header struct {
				Schema string `json:"$schema"`
			}
			_ = json.Unmarshal(data, &header)
			return "", fmt.Errorf("unsupported Agent Plugins schema %q; supported schemas: %q", header.Schema, AgentPluginSchemaURI)
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	for _, relativePath := range legacyPluginManifestRelativePaths {
		path := filepath.Join(pluginRoot, relativePath)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
	}
	return "", nil
}

type AgentPluginSchemaStatus uint8

const (
	AgentPluginSchemaUnrelated AgentPluginSchemaStatus = iota
	AgentPluginSchemaSupported
	AgentPluginSchemaUnsupported
)

func AgentPluginSchemaStatusForContents(data []byte) (AgentPluginSchemaStatus, error) {
	var value map[string]json.RawMessage
	if err := json.Unmarshal(data, &value); err != nil {
		return AgentPluginSchemaUnrelated, nil
	}
	rawSchema, ok := value["$schema"]
	if !ok {
		return AgentPluginSchemaUnrelated, nil
	}
	var schema string
	if err := json.Unmarshal(rawSchema, &schema); err != nil {
		return AgentPluginSchemaUnrelated, nil
	}
	if schema == AgentPluginSchemaURI {
		return AgentPluginSchemaSupported, nil
	}
	if strings.HasPrefix(schema, agentPluginSchemaPrefix) {
		return AgentPluginSchemaUnsupported, nil
	}
	return AgentPluginSchemaUnrelated, nil
}

func agentPluginSchemaStatus(data []byte) (AgentPluginSchemaStatus, error) {
	return AgentPluginSchemaStatusForContents(data)
}

// loadPluginManifest parses a plugin manifest without a manifest cache. Callers
// that own a plugin store go through PluginStore.LoadPluginManifest so repeated
// discovery reuses an unchanged revision (Rust #49099).
func loadPluginManifest(pluginRoot string) (*resolvedPluginManifest, error) {
	return parsePluginManifestAtRoot(pluginRoot, disabledManifestCache())
}

// parsePluginManifestAtRoot rereads the manifest (and, for an agent plugin, the
// Codex overlay) on every call, so manifest edits and overlay changes or removal
// stay visible, and routes the parse through cache when one is provided.
func parsePluginManifestAtRoot(pluginRoot string, cache *manifestCache) (*resolvedPluginManifest, error) {
	path, err := findPluginManifestPath(pluginRoot)
	if err != nil || path == "" {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	isAgentPlugin := filepath.Clean(path) == filepath.Clean(filepath.Join(pluginRoot, AgentPluginManifestRelativePath))
	overlayPath := ""
	var overlay []byte
	if isAgentPlugin {
		// Rust manifest_cache.rs reads the overlay for every agent plugin so the
		// revision digest covers it; the parser keeps the com.openai extension's
		// precedence over the overlay.
		overlayPath = filepath.Join(pluginRoot, ".codex-plugin", "plugin.json")
		if raw, readErr := os.ReadFile(overlayPath); readErr == nil {
			overlay = raw
		}
	}
	parse := func() (*resolvedPluginManifest, error) {
		return parseResolvedPluginManifest(pluginRoot, path, data, overlayPath, overlay, isAgentPlugin)
	}
	return cache.parse(pluginRoot, path, data, overlayPath, overlay, parse)
}

func parseResolvedPluginManifest(pluginRoot string, path string, data []byte, overlayPath string, overlay []byte, isAgentPlugin bool) (*resolvedPluginManifest, error) {
	if isAgentPlugin {
		manifest, err := parseAgentPluginManifest(pluginRoot, data, overlayPath, overlay)
		if err != nil {
			return nil, err
		}
		return &resolvedPluginManifest{Path: path, Kind: pluginManifestAgent, Manifest: manifest}, nil
	}
	var manifest pluginManifestFile
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	manifest.ManifestPath = path
	manifest.SkillsPath = filepath.Join(pluginRoot, "skills")
	manifest.MCPServersPath = filepath.Join(pluginRoot, ".mcp.json")
	return &resolvedPluginManifest{Path: path, Kind: pluginManifestLegacy, Manifest: manifest}, nil
}

// parseAgentPluginManifest parses an Agent Plugins manifest. overlayPath and
// overlay carry the plugin directory's `.codex-plugin/plugin.json`, which the
// caller reads so the manifest cache can digest it (Rust #49099).
func parseAgentPluginManifest(pluginRoot string, data []byte, overlayPath string, overlay []byte) (pluginManifestFile, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return pluginManifestFile{}, err
	}
	for _, field := range []string{"version", "description", "author", "homepage", "repository", "license"} {
		if raw, ok := object[field]; ok && string(raw) == "null" {
			return pluginManifestFile{}, fmt.Errorf("Agent Plugins %q must use its declared type when present", field)
		}
	}
	if raw, ok := object["author"]; ok {
		var author map[string]json.RawMessage
		if err := json.Unmarshal(raw, &author); err != nil {
			return pluginManifestFile{}, err
		}
		for _, field := range []string{"name", "email", "url"} {
			if value, exists := author[field]; exists && string(value) == "null" {
				return pluginManifestFile{}, fmt.Errorf("Agent Plugins author.%s must be a string when present", field)
			}
		}
	}
	var raw rawAgentPluginManifest
	if err := json.Unmarshal(data, &raw); err != nil {
		return pluginManifestFile{}, err
	}
	if raw.Schema != AgentPluginSchemaURI {
		return pluginManifestFile{}, fmt.Errorf("unsupported Agent Plugins schema %q; supported schemas: %q", raw.Schema, AgentPluginSchemaURI)
	}
	if !validAgentPluginName(raw.Name) {
		return pluginManifestFile{}, fmt.Errorf("invalid Agent Plugins name %q; use lowercase letters, numbers, dots, or hyphens", raw.Name)
	}
	description := trimmedStringPtrValue(raw.Description)
	manifest := pluginManifestFile{
		Name:           raw.Name,
		Version:        trimmedStringPtrValue(raw.Version),
		Description:    description,
		Keywords:       append([]string(nil), raw.Keywords...),
		ManifestPath:   filepath.Join(pluginRoot, AgentPluginManifestRelativePath),
		SkillsPath:     filepath.Join(pluginRoot, "skills"),
		MCPServersPath: filepath.Join(pluginRoot, "mcp.json"),
		AgentPlugin:    true,
		Interface: &PluginInterface{
			DisplayName:      stringPtrIfNotEmpty(raw.Name),
			ShortDescription: stringPtrIfNotEmpty(description),
			LongDescription:  stringPtrIfNotEmpty(description),
			DeveloperName:    stringPtrIfNotEmpty(trimmedAgentAuthorName(raw.Author)),
			Category:         "Other",
			WebsiteURL:       stringPtrIfNotEmpty(trimmedStringPtrValue(raw.Homepage)),
		},
	}
	var extension *pluginManifestFile
	if value, ok := raw.Extensions["com.openai"]; ok && len(value) > 0 && string(value) != "null" {
		var parsed pluginManifestFile
		if err := json.Unmarshal(value, &parsed); err == nil {
			extension = &parsed
		}
	}
	if extension == nil && len(overlay) > 0 {
		var parsed pluginManifestFile
		if err := json.Unmarshal(overlay, &parsed); err != nil {
			return pluginManifestFile{}, err
		}
		extension = &parsed
	}
	if extension != nil {
		if extension.Interface != nil {
			manifest.Interface = extension.Interface
		}
		manifest.Apps = cloneAppSummaries(extension.Apps)
		manifest.AppTemplates = cloneAppTemplateSummaries(firstPluginManifestAppTemplates(extension))
		manifest.OnboardingSkill = firstNonEmpty(extension.OnboardingSkill, manifest.OnboardingSkill)
	}
	return manifest, nil
}

func validAgentPluginName(name string) bool {
	if name == "" || len(name) > 64 || strings.Contains(name, "--") || strings.Contains(name, "..") {
		return false
	}
	for i := 0; i < len(name); i++ {
		value := name[i]
		if !((value >= 'a' && value <= 'z') || (value >= '0' && value <= '9') || value == '.' || value == '-') {
			return false
		}
	}
	first, last := name[0], name[len(name)-1]
	return isASCIIAlphaNumeric(first) && isASCIIAlphaNumeric(last)
}

func isASCIIAlphaNumeric(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
}

func trimmedStringPtrValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func trimmedAgentAuthorName(author *rawAgentPluginAuthor) string {
	if author == nil {
		return ""
	}
	return trimmedStringPtrValue(author.Name)
}
