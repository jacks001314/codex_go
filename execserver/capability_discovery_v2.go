package execserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"codex_go/utils"
)

type v2CapabilityLocation struct {
	root        string
	id          string
	marketplace string
	plugin      string
	version     string
	pluginRoot  bool
}

func discoverCapabilitiesV2(params *DiscoverV2CapabilitiesParams) (*DiscoverV2CapabilitiesResponse, error) {
	if params == nil || strings.TrimSpace(params.CWD) == "" {
		return nil, requestError(-32602, "capabilities/discoverV2 cwd is required")
	}
	if err := validateWirePathURI(params.CWD); err != nil {
		return nil, requestError(-32602, "capabilities/discoverV2 cwd must be an absolute file URI: "+err.Error())
	}
	codexHome := fsHelperCodexHome()
	userHome, _ := os.UserHomeDir()
	locations := []v2CapabilityLocation{
		{root: filepath.Join(codexHome, "skills"), id: "codex-home-skills"},
	}
	if userHome != "" {
		locations = append(locations, v2CapabilityLocation{
			root: filepath.Join(userHome, ".agents", "skills"),
			id:   "user-agent-skills",
		})
	}
	pluginRoots, warnings := discoverV2PluginRoots(filepath.Join(codexHome, "plugins", "cache"), params.Sandbox)
	locations = append(locations, pluginRoots...)

	response := &DiscoverV2CapabilitiesResponse{
		Plugins: []ExecutorPlugin{}, Skills: []ExecutorSkill{}, Warnings: warnings,
	}
	for _, location := range locations {
		rootURI, err := utils.FromHostNativePath(location.root)
		if err != nil {
			response.Warnings = append(response.Warnings, "could not resolve capability root "+location.root+": "+err.Error())
			continue
		}
		discovery := discoverCapabilityRoot(CapabilityRootDiscoverRequest{
			ID: location.id, Path: rootURI.String(), Sandbox: params.Sandbox,
		})
		if discovery.Error != nil {
			response.Warnings = append(response.Warnings, *discovery.Error)
			continue
		}
		response.Warnings = append(response.Warnings, discovery.Warnings...)
		if location.pluginRoot {
			manifestName, displayName, files := v2PluginMetadata(discovery)
			id := firstNonEmptyV2(manifestName, location.plugin)
			if location.marketplace != "" {
				id += "@" + location.marketplace
			}
			response.Plugins = append(response.Plugins, ExecutorPlugin{
				ID: id, DisplayName: firstNonEmptyV2(displayName, manifestName, location.plugin),
				Version: location.version, Root: rootURI.String(), Files: files,
				Skills: v2SkillsFromFiles(discovery.Skills, "", ""),
			})
			continue
		}
		response.Skills = append(response.Skills, v2SkillsFromFiles(discovery.Skills, location.id, rootURI.String())...)
	}
	sort.SliceStable(response.Plugins, func(i, j int) bool { return response.Plugins[i].ID < response.Plugins[j].ID })
	sort.SliceStable(response.Skills, func(i, j int) bool { return response.Skills[i].Name < response.Skills[j].Name })
	return response, nil
}

func discoverV2PluginRoots(cacheRoot string, sandbox *FileSystemSandboxContext) ([]v2CapabilityLocation, []string) {
	cacheURI, err := utils.FromHostNativePath(cacheRoot)
	if err != nil {
		return nil, []string{"could not resolve plugin cache " + cacheRoot + ": " + err.Error()}
	}
	walk, err := walkPath(&FSWalkParams{
		Path: cacheURI.String(),
		Options: FSWalkOptions{
			MaxDepth: 5, MaxDirectories: maxCapabilityDirectoriesPerRoot,
			MaxEntries: maxCapabilityEntriesPerRoot,
		},
		Sandbox: sandbox,
	})
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []string{"could not scan plugin cache " + cacheRoot + ": " + err.Error()}
	}
	seen := map[string]bool{}
	var roots []v2CapabilityLocation
	for _, entry := range walk.Entries {
		if entry.Kind != "file" {
			continue
		}
		uri, ok := utils.InferredPathURI(entry.Path)
		if !ok {
			continue
		}
		native, err := uri.HostNativePath()
		if err != nil {
			continue
		}
		if filepath.Base(native) != "plugin.json" || filepath.Base(filepath.Dir(native)) != ".codex-plugin" {
			continue
		}
		root := filepath.Dir(filepath.Dir(native))
		if seen[root] {
			continue
		}
		relative, err := filepath.Rel(cacheRoot, root)
		if err != nil || relative == "." || strings.HasPrefix(relative, "..") {
			continue
		}
		parts := strings.Split(filepath.ToSlash(relative), "/")
		if len(parts) < 3 {
			continue
		}
		seen[root] = true
		roots = append(roots, v2CapabilityLocation{
			root: root, id: parts[1], marketplace: parts[0],
			plugin: parts[1], version: parts[2], pluginRoot: true,
		})
	}
	return roots, nil
}

func v2PluginMetadata(discovery CapabilityRootDiscovery) (string, string, DiscoveredPluginFiles) {
	var files DiscoveredPluginFiles
	if discovery.Plugin == nil {
		return "", "", files
	}
	files = *discovery.Plugin
	var manifest struct {
		Name      string `json:"name"`
		Interface struct {
			DisplayName string `json:"displayName"`
		} `json:"interface"`
	}
	_ = json.Unmarshal([]byte(files.Manifest.Contents), &manifest)
	return strings.TrimSpace(manifest.Name), strings.TrimSpace(manifest.Interface.DisplayName), files
}

func v2SkillsFromFiles(files []DiscoveredSkillFiles, rootID string, rootPath string) []ExecutorSkill {
	out := make([]ExecutorSkill, 0, len(files))
	for _, file := range files {
		name, description := parseSkillFrontmatterV2(file.Instructions.Contents)
		if name == "" {
			name = filepath.Base(filepath.Dir(filepath.FromSlash(file.Instructions.Path)))
		}
		skill := ExecutorSkill{Name: name, Description: description, Path: file.Instructions.Path}
		if rootID != "" && rootPath != "" {
			skill.DiscoveryRoot = &SkillDiscoveryRoot{ID: rootID, Path: rootPath}
		}
		out = append(out, skill)
	}
	return out
}

func parseSkillFrontmatterV2(contents string) (string, string) {
	text := strings.TrimPrefix(strings.ReplaceAll(contents, "\r\n", "\n"), "\ufeff")
	if !strings.HasPrefix(text, "---\n") {
		return "", ""
	}
	end := strings.Index(text[4:], "\n---")
	if end < 0 {
		return "", ""
	}
	var name, description string
	for _, line := range strings.Split(text[4:4+end], "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(strings.Trim(strings.TrimSpace(value), `"'`))
		switch strings.TrimSpace(key) {
		case "name":
			name = value
		case "description":
			description = value
		}
	}
	return name, description
}

func firstNonEmptyV2(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
