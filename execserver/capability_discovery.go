package execserver

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"unicode/utf8"

	"codex_go/utils"
)

const (
	maxCapabilityDiscoveryRoots     = 128
	maxCapabilityScanDepth          = 6
	maxCapabilityDirectoriesPerRoot = 2_000
	maxCapabilityEntriesPerRoot     = 20_000
	maxCapabilityFileBytes          = 1024 * 1024
	maxCapabilityBundleBytesPerRoot = 16 * 1024 * 1024
	capabilitySkillFileName         = "SKILL.md"
	capabilitySkillMetadataPath     = "agents/openai.yaml"
	capabilityDefaultMCPConfigPath  = ".mcp.json"
)

var discoverablePluginManifestPaths = []string{
	".codex-plugin/plugin.json",
	".claude-plugin/plugin.json",
	".cursor-plugin/plugin.json",
}

func discoverCapabilityRoots(params *CapabilityRootsDiscoverParams) (*CapabilityRootsDiscoverResponse, error) {
	if params == nil || len(params.Roots) == 0 {
		return &CapabilityRootsDiscoverResponse{Roots: []CapabilityRootDiscovery{}}, nil
	}
	if len(params.Roots) > maxCapabilityDiscoveryRoots {
		return nil, requestError(-32602, fmt.Sprintf("capability root discovery accepts at most %d roots", maxCapabilityDiscoveryRoots))
	}
	response := &CapabilityRootsDiscoverResponse{Roots: make([]CapabilityRootDiscovery, 0, len(params.Roots))}
	for _, root := range params.Roots {
		response.Roots = append(response.Roots, discoverCapabilityRoot(root))
	}
	return response, nil
}

func discoverCapabilityRoot(request CapabilityRootDiscoverRequest) CapabilityRootDiscovery {
	discovery := CapabilityRootDiscovery{
		ID:                 request.ID,
		Path:               request.Path,
		Skills:             []DiscoveredSkillFiles{},
		NamespaceManifests: []CapabilityTextFile{},
		Warnings:           []string{},
	}
	metadata, err := getMetadata(&FSGetMetadataParams{Path: request.Path, Sandbox: request.Sandbox})
	if err != nil {
		discovery.Error = stringPtr(fmt.Sprintf("failed to inspect capability root %s: %v", request.Path, err))
		return discovery
	}
	if !metadata.IsDirectory {
		discovery.Error = stringPtr(fmt.Sprintf("capability root %s is not a directory", request.Path))
		return discovery
	}
	walk, err := walkPath(&FSWalkParams{
		Path: request.Path,
		Options: FSWalkOptions{
			MaxDepth:                maxCapabilityScanDepth,
			MaxDirectories:          maxCapabilityDirectoriesPerRoot,
			MaxEntries:              maxCapabilityEntriesPerRoot,
			FollowDirectorySymlinks: true,
		},
		Sandbox: request.Sandbox,
	})
	if err != nil {
		discovery.Error = stringPtr(fmt.Sprintf("failed to scan capability root %s: %v", request.Path, err))
		return discovery
	}
	for _, walkErr := range walk.Errors {
		discovery.Warnings = append(discovery.Warnings, fmt.Sprintf("failed to scan capability path %s: %s", walkErr.Path, walkErr.Message))
	}
	if walk.Truncated {
		discovery.Warnings = append(discovery.Warnings, fmt.Sprintf("capability scan reached its traversal limit (root: %s)", request.Path))
	}

	skillPaths := []string{}
	namespaceManifestPaths := []string{}
	for _, entry := range walk.Entries {
		if entry.Kind != "file" {
			continue
		}
		if capabilityBasename(entry.Path) == capabilitySkillFileName {
			skillPaths = append(skillPaths, entry.Path)
		}
		if capabilityPluginManifestPriority(entry.Path) >= 0 {
			namespaceManifestPaths = append(namespaceManifestPaths, entry.Path)
		}
	}
	sort.Strings(skillPaths)
	sort.SliceStable(namespaceManifestPaths, func(i, j int) bool {
		leftRoot, _ := capabilityPluginRoot(namespaceManifestPaths[i])
		rightRoot, _ := capabilityPluginRoot(namespaceManifestPaths[j])
		if leftRoot != rightRoot {
			return leftRoot < rightRoot
		}
		return capabilityPluginManifestPriority(namespaceManifestPaths[i]) < capabilityPluginManifestPriority(namespaceManifestPaths[j])
	})

	budget := &capabilityBundleBudget{}
	rootManifest := readFirstCapabilityPluginManifest(request.Path, request.Sandbox, budget, &discovery.Warnings)
	inheritedManifest := rootManifest
	if inheritedManifest == nil {
		inheritedManifest = readNearestCapabilityAncestorManifest(request.Path, request.Sandbox, budget, &discovery.Warnings)
	}
	seenNamespaceRoots := map[string]bool{}
	if inheritedManifest != nil {
		if pluginRoot, ok := capabilityPluginRoot(inheritedManifest.Path); ok {
			seenNamespaceRoots[pluginRoot] = true
		}
		discovery.NamespaceManifests = append(discovery.NamespaceManifests, *inheritedManifest)
	}
	for _, manifestPath := range namespaceManifestPaths {
		pluginRoot, ok := capabilityPluginRoot(manifestPath)
		if !ok || seenNamespaceRoots[pluginRoot] {
			continue
		}
		seenNamespaceRoots[pluginRoot] = true
		if manifest := readOptionalCapabilityTextFile(manifestPath, request.Sandbox, budget, &discovery.Warnings); manifest != nil {
			discovery.NamespaceManifests = append(discovery.NamespaceManifests, *manifest)
		}
	}

	if rootManifest != nil {
		declarations := capabilityPluginDeclarationPaths(request.Path, *rootManifest, &discovery.Warnings)
		mcpPath := declarations.mcpConfig
		if mcpPath == "" && !declarations.mcpInline {
			mcpPath, _ = capabilityJoin(request.Path, capabilityDefaultMCPConfigPath)
		}
		var mcpConfig *CapabilityTextFile
		if mcpPath != "" {
			mcpConfig = readOptionalCapabilityTextFile(mcpPath, request.Sandbox, budget, &discovery.Warnings)
		}
		var appsConfig *CapabilityTextFile
		if declarations.appsConfig != "" {
			appsConfig = readOptionalCapabilityTextFile(declarations.appsConfig, request.Sandbox, budget, &discovery.Warnings)
		}
		discovery.Plugin = &DiscoveredPluginFiles{Manifest: *rootManifest, MCPConfig: mcpConfig, AppsConfig: appsConfig}
	}

	for _, skillPath := range skillPaths {
		instructions := readOptionalCapabilityTextFile(skillPath, request.Sandbox, budget, &discovery.Warnings)
		if instructions == nil {
			continue
		}
		var metadata *CapabilityTextFile
		if skillDir, ok := capabilityParent(skillPath); ok {
			if metadataPath, joinErr := capabilityJoin(skillDir, capabilitySkillMetadataPath); joinErr == nil {
				metadata = readOptionalCapabilityTextFile(metadataPath, request.Sandbox, budget, &discovery.Warnings)
			}
		}
		discovery.Skills = append(discovery.Skills, DiscoveredSkillFiles{Instructions: *instructions, Metadata: metadata})
	}
	return discovery
}

func readFirstCapabilityPluginManifest(root string, sandbox *FileSystemSandboxContext, budget *capabilityBundleBudget, warnings *[]string) *CapabilityTextFile {
	for _, relativePath := range discoverablePluginManifestPaths {
		path, err := capabilityJoin(root, relativePath)
		if err != nil {
			continue
		}
		if manifest := readOptionalCapabilityTextFile(path, sandbox, budget, warnings); manifest != nil {
			return manifest
		}
	}
	return nil
}

func readNearestCapabilityAncestorManifest(root string, sandbox *FileSystemSandboxContext, budget *capabilityBundleBudget, warnings *[]string) *CapabilityTextFile {
	ancestor, ok := capabilityParent(root)
	for ok {
		if manifest := readFirstCapabilityPluginManifest(ancestor, sandbox, budget, warnings); manifest != nil {
			return manifest
		}
		ancestor, ok = capabilityParent(ancestor)
	}
	return nil
}

func readOptionalCapabilityTextFile(path string, sandbox *FileSystemSandboxContext, budget *capabilityBundleBudget, warnings *[]string) *CapabilityTextFile {
	metadata, err := getMetadata(&FSGetMetadataParams{Path: path, Sandbox: sandbox})
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			*warnings = append(*warnings, fmt.Sprintf("failed to inspect capability file %s: %v", path, err))
		}
		return nil
	}
	if !metadata.IsFile {
		return nil
	}
	if metadata.Size < 0 || metadata.Size > maxCapabilityFileBytes {
		*warnings = append(*warnings, fmt.Sprintf("capability file %s exceeds the %d-byte limit", path, maxCapabilityFileBytes))
		return nil
	}
	if !budget.canAdd(int(metadata.Size)) {
		*warnings = append(*warnings, fmt.Sprintf("capability root bundle exceeds the %d-byte limit", maxCapabilityBundleBytesPerRoot))
		return nil
	}
	response, err := readFile(&FSReadFileParams{Path: path, Sandbox: sandbox})
	if err != nil {
		*warnings = append(*warnings, fmt.Sprintf("failed to read capability file %s: %v", path, err))
		return nil
	}
	contents, err := base64.StdEncoding.DecodeString(response.DataBase64)
	if err != nil {
		*warnings = append(*warnings, fmt.Sprintf("failed to read capability file %s: %v", path, err))
		return nil
	}
	if len(contents) > maxCapabilityFileBytes || !budget.canAdd(len(contents)) {
		*warnings = append(*warnings, fmt.Sprintf("capability file %s exceeded its read limit", path))
		return nil
	}
	if !utf8.Valid(contents) {
		*warnings = append(*warnings, fmt.Sprintf("capability file %s is not UTF-8", path))
		return nil
	}
	budget.add(len(contents))
	return &CapabilityTextFile{Path: path, Contents: string(contents)}
}

func capabilityPluginManifestPriority(path string) int {
	if capabilityBasename(path) != "plugin.json" {
		return -1
	}
	parent, ok := capabilityParent(path)
	if !ok {
		return -1
	}
	directory := capabilityBasename(parent)
	for index, relativePath := range discoverablePluginManifestPaths {
		if strings.TrimSuffix(relativePath, "/plugin.json") == directory {
			return index
		}
	}
	return -1
}

func capabilityPluginRoot(path string) (string, bool) {
	parent, ok := capabilityParent(path)
	if !ok {
		return "", false
	}
	return capabilityParent(parent)
}

type capabilityBundleBudget struct{ bytes int }

func (b *capabilityBundleBudget) canAdd(bytes int) bool {
	return bytes >= 0 && b.bytes <= maxCapabilityBundleBytesPerRoot-bytes
}

func (b *capabilityBundleBudget) add(bytes int) { b.bytes += bytes }

type capabilityPluginDeclarations struct {
	mcpConfig  string
	mcpInline  bool
	appsConfig string
}

func capabilityPluginDeclarationPaths(root string, manifest CapabilityTextFile, warnings *[]string) capabilityPluginDeclarations {
	var values map[string]any
	if json.Unmarshal([]byte(manifest.Contents), &values) != nil {
		return capabilityPluginDeclarations{}
	}
	declarations := capabilityPluginDeclarations{}
	if value, ok := values["mcpServers"]; ok {
		_, declarations.mcpInline = value.(map[string]any)
		if path, ok := value.(string); ok {
			declarations.mcpConfig = declaredCapabilityFilePath(root, "mcpServers", path, manifest.Path, warnings)
		}
	}
	if path, ok := values["apps"].(string); ok {
		declarations.appsConfig = declaredCapabilityFilePath(root, "apps", path, manifest.Path, warnings)
	}
	return declarations
}

func declaredCapabilityFilePath(root, field, value, manifestPath string, warnings *[]string) string {
	if !strings.HasPrefix(value, "./") {
		*warnings = append(*warnings, fmt.Sprintf("ignoring %s in %s: path must start with `./`", field, manifestPath))
		return ""
	}
	relativePath := strings.TrimPrefix(value, "./")
	unsafe := relativePath == ""
	for _, component := range strings.FieldsFunc(relativePath, func(r rune) bool { return r == '/' || r == '\\' }) {
		unsafe = unsafe || component == ".."
	}
	if unsafe {
		*warnings = append(*warnings, fmt.Sprintf("ignoring %s in %s: path must remain below the capability root", field, manifestPath))
		return ""
	}
	joined, err := capabilityJoin(root, relativePath)
	rootURI, rootErr := utils.Parse(root)
	joinedURI, joinedErr := utils.Parse(joined)
	if err != nil || rootErr != nil || joinedErr != nil || !joinedURI.StartsWith(rootURI) {
		*warnings = append(*warnings, fmt.Sprintf("ignoring %s in %s: path must remain below the capability root", field, manifestPath))
		return ""
	}
	return joined
}

func capabilityJoin(root, relativePath string) (string, error) {
	uri, err := utils.Parse(root)
	if err != nil {
		return "", err
	}
	joined, err := uri.Join(relativePath)
	if err != nil {
		return "", err
	}
	return joined.String(), nil
}

func capabilityParent(path string) (string, bool) {
	uri, err := utils.Parse(path)
	if err != nil {
		return "", false
	}
	parent, ok := uri.Parent()
	if !ok {
		return "", false
	}
	return parent.String(), true
}

func capabilityBasename(path string) string {
	uri, err := utils.Parse(path)
	if err != nil {
		return ""
	}
	base, _ := uri.Basename()
	return base
}
