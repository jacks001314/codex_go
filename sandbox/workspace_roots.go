package sandbox

import (
	"fmt"
	"path/filepath"
	"strings"

	json "github.com/goccy/go-json"

	"codex_go/utils"
)

const workspaceRootsGlobPrefix = "codex-project-roots://"

// MaterializePermissionProfileWorkspaceRoots binds symbolic workspace rules to
// the roots of the host that will enforce the policy. A nil roots slice means
// the profile is already resolved; a non-nil empty slice means the launch has no
// workspace access and must not fall back to the policy cwd.
func MaterializePermissionProfileWorkspaceRoots(profile *PermissionProfile, policyCWD string, workspaceRoots []string) (*PermissionProfile, error) {
	if profile == nil {
		readOnly := ReadOnlyPermissionProfile()
		profile = &readOnly
	}
	cloned := clonePermissionProfile(profile)
	if workspaceRoots == nil {
		return &cloned, nil
	}
	if _, err := nativeLaunchPath(policyCWD, "sandbox policy cwd"); err != nil {
		return nil, err
	}
	nativeRoots := make([]string, 0, len(workspaceRoots))
	for _, root := range workspaceRoots {
		resolved, err := nativeLaunchPath(root, "sandbox workspace root")
		if err != nil {
			return nil, err
		}
		nativeRoots = append(nativeRoots, resolved)
	}
	if cloned.Disabled {
		return &cloned, nil
	}
	raw, err := RuntimePermissionProfileJSON(cloned)
	if err != nil {
		return nil, err
	}
	wire, err := parseRustPermissionProfileWireJSON([]byte(raw))
	if err != nil {
		return nil, err
	}
	if wire.Type != "managed" || wire.FileSys == nil || strings.EqualFold(wire.FileSys.Type, "unrestricted") {
		return &cloned, nil
	}
	entries, ok, err := materializeWorkspaceRootEntries(wire.FileSys.Entries, nativeRoots)
	if err != nil {
		return nil, err
	}
	if !ok {
		wire.FileSys = &rustPermissionFilesystem{Type: "restricted", Entries: []rustPermissionFilesystemEntry{}}
	} else {
		wire.FileSys.Entries = entries
	}
	encoded, err := json.Marshal(wire)
	if err != nil {
		return nil, err
	}
	materialized, err := canonicalPermissionProfileFromJSON(encoded)
	if err != nil {
		return nil, err
	}
	return &materialized, nil
}

func materializeWorkspaceRootEntries(entries []rustPermissionFilesystemEntry, roots []string) ([]rustPermissionFilesystemEntry, bool, error) {
	convention := utils.NativePathConvention()
	out := make([]rustPermissionFilesystemEntry, 0, len(entries)+len(roots))
	for _, entry := range entries {
		if entry.Path.Type == "path" || entry.Path.Type == "" {
			if _, err := nativeLaunchPath(entry.Path.Path, "sandbox permission path"); err != nil {
				return nil, false, err
			}
			out = append(out, entry)
			continue
		}
		subpath, isGlob := "", false
		switch entry.Path.Type {
		case "special":
			if !strings.EqualFold(entry.Path.Value.Kind, "project_roots") &&
				!strings.EqualFold(entry.Path.Value.Kind, "current_working_directory") {
				out = append(out, entry)
				continue
			}
			if entry.Path.Value.Subpath != nil {
				subpath = *entry.Path.Value.Subpath
			}
		case "glob_pattern":
			if !strings.HasPrefix(entry.Path.Pattern, workspaceRootsGlobPrefix) {
				out = append(out, entry)
				continue
			}
			subpath = strings.TrimPrefix(entry.Path.Pattern, workspaceRootsGlobPrefix)
			isGlob = true
		default:
			out = append(out, entry)
			continue
		}
		if entry.Access == string(FileSystemAccessDeny) && subpath != "" {
			if _, homeRelative := convention.HomeRelativeSuffix(subpath); homeRelative {
				// This legacy rule previously named a home path outside the
				// workspace. Without the executor home, denying only the
				// workspace would weaken it, so clear the filesystem grants.
				return nil, false, nil
			}
		}
		for _, root := range roots {
			if isGlob {
				uri, ok := utils.InferredPathURI(root)
				if !ok || uri.ValidateGlobDirectory(convention) != nil {
					if entry.Access == string(FileSystemAccessWrite) {
						return nil, false, nil
					}
					out = append(out, rustPermissionFilesystemEntry{
						Path:   rustPermissionFilesystemPath{Type: "path", Path: root},
						Access: string(FileSystemAccessDeny),
					})
					continue
				}
			}
			path, valid := resolveScopedWorkspaceLaunchPath(root, subpath, convention)
			if !valid {
				if entry.Access == string(FileSystemAccessWrite) {
					return nil, false, nil
				}
				out = append(out, rustPermissionFilesystemEntry{
					Path:   rustPermissionFilesystemPath{Type: "path", Path: root},
					Access: string(FileSystemAccessDeny),
				})
				continue
			}
			pathEntry := rustPermissionFilesystemPath{Type: "path", Path: path}
			if isGlob {
				pathEntry = rustPermissionFilesystemPath{Type: "glob_pattern", Pattern: path}
			}
			out = append(out, rustPermissionFilesystemEntry{Path: pathEntry, Access: entry.Access})
		}
	}
	return out, true, nil
}

func nativeLaunchPath(raw string, field string) (string, error) {
	raw = strings.TrimSpace(raw)
	uri, ok := utils.InferredPathURI(raw)
	if !ok {
		return "", fmt.Errorf("invalid %s: path must be absolute", field)
	}
	convention, ok := uri.InferConvention()
	if !ok || convention != utils.NativePathConvention() {
		return "", fmt.Errorf("invalid %s: path is not native to this host", field)
	}
	rendered, err := utils.LegacyAppPathStringFromURI(uri, convention)
	if err != nil {
		return "", fmt.Errorf("invalid %s: %w", field, err)
	}
	return utils.LexicalClean(rendered.Value, convention), nil
}

func resolveScopedWorkspaceLaunchPath(root string, subpath string, convention utils.PathConvention) (string, bool) {
	if subpath == "" {
		return utils.LexicalClean(root, convention), true
	}
	if strings.HasPrefix(subpath, "/") || convention == utils.ConventionWindows && strings.HasPrefix(subpath, `\`) {
		return "", false
	}
	for _, segment := range utils.PathSegments(convention, subpath) {
		if segment == "." || segment == ".." || convention == utils.ConventionWindows && strings.Contains(segment, ":") {
			return "", false
		}
	}
	path := utils.LexicalClean(filepath.Join(root, subpath), convention)
	if !sameOrWithin(path, root) {
		return "", false
	}
	return path, true
}
