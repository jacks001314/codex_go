package appserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	pathpkg "path"
	"sort"
	"strings"

	"github.com/coder/websocket"
	"gopkg.in/yaml.v3"
)

const (
	remoteSkillWalkMaxDepth       = skillMaxScanDepth
	remoteSkillWalkMaxDirectories = skillMaxDirsPerRoot
	remoteSkillWalkMaxEntries     = 20000
)

var remoteDiscoverablePluginManifestPaths = []string{
	".codex-plugin/plugin.json",
	".claude-plugin/plugin.json",
}

type remoteFSWalkEntry struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

type remoteFSWalkError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

type remoteFSWalkResponse struct {
	Entries   []remoteFSWalkEntry `json:"entries"`
	Errors    []remoteFSWalkError `json:"errors"`
	Truncated bool                `json:"truncated"`
}

type remoteFSReadFileResponse struct {
	DataBase64 string `json:"dataBase64"`
}

type remoteFSGetMetadataResponse struct {
	IsFile bool `json:"isFile"`
}

func discoverRemoteEnvironmentSkills(ctx context.Context, record *EnvironmentRecord, rootPath string) ([]SkillsListEntry, []string, error) {
	if record == nil {
		return nil, nil, nil
	}
	rootPath = strings.TrimSpace(rootPath)
	if rootPath == "" {
		return nil, nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, environmentConnectTimeout(record.ConnectTimeoutMS))
	defer cancel()
	conn, _, err := websocket.Dial(ctx, record.ExecServerURL, nil)
	if err != nil {
		return nil, nil, err
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	if err := writeExecServerJSON(ctx, conn, &execServerJSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: map[string]any{
			"clientName": "codex-go",
		},
	}); err != nil {
		return nil, nil, err
	}
	if _, err := readExecServerResponse(ctx, conn, 1); err != nil {
		return nil, nil, err
	}
	if err := writeExecServerJSON(ctx, conn, &execServerJSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "initialized",
	}); err != nil {
		return nil, nil, err
	}
	walkRaw, err := callRemoteEnvironmentFS(ctx, conn, 2, "fs/walk", map[string]any{
		"path": rootPath,
		"options": map[string]any{
			"maxDepth":                remoteSkillWalkMaxDepth,
			"maxDirectories":          remoteSkillWalkMaxDirectories,
			"maxEntries":              remoteSkillWalkMaxEntries,
			"followDirectorySymlinks": true,
		},
	})
	if err != nil {
		return nil, nil, err
	}
	var walk remoteFSWalkResponse
	if err := json.Unmarshal(walkRaw, &walk); err != nil {
		return nil, nil, err
	}
	warnings := remoteEnvironmentSkillWalkWarnings(rootPath, walk)
	sort.SliceStable(walk.Entries, func(i int, j int) bool {
		return walk.Entries[i].Path < walk.Entries[j].Path
	})
	remoteFiles := map[string]bool{}
	for _, entry := range walk.Entries {
		if entry.Kind == "file" {
			remoteFiles[remoteNormalizePathKey(entry.Path)] = true
		}
	}
	nextID := 3
	pluginNamespaces := remotePluginNamespacesFromInventory(ctx, conn, &nextID, remoteFiles)
	pluginNamespaces = remotePluginNamespacesFromRootAncestors(ctx, conn, &nextID, rootPath, pluginNamespaces)
	entries := make([]SkillsListEntry, 0)
	for _, entry := range walk.Entries {
		if entry.Kind != "file" || remotePathBase(entry.Path) != SkillFilename {
			continue
		}
		contents, err := readRemoteEnvironmentText(ctx, conn, &nextID, entry.Path)
		if err != nil {
			return nil, warnings, err
		}
		metadataPath := remoteSkillMetadataPath(entry.Path)
		metadataContents := ""
		if remoteFiles[remoteNormalizePathKey(metadataPath)] {
			metadataContents, _ = readRemoteEnvironmentText(ctx, conn, &nextID, metadataPath)
		}
		pluginNamespace := remotePluginNamespaceForSkill(entry.Path, pluginNamespaces)
		if skillEntry, warning, ok := remoteSkillEntryFromContents(record.EnvironmentID, entry.Path, contents, metadataContents, pluginNamespace); ok {
			if !skillMatchesCodexProductRestriction(&skillEntry) {
				continue
			}
			entries = append(entries, skillEntry)
		} else if strings.TrimSpace(warning) != "" {
			warnings = append(warnings, warning)
		}
	}
	sort.SliceStable(entries, func(i int, j int) bool {
		if entries[i].Name != entries[j].Name {
			return entries[i].Name < entries[j].Name
		}
		return entries[i].Path < entries[j].Path
	})
	return entries, warnings, nil
}

func remoteEnvironmentSkillWalkWarnings(rootPath string, walk remoteFSWalkResponse) []string {
	warnings := make([]string, 0, len(walk.Errors)+1)
	for _, walkErr := range walk.Errors {
		path := strings.TrimSpace(walkErr.Path)
		if path == "" {
			path = rootPath
		}
		warnings = append(warnings, fmt.Sprintf("failed to scan skill path %s: %s", path, walkErr.Message))
	}
	if walk.Truncated {
		warnings = append(warnings, fmt.Sprintf("skills scan reached its traversal limit (root: %s)", rootPath))
	}
	return warnings
}

type remotePluginManifestCandidate struct {
	path     string
	priority int
}

type remotePluginManifestName struct {
	Name string `json:"name"`
}

func remotePluginNamespacesFromInventory(ctx context.Context, conn *websocket.Conn, nextID *int, remoteFiles map[string]bool) map[string]string {
	if len(remoteFiles) == 0 {
		return nil
	}
	candidates := map[string]remotePluginManifestCandidate{}
	for path := range remoteFiles {
		root, priority, ok := remotePluginRootFromManifestPath(path)
		if !ok {
			continue
		}
		key := remoteNormalizePathKey(root)
		existing, exists := candidates[key]
		if !exists || priority < existing.priority {
			candidates[key] = remotePluginManifestCandidate{path: path, priority: priority}
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	roots := make([]string, 0, len(candidates))
	for root := range candidates {
		roots = append(roots, root)
	}
	sort.Strings(roots)
	namespaces := make(map[string]string, len(roots))
	for _, root := range roots {
		contents, err := readRemoteEnvironmentText(ctx, conn, nextID, candidates[root].path)
		if err != nil {
			continue
		}
		var manifest remotePluginManifestName
		if err := json.Unmarshal([]byte(contents), &manifest); err != nil {
			continue
		}
		name := manifest.Name
		if strings.TrimSpace(name) == "" {
			name = remotePluginNamespaceFallbackName(root)
		}
		if name != "" {
			namespaces[root] = name
		}
	}
	if len(namespaces) == 0 {
		return nil
	}
	return namespaces
}

func remotePluginNamespacesFromRootAncestors(ctx context.Context, conn *websocket.Conn, nextID *int, rootPath string, existing map[string]string) map[string]string {
	current := remoteNormalizePathKey(rootPath)
	for i := 0; current != "" && i < 64; i++ {
		if _, ok := existing[current]; !ok {
			if namespace, ok := remotePluginNamespaceForRoot(ctx, conn, nextID, current); ok {
				if existing == nil {
					existing = map[string]string{}
				}
				existing[current] = namespace
			}
		}
		parent := remoteNormalizePathKey(remoteSkillDir(current))
		if parent == "" || parent == current {
			break
		}
		current = parent
	}
	return existing
}

func remotePluginNamespaceForRoot(ctx context.Context, conn *websocket.Conn, nextID *int, pluginRoot string) (string, bool) {
	for _, relativePath := range remoteDiscoverablePluginManifestPaths {
		manifestPath := remoteJoin(pluginRoot, relativePath)
		metadata, err := getRemoteEnvironmentMetadata(ctx, conn, nextID, manifestPath)
		if err != nil || metadata == nil || !metadata.IsFile {
			continue
		}
		contents, err := readRemoteEnvironmentText(ctx, conn, nextID, manifestPath)
		if err != nil {
			return "", false
		}
		var manifest remotePluginManifestName
		if err := json.Unmarshal([]byte(contents), &manifest); err != nil {
			return "", false
		}
		name := manifest.Name
		if strings.TrimSpace(name) == "" {
			name = remotePluginNamespaceFallbackName(pluginRoot)
		}
		return name, name != ""
	}
	return "", false
}

func remotePluginRootFromManifestPath(manifestPath string) (string, int, bool) {
	normalized := remoteNormalizePathKey(manifestPath)
	parsed, err := url.Parse(normalized)
	for priority, relativePath := range remoteDiscoverablePluginManifestPaths {
		suffix := "/" + relativePath
		if err == nil && parsed.Scheme != "" {
			cleanPath := "/" + strings.TrimPrefix(pathpkg.Clean(parsed.Path), "/")
			if !strings.HasSuffix(cleanPath, suffix) {
				continue
			}
			rootPath := strings.TrimSuffix(cleanPath, suffix)
			if rootPath == "" {
				rootPath = "/"
			}
			root := *parsed
			root.Path = rootPath
			root.RawPath = ""
			return root.String(), priority, true
		}
		cleanPath := strings.TrimPrefix(pathpkg.Clean(strings.ReplaceAll(normalized, "\\", "/")), "/")
		if cleanPath == relativePath {
			return ".", priority, true
		}
		if strings.HasSuffix(cleanPath, suffix) {
			root := strings.TrimSuffix(cleanPath, suffix)
			if root == "" {
				root = "."
			}
			return root, priority, true
		}
	}
	return "", 0, false
}

func remotePluginNamespaceForSkill(skillPath string, pluginNamespaces map[string]string) string {
	if len(pluginNamespaces) == 0 {
		return ""
	}
	current := remoteNormalizePathKey(remoteSkillDir(skillPath))
	for {
		if namespace := pluginNamespaces[current]; namespace != "" {
			return namespace
		}
		parent := remoteNormalizePathKey(remoteSkillDir(current))
		if parent == "" || parent == current {
			return ""
		}
		current = parent
	}
}

func remotePluginNamespaceFallbackName(pluginRoot string) string {
	name := remotePathBase(pluginRoot)
	if unescaped, err := url.PathUnescape(name); err == nil {
		name = unescaped
	}
	return firstNonEmpty(name, "plugin")
}

func callRemoteEnvironmentFS(ctx context.Context, conn *websocket.Conn, id int, method string, params any) (json.RawMessage, error) {
	if err := writeExecServerJSON(ctx, conn, &execServerJSONRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}); err != nil {
		return nil, err
	}
	return readExecServerResponse(ctx, conn, id)
}

func readRemoteEnvironmentText(ctx context.Context, conn *websocket.Conn, nextID *int, path string) (string, error) {
	if nextID == nil {
		return "", fmt.Errorf("remote fs request id is nil")
	}
	id := *nextID
	*nextID = *nextID + 1
	raw, err := callRemoteEnvironmentFS(ctx, conn, id, "fs/readFile", map[string]any{"path": path})
	if err != nil {
		return "", err
	}
	var response remoteFSReadFileResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return "", err
	}
	data, err := base64.StdEncoding.DecodeString(response.DataBase64)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func getRemoteEnvironmentMetadata(ctx context.Context, conn *websocket.Conn, nextID *int, path string) (*remoteFSGetMetadataResponse, error) {
	if nextID == nil {
		return nil, fmt.Errorf("remote fs request id is nil")
	}
	id := *nextID
	*nextID = *nextID + 1
	raw, err := callRemoteEnvironmentFS(ctx, conn, id, "fs/getMetadata", map[string]any{"path": path})
	if err != nil {
		return nil, err
	}
	var response remoteFSGetMetadataResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func remoteSkillEntryFromContents(environmentID string, skillPath string, contents string, metadataContents string, pluginNamespace string) (SkillsListEntry, string, bool) {
	defaultName := remoteSkillDefaultName(skillPath)
	parsed, err := parseSkillFrontmatterResult(contents, defaultName)
	if err != nil {
		return SkillsListEntry{}, fmt.Sprintf("Failed to load environment skill at %s: %s", skillPath, err), false
	}
	name := parsed.Name
	if pluginNamespace != "" {
		name = pluginNamespace + ":" + name
	}
	if len([]rune(name)) > skillMaxQualifiedNameLen {
		return SkillsListEntry{}, fmt.Sprintf("Failed to load environment skill at %s: %s", skillPath, invalidSkillFieldError("qualified name", skillMaxQualifiedNameLen)), false
	}
	entry := SkillsListEntry{
		Name:             name,
		Path:             remoteSkillSourcePath(environmentID, skillPath),
		Scope:            "environment",
		Description:      parsed.Description,
		ShortDescription: parsed.ShortDescription,
		Enabled:          true,
		Contents:         contents,
	}
	if strings.TrimSpace(metadataContents) != "" {
		var parsed skillMetadataFile
		if err := yaml.Unmarshal([]byte(metadataContents), &parsed); err == nil {
			entry.Interface = resolveRemoteSkillInterface(parsed.Interface, environmentID, remoteSkillDir(skillPath))
			entry.Dependencies = resolveSkillDependencies(parsed.Dependencies)
			entry.Policy = resolveSkillPolicy(parsed.Policy)
		}
	}
	return entry, "", true
}

func resolveRemoteSkillInterface(metadata *skillMetadataInterface, environmentID string, skillDir string) *SkillInterface {
	if metadata == nil {
		return nil
	}
	value := &SkillInterface{
		DisplayName:      resolveSkillString(metadata.displayName(), skillMaxNameLen),
		ShortDescription: resolveSkillString(metadata.shortDescription(), skillMaxDescriptionLen),
		IconSmall:        resolveRemoteSkillAssetPath(environmentID, skillDir, metadata.iconSmall()),
		IconLarge:        resolveRemoteSkillAssetPath(environmentID, skillDir, metadata.iconLarge()),
		BrandColor:       resolveSkillColor(metadata.brandColor()),
		DefaultPrompt:    optionalSkillString(metadata.defaultPrompt(), skillMaxDescriptionLen),
	}
	if value.DisplayName == "" && value.ShortDescription == "" && value.IconSmall == nil && value.IconLarge == nil && value.BrandColor == nil && value.DefaultPrompt == nil {
		return nil
	}
	return value
}

func resolveRemoteSkillAssetPath(environmentID string, skillDir string, value string) *string {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "/") || strings.Contains(value, ":") {
		return nil
	}
	cleaned := pathpkg.Clean(strings.ReplaceAll(value, "\\", "/"))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return nil
	}
	parts := strings.Split(cleaned, "/")
	if len(parts) == 0 || parts[0] != "assets" {
		return nil
	}
	joined := remoteJoin(skillDir, parts...)
	if strings.TrimSpace(joined) == "" {
		return nil
	}
	locator := remoteSkillSourcePath(environmentID, joined)
	return &locator
}

func remoteSkillMetadataPath(skillPath string) string {
	return remoteJoin(remoteSkillDir(skillPath), SkillMetadataDir, SkillMetadataFilename)
}

func remoteSkillSourcePath(environmentID string, skillPath string) string {
	environmentID = strings.TrimSpace(environmentID)
	if environmentID == "" {
		return skillPath
	}
	parsed, err := url.Parse(skillPath)
	if err == nil && parsed.Scheme != "" {
		path := parsed.EscapedPath()
		if path == "" {
			path = "/" + strings.TrimPrefix(parsed.Opaque, "/")
		}
		return "environment://" + url.PathEscape(environmentID) + path
	}
	cleaned := pathpkg.Clean(strings.ReplaceAll(skillPath, "\\", "/"))
	pathURL := url.URL{Path: "/" + strings.TrimPrefix(cleaned, "/")}
	return "environment://" + url.PathEscape(environmentID) + pathURL.EscapedPath()
}

func remoteSkillDir(path string) string {
	parsed, err := url.Parse(path)
	if err == nil && parsed.Scheme != "" {
		parsed.Path = pathpkg.Dir(parsed.Path)
		parsed.RawPath = ""
		return parsed.String()
	}
	return pathpkg.Dir(strings.ReplaceAll(path, "\\", "/"))
}

func remotePathBase(path string) string {
	parsed, err := url.Parse(path)
	if err == nil && parsed.Scheme != "" {
		return pathpkg.Base(parsed.Path)
	}
	return pathpkg.Base(strings.ReplaceAll(path, "\\", "/"))
}

func remoteSkillDefaultName(skillPath string) string {
	dir := remoteSkillDir(skillPath)
	name := remotePathBase(dir)
	if unescaped, err := url.PathUnescape(name); err == nil {
		name = unescaped
	}
	return firstNonEmpty(name, "skill")
}

func remoteNormalizePathKey(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	parsed, err := url.Parse(path)
	if err == nil && parsed.Scheme != "" {
		cleaned := pathpkg.Clean(parsed.Path)
		if cleaned == "." {
			cleaned = "/"
		}
		if !strings.HasPrefix(cleaned, "/") {
			cleaned = "/" + cleaned
		}
		parsed.Path = cleaned
		parsed.RawPath = ""
		return parsed.String()
	}
	return pathpkg.Clean(strings.ReplaceAll(path, "\\", "/"))
}

func remoteJoin(base string, parts ...string) string {
	parsed, err := url.Parse(base)
	if err == nil && parsed.Scheme != "" {
		segments := []string{parsed.Path}
		segments = append(segments, parts...)
		parsed.Path = pathpkg.Join(segments...)
		parsed.RawPath = ""
		return parsed.String()
	}
	segments := []string{strings.ReplaceAll(base, "\\", "/")}
	segments = append(segments, parts...)
	return pathpkg.Join(segments...)
}
