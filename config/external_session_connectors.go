package config

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const externalSessionManifestsDir = "claude-code-sessions"

type externalSessionConnectorAttribution struct {
	SessionID string
	ServerIDs map[string]bool
	Roots     []string
}

type externalSessionManifest struct {
	CLISessionID           string                   `json:"cliSessionId"`
	RemoteMCPServersConfig []externalManifestServer `json:"remoteMcpServersConfig"`
}

type externalManifestServer struct {
	Name *string `json:"name"`
	UUID *string `json:"uuid"`
}

// DetectExternalSessionConnectors reports connectors attributed to the supplied
// sessions using the selected external agent's metadata format.
func (s *ConfigService) DetectExternalSessionConnectors(migrationSource *string, sessions []SessionMigration) []ExternalAgentDetectedConnectorCandidate {
	source := normalizeExternalMigrationSource(migrationSource)
	home := s.externalAgentHomeForSource(source)
	if source == externalMigrationSourceCursor {
		return detectExternalCursorSessionConnectorCandidates(sessions, home)
	}
	return detectExternalClaudeSessionConnectorCandidates(sessions, externalConnectorMetadataRootsForHome(home))
}

func detectExternalClaudeSessionConnectorCandidates(sessions []SessionMigration, roots []string) []ExternalAgentDetectedConnectorCandidate {
	attributions := make([]externalSessionConnectorAttribution, 0, len(sessions))
	for _, session := range sessions {
		attribution := readExternalSessionConnectorAttribution(session.Path)
		attribution.Roots = roots
		if attribution.SessionID != "" && len(attribution.ServerIDs) != 0 {
			attributions = append(attributions, attribution)
		}
	}
	namesBySession := detectExternalSessionConnectors(attributions)
	byName := map[string]ExternalAgentDetectedConnectorCandidate{}
	sessionIDs := make([]string, 0, len(namesBySession))
	for sessionID := range namesBySession {
		sessionIDs = append(sessionIDs, sessionID)
	}
	sort.Strings(sessionIDs)
	for _, sessionID := range sessionIDs {
		names := namesBySession[sessionID]
		for _, name := range names {
			key := strings.ToLower(name)
			candidate, exists := byName[key]
			if !exists {
				candidate = ExternalAgentDetectedConnectorCandidate{
					Name:   name,
					Source: ExternalAgentConnectorRemoteMCPServersConfig,
				}
			}
			if candidate.SessionCount != ^uint32(0) {
				candidate.SessionCount++
			}
			byName[key] = candidate
		}
	}
	return sortedExternalConnectorCandidates(byName)
}

type externalCursorPluginManifest struct {
	Name        string          `json:"name"`
	DisplayName *string         `json:"displayName"`
	MCPServers  json.RawMessage `json:"mcpServers"`
}

func detectExternalCursorSessionConnectorCandidates(sessions []SessionMigration, externalAgentHome string) []ExternalAgentDetectedConnectorCandidate {
	namesByServerID := cachedExternalCursorConnectorNames(externalAgentHome)
	if len(namesByServerID) == 0 {
		return []ExternalAgentDetectedConnectorCandidate{}
	}
	byName := map[string]ExternalAgentDetectedConnectorCandidate{}
	for _, session := range sessions {
		for key, name := range externalCursorSessionConnectorNames(session.Path, namesByServerID) {
			candidate, exists := byName[key]
			if !exists {
				candidate = ExternalAgentDetectedConnectorCandidate{
					Name:   name,
					Source: ExternalAgentConnectorSessionToolUse,
				}
			}
			if candidate.SessionCount != ^uint32(0) {
				candidate.SessionCount++
			}
			byName[key] = candidate
		}
	}
	return sortedExternalConnectorCandidates(byName)
}

func sortedExternalConnectorCandidates(byName map[string]ExternalAgentDetectedConnectorCandidate) []ExternalAgentDetectedConnectorCandidate {
	keys := make([]string, 0, len(byName))
	for key := range byName {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]ExternalAgentDetectedConnectorCandidate, 0, len(keys))
	for _, key := range keys {
		result = append(result, byName[key])
	}
	return result
}

func cachedExternalCursorConnectorNames(externalAgentHome string) map[string]string {
	result := map[string]string{}
	cacheRoot := filepath.Join(strings.TrimSpace(externalAgentHome), "plugins", "cache")
	for _, marketplaceRoot := range externalChildDirectories(cacheRoot) {
		for _, pluginRoot := range externalChildDirectories(marketplaceRoot) {
			for _, versionRoot := range externalChildDirectories(pluginRoot) {
				manifestPath := filepath.Join(versionRoot, ".cursor-plugin", "plugin.json")
				data, err := os.ReadFile(manifestPath)
				if err != nil {
					continue
				}
				var manifest externalCursorPluginManifest
				if json.Unmarshal(data, &manifest) != nil {
					continue
				}
				displayName := strings.TrimSpace(manifest.Name)
				if manifest.DisplayName != nil {
					displayName = strings.TrimSpace(*manifest.DisplayName)
				}
				if displayName == "" {
					continue
				}
				for _, serverName := range externalCursorManifestMCPServerNames(versionRoot, manifest.MCPServers) {
					serverID := strings.ToLower("plugin-" + manifest.Name + "-" + serverName)
					result[serverID] = displayName
				}
			}
		}
	}
	return result
}

func externalChildDirectories(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	result := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			result = append(result, filepath.Join(root, entry.Name()))
		}
	}
	return result
}

func externalCursorManifestMCPServerNames(versionRoot string, declaration json.RawMessage) []string {
	if len(declaration) == 0 || strings.TrimSpace(string(declaration)) == "null" {
		return externalCursorMCPServerNamesFromFile(versionRoot, ".mcp.json")
	}
	var value any
	if json.Unmarshal(declaration, &value) != nil {
		return nil
	}
	names := map[string]bool{}
	externalCursorCollectMCPServerNames(versionRoot, value, names)
	return sortedExternalKeys(names)
}

func externalCursorCollectMCPServerNames(versionRoot string, declaration any, names map[string]bool) {
	switch value := declaration.(type) {
	case string:
		for _, name := range externalCursorMCPServerNamesFromFile(versionRoot, value) {
			names[name] = true
		}
	case map[string]any:
		for _, name := range externalCursorMCPServerNamesFromConfig(value) {
			names[name] = true
		}
	case []any:
		for _, nested := range value {
			externalCursorCollectMCPServerNames(versionRoot, nested, names)
		}
	}
}

func externalCursorMCPServerNamesFromFile(versionRoot string, relativePath string) []string {
	if !safeExternalCursorRelativePath(relativePath) {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(versionRoot, filepath.FromSlash(relativePath)))
	if err != nil {
		return nil
	}
	var config map[string]any
	if json.Unmarshal(data, &config) != nil {
		return nil
	}
	return externalCursorMCPServerNamesFromConfig(config)
}

func safeExternalCursorRelativePath(path string) bool {
	if path == "" || filepath.IsAbs(path) || filepath.VolumeName(path) != "" || strings.HasPrefix(path, "/") || strings.HasPrefix(path, `\`) {
		return false
	}
	for _, component := range strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' }) {
		if component == ".." {
			return false
		}
	}
	return true
}

func externalCursorMCPServerNamesFromConfig(config map[string]any) []string {
	servers := config
	if nested, ok := config["mcpServers"].(map[string]any); ok {
		servers = nested
	}
	keys := make([]string, 0, len(servers))
	for name := range servers {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	return keys
}

func sortedExternalKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func externalCursorSessionConnectorNames(path string, namesByServerID map[string]string) map[string]string {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	result := map[string]string{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		var record map[string]any
		if json.Unmarshal(scanner.Bytes(), &record) != nil {
			continue
		}
		message, _ := record["message"].(map[string]any)
		contents, _ := message["content"].([]any)
		for _, rawContent := range contents {
			content, _ := rawContent.(map[string]any)
			contentType, _ := content["type"].(string)
			name, _ := content["name"].(string)
			if contentType != "tool_use" || name != "CallMcpTool" {
				continue
			}
			input, _ := content["input"].(map[string]any)
			server, _ := input["server"].(string)
			serverID := strings.ToLower(strings.TrimSpace(server))
			connectorName := namesByServerID[serverID]
			if connectorName != "" {
				result[strings.ToLower(connectorName)] = connectorName
			}
		}
	}
	return result
}

func externalSessionConnectorNames(imports []ExternalSessionImportCompletion) map[string][]string {
	attributions := make([]externalSessionConnectorAttribution, 0, len(imports))
	for _, completed := range imports {
		if len(completed.ConnectorNames) != 0 {
			continue
		}
		attribution := readExternalSessionConnectorAttribution(completed.SourcePath)
		if attribution.SessionID != "" && len(attribution.ServerIDs) != 0 && len(attribution.Roots) != 0 {
			attributions = append(attributions, attribution)
		}
	}
	return detectExternalSessionConnectors(attributions)
}

func readExternalSessionConnectorAttribution(path string) externalSessionConnectorAttribution {
	attribution := externalSessionConnectorAttribution{
		SessionID: strings.TrimSpace(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))),
		ServerIDs: map[string]bool{},
		Roots:     externalConnectorMetadataRoots(path),
	}
	file, err := os.Open(path)
	if err != nil {
		return attribution
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		var row struct {
			AttributionMCPServer string `json:"attributionMcpServer"`
		}
		if json.Unmarshal(scanner.Bytes(), &row) == nil {
			if serverID := strings.TrimSpace(row.AttributionMCPServer); serverID != "" {
				attribution.ServerIDs[serverID] = true
			}
		}
	}
	return attribution
}

func detectExternalSessionConnectors(attributions []externalSessionConnectorAttribution) map[string][]string {
	bySession := map[string]map[string]bool{}
	roots := map[string]bool{}
	for _, attribution := range attributions {
		if attribution.SessionID == "" || len(attribution.ServerIDs) == 0 {
			continue
		}
		ids := bySession[attribution.SessionID]
		if ids == nil {
			ids = map[string]bool{}
			bySession[attribution.SessionID] = ids
		}
		for serverID := range attribution.ServerIDs {
			ids[serverID] = true
		}
		for _, root := range attribution.Roots {
			roots[root] = true
		}
	}
	namesBySession := map[string]map[string]string{}
	for root := range roots {
		_ = filepath.WalkDir(filepath.Join(root, externalSessionManifestsDir), func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry == nil || entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
				return nil
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return nil
			}
			var manifest externalSessionManifest
			if json.Unmarshal(data, &manifest) != nil {
				return nil
			}
			serverIDs := bySession[manifest.CLISessionID]
			if len(serverIDs) == 0 {
				return nil
			}
			for _, server := range manifest.RemoteMCPServersConfig {
				name := normalizedExternalConnectorName(server.Name)
				if name == "" || !externalManifestServerAttributed(server, name, serverIDs) {
					continue
				}
				names := namesBySession[manifest.CLISessionID]
				if names == nil {
					names = map[string]string{}
					namesBySession[manifest.CLISessionID] = names
				}
				key := strings.ToLower(name)
				if _, exists := names[key]; !exists {
					names[key] = name
				}
			}
			return nil
		})
	}
	result := map[string][]string{}
	for sessionID, names := range namesBySession {
		keys := make([]string, 0, len(names))
		for key := range names {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			result[sessionID] = append(result[sessionID], names[key])
		}
	}
	return result
}

func externalManifestServerAttributed(server externalManifestServer, normalizedName string, serverIDs map[string]bool) bool {
	if server.UUID != nil && serverIDs[strings.TrimSpace(*server.UUID)] {
		return true
	}
	for serverID := range serverIDs {
		if strings.EqualFold(serverID, normalizedName) {
			return true
		}
	}
	return false
}

func normalizedExternalConnectorName(name *string) string {
	if name == nil {
		return ""
	}
	return strings.TrimSpace(*name)
}

func externalConnectorMetadataRoots(sourcePath string) []string {
	path := filepath.Clean(sourcePath)
	var externalHome string
	for directory := filepath.Dir(path); directory != filepath.Dir(directory); directory = filepath.Dir(directory) {
		if strings.EqualFold(filepath.Base(directory), "projects") {
			externalHome = filepath.Dir(directory)
			break
		}
	}
	return externalConnectorMetadataRootsForHome(externalHome)
}

func externalConnectorMetadataRootsForHome(externalHome string) []string {
	userHome := filepath.Dir(strings.TrimSpace(externalHome))
	if strings.TrimSpace(externalHome) == "" || userHome == "" || userHome == "." {
		return nil
	}
	var roots []string
	switch runtime.GOOS {
	case "darwin":
		roots = []string{filepath.Join(userHome, "Library", "Application Support", "Claude")}
	case "windows":
		defaultRoaming := filepath.Join(userHome, "AppData", "Roaming")
		defaultLocal := filepath.Join(userHome, "AppData", "Local")
		roaming := absoluteEnvPathOr("APPDATA", defaultRoaming)
		local := absoluteEnvPathOr("LOCALAPPDATA", defaultLocal)
		roots = []string{
			filepath.Join(local, "Packages", "Claude_pzs8sxrjxfjjc", "LocalCache", "Roaming", "Claude"),
			filepath.Join(roaming, "Claude"),
			filepath.Join(defaultLocal, "Packages", "Claude_pzs8sxrjxfjjc", "LocalCache", "Roaming", "Claude"),
			filepath.Join(defaultRoaming, "Claude"),
		}
	default:
		roots = []string{filepath.Join(userHome, ".config", "Claude")}
	}
	sort.Strings(roots)
	return compactExternalPaths(roots)
}

func absoluteEnvPathOr(name string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return fallback
}

func compactExternalPaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if len(out) == 0 || out[len(out)-1] != path {
			out = append(out, path)
		}
	}
	return out
}
