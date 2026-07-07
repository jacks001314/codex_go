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
	remoteSkillWalkMaxDepth       = 8
	remoteSkillWalkMaxDirectories = 256
	remoteSkillWalkMaxEntries     = 4096
)

type remoteFSWalkEntry struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

type remoteFSWalkResponse struct {
	Entries   []remoteFSWalkEntry `json:"entries"`
	Truncated bool                `json:"truncated"`
}

type remoteFSReadFileResponse struct {
	DataBase64 string `json:"dataBase64"`
}

func discoverRemoteEnvironmentSkills(ctx context.Context, record *EnvironmentRecord, rootPath string) ([]SkillsListEntry, error) {
	if record == nil {
		return nil, nil
	}
	rootPath = strings.TrimSpace(rootPath)
	if rootPath == "" {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, environmentConnectTimeout(record.ConnectTimeoutMS))
	defer cancel()
	conn, _, err := websocket.Dial(ctx, record.ExecServerURL, nil)
	if err != nil {
		return nil, err
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
		return nil, err
	}
	if _, err := readExecServerResponse(ctx, conn, 1); err != nil {
		return nil, err
	}
	if err := writeExecServerJSON(ctx, conn, &execServerJSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "initialized",
	}); err != nil {
		return nil, err
	}
	walkRaw, err := callRemoteEnvironmentFS(ctx, conn, 2, "fs/walk", map[string]any{
		"path": rootPath,
		"options": map[string]any{
			"maxDepth":                remoteSkillWalkMaxDepth,
			"maxDirectories":          remoteSkillWalkMaxDirectories,
			"maxEntries":              remoteSkillWalkMaxEntries,
			"followDirectorySymlinks": false,
		},
	})
	if err != nil {
		return nil, err
	}
	var walk remoteFSWalkResponse
	if err := json.Unmarshal(walkRaw, &walk); err != nil {
		return nil, err
	}
	sort.SliceStable(walk.Entries, func(i int, j int) bool {
		return walk.Entries[i].Path < walk.Entries[j].Path
	})
	remoteFiles := map[string]bool{}
	for _, entry := range walk.Entries {
		if entry.Kind == "file" {
			remoteFiles[entry.Path] = true
		}
	}
	entries := make([]SkillsListEntry, 0)
	nextID := 3
	for _, entry := range walk.Entries {
		if entry.Kind != "file" || remotePathBase(entry.Path) != SkillFilename {
			continue
		}
		contents, err := readRemoteEnvironmentText(ctx, conn, &nextID, entry.Path)
		if err != nil {
			return nil, err
		}
		metadataPath := remoteSkillMetadataPath(entry.Path)
		metadataContents := ""
		if remoteFiles[metadataPath] {
			metadataContents, _ = readRemoteEnvironmentText(ctx, conn, &nextID, metadataPath)
		}
		entries = append(entries, remoteSkillEntryFromContents(record.EnvironmentID, entry.Path, contents, metadataContents))
	}
	return entries, nil
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

func remoteSkillEntryFromContents(environmentID string, skillPath string, contents string, metadataContents string) SkillsListEntry {
	defaultName := remoteSkillDefaultName(skillPath)
	name := defaultName
	description := ""
	shortDescription := ""
	if parsed, ok := parseSkillFrontmatter(contents, defaultName); ok {
		name = parsed.Name
		description = parsed.Description
		shortDescription = firstNonEmpty(parsed.ShortDescription, parsed.Description)
	} else {
		description = firstLineFromText(contents)
		shortDescription = description
	}
	entry := SkillsListEntry{
		Name:             name,
		Path:             remoteSkillSourcePath(environmentID, skillPath),
		Scope:            "environment",
		Description:      description,
		ShortDescription: shortDescription,
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
	return entry
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
