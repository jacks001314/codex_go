package execserver

import (
	"fmt"
	"sort"
	"strings"
)

const maxCapabilityDiscoveryRoots = 128

func discoverCapabilities(params *CapabilityDiscoveryParams) (*CapabilityDiscoveryResponse, error) {
	if params == nil || len(params.Roots) == 0 {
		return &CapabilityDiscoveryResponse{Manifests: []CapabilityManifest{}, Errors: []CapabilityDiscoveryError{}}, nil
	}
	if len(params.Roots) > maxCapabilityDiscoveryRoots {
		return nil, requestError(-32602, "capability discovery accepts at most 64 roots")
	}
	response := &CapabilityDiscoveryResponse{Manifests: []CapabilityManifest{}, Errors: []CapabilityDiscoveryError{}}
	for _, root := range params.Roots {
		walk, err := walkPath(&FSWalkParams{
			Path: root.Path,
			Options: FSWalkOptions{
				MaxDepth:                6,
				MaxDirectories:          2000,
				MaxEntries:              20000,
				FollowDirectorySymlinks: true,
			},
			Sandbox: root.Sandbox,
		})
		if err != nil {
			response.Errors = append(response.Errors, CapabilityDiscoveryError{RootID: root.ID, Message: err.Error()})
			continue
		}
		for _, walkErr := range walk.Errors {
			response.Errors = append(response.Errors, CapabilityDiscoveryError{RootID: root.ID, Message: fmt.Sprintf("%s: %s", walkErr.Path, walkErr.Message)})
		}
		if walk.Truncated {
			response.Errors = append(response.Errors, CapabilityDiscoveryError{RootID: root.ID, Message: "capability root reached its traversal limit"})
		}
		for _, entry := range walk.Entries {
			if entry.Kind != "file" {
				continue
			}
			normalized := strings.ReplaceAll(entry.Path, "\\", "/")
			kind := ""
			if strings.HasSuffix(normalized, "/SKILL.md") {
				kind = "skill"
			}
			if normalized == ".codex-plugin/plugin.json" || strings.HasSuffix(normalized, "/.codex-plugin/plugin.json") {
				kind = "plugin"
			}
			if kind != "" {
				response.Manifests = append(response.Manifests, CapabilityManifest{RootID: root.ID, Kind: kind, Path: entry.Path})
			}
		}
	}
	sort.Slice(response.Manifests, func(i, j int) bool {
		if response.Manifests[i].RootID != response.Manifests[j].RootID {
			return response.Manifests[i].RootID < response.Manifests[j].RootID
		}
		if response.Manifests[i].Kind != response.Manifests[j].Kind {
			return response.Manifests[i].Kind < response.Manifests[j].Kind
		}
		return response.Manifests[i].Path < response.Manifests[j].Path
	})
	return response, nil
}
