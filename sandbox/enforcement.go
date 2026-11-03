package sandbox

import (
	"os"
	pathpkg "path"
	"strings"

	"codex_go/sandbox/sandboxpath"
)

// Rust parity: codex-rs/protocol/src/permissions/enforcement.rs (#59009).
//
// `ResolveFileSystemPermissionsForEnforcement` is Go's
// `FileSystemSandboxPolicy::resolve_for_enforcement`: it resolves the profile's
// grants, denials, glob patterns and platform defaults once, so the macOS
// Seatbelt policy and the Linux mount policy consume the same projection
// instead of each re-deriving it.

// ResolveFileSystemPermissionsForEnforcement resolves the roots one sandbox
// backend enforces. Paths keep Seatbelt's POSIX spelling because the runtime
// profile is platform independent until a backend binds it.
func ResolveFileSystemPermissionsForEnforcement(profile *PermissionProfile, cwd string, projection sandboxpath.FileSystemPolicyProjection) (sandboxpath.ResolvedFileSystemPermissions, error) {
	wire := rustPermissionProfileWireFromPermissionProfile(profile)
	if profile == nil || profile.Disabled {
		return sandboxpath.ResolvedFileSystemPermissions{}, nil
	}
	policy := profile.LegacySandboxPolicy()
	req := sandboxpath.EnforcementRequest{
		CWD:                    cwd,
		HasFullDiskReadAccess:  policy.HasFullDiskReadAccess(),
		HasFullDiskWriteAccess: policy.HasFullDiskWriteAccess(),
		Entries:                enforcementEntries(profile, cwd),
		Projection:             projection,
		CanRead: func(path string) bool {
			return wire.canReadPathWithCWD(path, cwd)
		},
		DecorateWritableRoots: func(roots []string) []sandboxpath.WritableRoot {
			return sandboxpath.WritableRootsWithProtectedSubpaths(roots)
		},
	}
	resolved, err := sandboxpath.ResolveForEnforcement(req)
	if err != nil {
		return sandboxpath.ResolvedFileSystemPermissions{}, err
	}
	// A fully writable policy short-circuits above, but Seatbelt still enforces
	// explicit read denials layered over it.
	if resolved.HasFullDiskReadAccess && resolved.HasFullDiskWriteAccess {
		denials, err := explicitDenyRoots(req)
		if err != nil {
			return sandboxpath.ResolvedFileSystemPermissions{}, err
		}
		resolved.UnreadableRoots = denials
	}
	return resolved, nil
}

// explicitDenyRoots keeps the deny entries a fully writable policy still
// enforces: concrete paths the policy names outright.
func explicitDenyRoots(req sandboxpath.EnforcementRequest) ([]string, error) {
	var roots []string
	seen := map[string]bool{}
	for _, entry := range req.Entries {
		if !strings.EqualFold(strings.TrimSpace(entry.Access), "deny") || strings.TrimSpace(entry.Pattern) != "" {
			continue
		}
		if entry.Path == "" || isRootPathForPolicy(entry.Path) || seen[entry.Path] {
			continue
		}
		seen[entry.Path] = true
		roots = append(roots, entry.Path)
	}
	return roots, nil
}

func isRootPathForPolicy(path string) bool {
	trimmed := strings.TrimRight(path, `/\`)
	if trimmed == "" {
		return true
	}
	return len(trimmed) == 2 && trimmed[1] == ':'
}

// enforcementEntries resolves the profile's wire entries into concrete paths
// and glob patterns for the shared resolver.
func enforcementEntries(profile *PermissionProfile, cwd string) []sandboxpath.EnforcementEntry {
	wire := rustPermissionProfileWireFromPermissionProfile(profile)
	if wire.FileSys == nil || wire.FileSys.Type != "restricted" {
		return nil
	}
	var entries []sandboxpath.EnforcementEntry
	for _, entry := range wire.FileSys.Entries {
		if entry.Path.Type == "glob_pattern" {
			if pattern := seatbeltResolveGlobAgainstCWD(entry.Path.Pattern, cwd); pattern != "" {
				entries = append(entries, sandboxpath.EnforcementEntry{Access: entry.Access, Pattern: pattern})
			}
			continue
		}
		if path := enforcementEntryPath(entry.Path, cwd); path != "" {
			entries = append(entries, sandboxpath.EnforcementEntry{Access: entry.Access, Path: path})
		}
	}
	return entries
}

// enforcementEntryPath resolves one wire path with the runtime's POSIX
// spelling: Seatbelt evaluates absolute paths on the macOS host the policy was
// written for, and the relative/special forms mirror Rust's
// `AbsolutePathBuf` resolution.
func enforcementEntryPath(p rustPermissionFilesystemPath, cwd string) string {
	switch p.Type {
	case "path":
		return seatbeltPathWithCWD(p.Path, cwd)
	case "special":
		switch strings.ToLower(strings.TrimSpace(p.Value.Kind)) {
		case "root":
			if strings.HasPrefix(strings.TrimSpace(cwd), "/") {
				return "/"
			}
			return cleanSeatbeltPath(rootPathForCWD(cwd))
		case "project_roots", "current_working_directory":
			if p.Value.Subpath != nil && strings.TrimSpace(*p.Value.Subpath) != "" {
				return seatbeltPathWithCWD(*p.Value.Subpath, cwd)
			}
			return seatbeltPathWithCWD(cwd, "")
		case "slash_tmp":
			if slashTmp := slashTmpPath(); slashTmp != "" {
				return cleanSeatbeltPath(slashTmp)
			}
			return ""
		case "tmpdir":
			return cleanSeatbeltPath(os.TempDir())
		}
	}
	return ""
}

func seatbeltPathWithCWD(path string, cwd string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if !isSeatbeltAbsolutePath(path) && strings.TrimSpace(cwd) != "" {
		if strings.HasPrefix(cwd, "/") && !strings.Contains(path, `\`) {
			path = pathpkg.Join(cwd, path)
		} else {
			path = joinPathForHost(cwd, path)
		}
	}
	return cleanSeatbeltPath(path)
}

func joinPathForHost(root string, element string) string {
	if strings.HasPrefix(root, "/") {
		return pathpkg.Join(root, element)
	}
	return joinSeatbeltPath(root, element)
}
