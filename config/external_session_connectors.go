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
	userHome := filepath.Dir(externalHome)
	if externalHome == "" || userHome == "" || userHome == "." {
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
