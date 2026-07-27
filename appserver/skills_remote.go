package appserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	pathpkg "path"
	"sort"
	"strings"

	execserverclient "codex_go/execserver"
	"codex_go/plugin"

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
	".cursor-plugin/plugin.json",
}

const remoteAgentPluginManifestPath = "plugin.json"

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

type remoteEnvironmentFSCaller interface {
	Call(context.Context, int, string, any) (json.RawMessage, error)
}

type websocketRemoteEnvironmentFSCaller struct {
	conn *websocket.Conn
}

func (c websocketRemoteEnvironmentFSCaller) Call(ctx context.Context, id int, method string, params any) (json.RawMessage, error) {
	return callRemoteEnvironmentFS(ctx, c.conn, id, method, params)
}

type clientRemoteEnvironmentFSCaller struct {
	client *execserverclient.Client
}

func (c clientRemoteEnvironmentFSCaller) Call(ctx context.Context, _ int, method string, params any) (json.RawMessage, error) {
	encoded, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	var response any
	switch method {
	case execserverclient.MethodFSWalk:
		var request execserverclient.FSWalkParams
		if err := json.Unmarshal(encoded, &request); err != nil {
			return nil, err
		}
		response, err = c.client.FSWalk(ctx, &request)
	case execserverclient.MethodFSReadFile:
		var request execserverclient.FSReadFileParams
		if err := json.Unmarshal(encoded, &request); err != nil {
			return nil, err
		}
		response, err = c.client.FSReadFile(ctx, &request)
	case execserverclient.MethodFSGetMetadata:
		var request execserverclient.FSGetMetadataParams
		if err := json.Unmarshal(encoded, &request); err != nil {
			return nil, err
		}
		response, err = c.client.FSGetMetadata(ctx, &request)
	default:
		return nil, fmt.Errorf("unsupported remote filesystem method %s", method)
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(response)
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
	var caller remoteEnvironmentFSCaller
	if record.NoiseProvider != nil {
		client, err := execserverclient.DialNoiseRendezvousClient(ctx, record.NoiseProvider, execserverclient.DialClientOptions{ClientName: "codex-go", HTTPClient: record.HTTPClient})
		if err != nil {
			return nil, nil, err
		}
		defer client.Close()
		caller = clientRemoteEnvironmentFSCaller{client: client}
	} else {
		conn, _, err := websocket.Dial(ctx, record.ExecServerURL, &websocket.DialOptions{HTTPClient: record.HTTPClient})
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
		caller = websocketRemoteEnvironmentFSCaller{conn: conn}
	}
	walkRaw, err := caller.Call(ctx, 2, "fs/walk", map[string]any{
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
	pluginNamespaces := remotePluginNamespacesFromInventory(ctx, caller, &nextID, remoteFiles)
	pluginNamespaces = remotePluginNamespacesFromRootAncestors(ctx, caller, &nextID, rootPath, pluginNamespaces)
	entries := make([]SkillsListEntry, 0)
	for _, entry := range walk.Entries {
		if entry.Kind != "file" || remotePathBase(entry.Path) != SkillFilename {
			continue
		}
		pluginDescriptor, _ := remotePluginDescriptorForSkill(entry.Path, pluginNamespaces)
		if pluginDescriptor.Agent && !remoteAgentPluginDirectChildSkill(pluginDescriptor.Root, entry.Path) {
			continue
		}
		contents, err := readRemoteEnvironmentText(ctx, caller, &nextID, entry.Path)
		if err != nil {
			return nil, warnings, err
		}
		metadataPath := remoteSkillMetadataPath(entry.Path)
		metadataContents := ""
		if remoteFiles[remoteNormalizePathKey(metadataPath)] {
			metadataContents, _ = readRemoteEnvironmentText(ctx, caller, &nextID, metadataPath)
		}
		pluginNamespace := pluginDescriptor.Name
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

func readRemoteEnvironmentSkillText(ctx context.Context, record *EnvironmentRecord, resourcePath string) (string, error) {
	if record == nil {
		return "", errors.New("remote environment is unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, environmentConnectTimeout(record.ConnectTimeoutMS))
	defer cancel()
	var caller remoteEnvironmentFSCaller
	if record.NoiseProvider != nil {
		client, err := execserverclient.DialNoiseRendezvousClient(ctx, record.NoiseProvider, execserverclient.DialClientOptions{ClientName: "codex-go", HTTPClient: record.HTTPClient})
		if err != nil {
			return "", err
		}
		defer client.Close()
		caller = clientRemoteEnvironmentFSCaller{client: client}
	} else {
		conn, _, err := websocket.Dial(ctx, record.ExecServerURL, &websocket.DialOptions{HTTPClient: record.HTTPClient})
		if err != nil {
			return "", err
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		if err := writeExecServerJSON(ctx, conn, &execServerJSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: "initialize", Params: map[string]any{"clientName": "codex-go"}}); err != nil {
			return "", err
		}
		if _, err := readExecServerResponse(ctx, conn, 1); err != nil {
			return "", err
		}
		if err := writeExecServerJSON(ctx, conn, &execServerJSONRPCRequest{JSONRPC: "2.0", Method: "initialized"}); err != nil {
			return "", err
		}
		caller = websocketRemoteEnvironmentFSCaller{conn: conn}
	}
	nextID := 2
	return readRemoteEnvironmentText(ctx, caller, &nextID, resourcePath)
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
	agent    bool
}

type remotePluginManifestName struct {
	Name string `json:"name"`
}

type remotePluginDescriptor struct {
	Root  string
	Name  string
	Agent bool
}

func remotePluginNamespacesFromInventory(ctx context.Context, caller remoteEnvironmentFSCaller, nextID *int, remoteFiles map[string]bool) map[string]remotePluginDescriptor {
	if len(remoteFiles) == 0 {
		return nil
	}
	candidates := map[string][]remotePluginManifestCandidate{}
	for path := range remoteFiles {
		root, priority, agentManifest, ok := remotePluginRootFromManifestPath(path)
		if !ok {
			continue
		}
		key := remoteNormalizePathKey(root)
		candidates[key] = append(candidates[key], remotePluginManifestCandidate{path: path, priority: priority, agent: agentManifest})
	}
	if len(candidates) == 0 {
		return nil
	}
	roots := make([]string, 0, len(candidates))
	for root := range candidates {
		roots = append(roots, root)
	}
	sort.Strings(roots)
	namespaces := make(map[string]remotePluginDescriptor, len(roots))
	for _, root := range roots {
		sort.SliceStable(candidates[root], func(i, j int) bool { return candidates[root][i].priority < candidates[root][j].priority })
		if descriptor, ok := remotePluginDescriptorFromCandidates(ctx, caller, nextID, root, candidates[root]); ok {
			namespaces[root] = descriptor
		}
	}
	if len(namespaces) == 0 {
		return nil
	}
	return namespaces
}

func remotePluginNamespacesFromRootAncestors(ctx context.Context, caller remoteEnvironmentFSCaller, nextID *int, rootPath string, existing map[string]remotePluginDescriptor) map[string]remotePluginDescriptor {
	current := remoteNormalizePathKey(rootPath)
	for i := 0; current != "" && i < 64; i++ {
		if _, ok := existing[current]; !ok {
			if descriptor, ok := remotePluginNamespaceForRoot(ctx, caller, nextID, current); ok {
				if existing == nil {
					existing = map[string]remotePluginDescriptor{}
				}
				existing[current] = descriptor
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

func remotePluginNamespaceForRoot(ctx context.Context, caller remoteEnvironmentFSCaller, nextID *int, pluginRoot string) (remotePluginDescriptor, bool) {
	candidates := make([]remotePluginManifestCandidate, 0, len(remoteDiscoverablePluginManifestPaths)+1)
	for priority, relativePath := range append([]string{remoteAgentPluginManifestPath}, remoteDiscoverablePluginManifestPaths...) {
		manifestPath := remoteJoin(pluginRoot, relativePath)
		metadata, err := getRemoteEnvironmentMetadata(ctx, caller, nextID, manifestPath)
		if err != nil || metadata == nil || !metadata.IsFile {
			continue
		}
		candidates = append(candidates, remotePluginManifestCandidate{path: manifestPath, priority: priority, agent: relativePath == remoteAgentPluginManifestPath})
	}
	return remotePluginDescriptorFromCandidates(ctx, caller, nextID, pluginRoot, candidates)
}

func remotePluginRootFromManifestPath(manifestPath string) (string, int, bool, bool) {
	normalized := remoteNormalizePathKey(manifestPath)
	parsed, err := url.Parse(normalized)
	// Match the longer legacy paths before the root plugin.json suffix. The
	// latter is otherwise also a suffix of .codex-plugin/plugin.json.
	paths := append([]string(nil), remoteDiscoverablePluginManifestPaths...)
	paths = append(paths, remoteAgentPluginManifestPath)
	for index, relativePath := range paths {
		priority := index + 1
		if relativePath == remoteAgentPluginManifestPath {
			priority = 0
		}
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
			return root.String(), priority, relativePath == remoteAgentPluginManifestPath, true
		}
		cleanPath := strings.TrimPrefix(pathpkg.Clean(strings.ReplaceAll(normalized, "\\", "/")), "/")
		if cleanPath == relativePath {
			return ".", priority, relativePath == remoteAgentPluginManifestPath, true
		}
		if strings.HasSuffix(cleanPath, suffix) {
			root := strings.TrimSuffix(cleanPath, suffix)
			if root == "" {
				root = "."
			}
			return root, priority, relativePath == remoteAgentPluginManifestPath, true
		}
	}
	return "", 0, false, false
}

func remotePluginDescriptorFromCandidates(ctx context.Context, caller remoteEnvironmentFSCaller, nextID *int, pluginRoot string, candidates []remotePluginManifestCandidate) (remotePluginDescriptor, bool) {
	for _, candidate := range candidates {
		contents, err := readRemoteEnvironmentText(ctx, caller, nextID, candidate.path)
		if err != nil {
			continue
		}
		if candidate.agent {
			status, _ := plugin.AgentPluginSchemaStatusForContents([]byte(contents))
			switch status {
			case plugin.AgentPluginSchemaUnsupported:
				return remotePluginDescriptor{}, false
			case plugin.AgentPluginSchemaUnrelated:
				continue
			}
		}
		var manifest remotePluginManifestName
		if err := json.Unmarshal([]byte(contents), &manifest); err != nil {
			continue
		}
		name := strings.TrimSpace(manifest.Name)
		if name == "" {
			name = remotePluginNamespaceFallbackName(pluginRoot)
		}
		if name != "" {
			return remotePluginDescriptor{Root: remoteNormalizePathKey(pluginRoot), Name: name, Agent: candidate.agent}, true
		}
	}
	return remotePluginDescriptor{}, false
}

func remotePluginDescriptorForSkill(skillPath string, pluginNamespaces map[string]remotePluginDescriptor) (remotePluginDescriptor, bool) {
	if len(pluginNamespaces) == 0 {
		return remotePluginDescriptor{}, false
	}
	current := remoteNormalizePathKey(remoteSkillDir(skillPath))
	for {
		if descriptor, ok := pluginNamespaces[current]; ok && descriptor.Name != "" {
			return descriptor, true
		}
		parent := remoteNormalizePathKey(remoteSkillDir(current))
		if parent == "" || parent == current {
			return remotePluginDescriptor{}, false
		}
		current = parent
	}
}

func remotePluginNamespaceForSkill(skillPath string, pluginNamespaces map[string]remotePluginDescriptor) string {
	descriptor, _ := remotePluginDescriptorForSkill(skillPath, pluginNamespaces)
	return descriptor.Name
}

func remoteAgentPluginDirectChildSkill(pluginRoot string, skillPath string) bool {
	root := strings.TrimSuffix(remoteNormalizePathKey(pluginRoot), "/")
	path := remoteNormalizePathKey(skillPath)
	prefix := root + "/"
	if root == "." {
		prefix = ""
	}
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	relative := strings.TrimPrefix(path, prefix)
	parts := strings.Split(relative, "/")
	return len(parts) == 3 && parts[0] == "skills" && parts[1] != "" && parts[2] == SkillFilename
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

func readRemoteEnvironmentText(ctx context.Context, caller remoteEnvironmentFSCaller, nextID *int, path string) (string, error) {
	if nextID == nil {
		return "", fmt.Errorf("remote fs request id is nil")
	}
	id := *nextID
	*nextID = *nextID + 1
	raw, err := caller.Call(ctx, id, "fs/readFile", map[string]any{"path": path})
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

func getRemoteEnvironmentMetadata(ctx context.Context, caller remoteEnvironmentFSCaller, nextID *int, path string) (*remoteFSGetMetadataResponse, error) {
	if nextID == nil {
		return nil, fmt.Errorf("remote fs request id is nil")
	}
	id := *nextID
	*nextID = *nextID + 1
	raw, err := caller.Call(ctx, id, "fs/getMetadata", map[string]any{"path": path})
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
		EnvironmentID:    environmentID,
		SourcePath:       skillPath,
	}
	if strings.TrimSpace(metadataContents) != "" {
		var parsed skillMetadataFile
		if err := yaml.Unmarshal([]byte(metadataContents), &parsed); err == nil && skillMetadataProductsValid(parsed.Policy) {
			entry.Dependencies = resolveSkillDependencies(parsed.Dependencies)
			entry.Policy = resolveSkillPolicy(parsed.Policy)
		}
	}
	return entry, "", true
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
