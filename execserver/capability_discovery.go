package execserver

import (
	"fmt"
	"io/fs"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
)

const maxCapabilityDiscoveryRoots = 64

func discoverCapabilities(params *CapabilityDiscoveryParams) (*CapabilityDiscoveryResponse, error) {
	if params == nil || len(params.Roots) == 0 {
		return &CapabilityDiscoveryResponse{Manifests: []CapabilityManifest{}, Errors: []CapabilityDiscoveryError{}}, nil
	}
	if len(params.Roots) > maxCapabilityDiscoveryRoots {
		return nil, requestError(-32602, "capability discovery accepts at most 64 roots")
	}
	response := &CapabilityDiscoveryResponse{Manifests: []CapabilityManifest{}, Errors: []CapabilityDiscoveryError{}}
	for _, root := range params.Roots {
		path, err := capabilityRootPath(root.Path)
		if err != nil {
			response.Errors = append(response.Errors, CapabilityDiscoveryError{RootID: root.ID, Message: err.Error()})
			continue
		}
		count := 0
		err = filepath.WalkDir(path, func(current string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			rel, err := filepath.Rel(path, current)
			if err != nil {
				return err
			}
			if entry.IsDir() && rel != "." && len(strings.Split(filepath.ToSlash(rel), "/")) > 8 {
				return filepath.SkipDir
			}
			count++
			if count > 4096 {
				return fmt.Errorf("capability root exceeds 4096 entries")
			}
			if entry.IsDir() {
				return nil
			}
			normalized := filepath.ToSlash(rel)
			kind := ""
			if entry.Name() == "SKILL.md" {
				kind = "skill"
			}
			if normalized == ".codex-plugin/plugin.json" || strings.HasSuffix(normalized, "/.codex-plugin/plugin.json") {
				kind = "plugin"
			}
			if kind != "" {
				response.Manifests = append(response.Manifests, CapabilityManifest{RootID: root.ID, Kind: kind, Path: filepath.ToSlash(current)})
			}
			return nil
		})
		if err != nil {
			response.Errors = append(response.Errors, CapabilityDiscoveryError{RootID: root.ID, Message: err.Error()})
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

func capabilityRootPath(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "file" {
		return "", fmt.Errorf("root path must be an absolute file URI")
	}
	path := parsed.Path
	if parsed.Host != "" {
		path = "//" + parsed.Host + parsed.Path
	}
	if len(path) >= 3 && path[0] == '/' && path[2] == ':' {
		path = path[1:]
	}
	path = filepath.FromSlash(path)
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("root path must be an absolute file URI")
	}
	return filepath.Clean(path), nil
}
