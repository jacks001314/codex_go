package sandbox

import (
	"strings"

	"codex_go/utils"
)

// SummarizePermissionProfile mirrors Rust #45852
// summarize_permission_profile. The summary is derived from the profile's
// runtime file-system entries so executor paths keep their own conventions
// instead of being projected into the controller's filesystem namespace.
func SummarizePermissionProfile(profile *PermissionProfile, cwd *utils.PathURI, workspaceRoots []*utils.PathURI) string {
	if profile == nil {
		return "read-only"
	}
	networkAccess := profile.AllowsNetwork()
	if profile.Disabled {
		return "danger-full-access"
	}
	if profile.SandboxPolicy != nil && profile.SandboxPolicy.Kind == SandboxModeExternalSandbox {
		return permissionSummaryWithNetwork("external-sandbox", networkAccess)
	}
	if profile.SandboxPolicy != nil && profile.SandboxPolicy.HasFullDiskWriteAccess() {
		// Rust returns before appending the network suffix for full-disk writes.
		if networkAccess {
			return "danger-full-access"
		}
		return "external-sandbox"
	}

	cwdURI, cwdNative := pathURIRenderings(cwd)
	var workspaceWritable, otherWrites, tmpdirWritable, slashTmpWritable bool
	for _, entry := range profile.RuntimeFileSystemEntries() {
		if entry.Access != FileSystemAccessWrite {
			continue
		}
		switch entry.Path.Type {
		case "path":
			if entry.Path.Path == cwdURI || entry.Path.Path == cwdNative {
				workspaceWritable = true
			} else {
				otherWrites = true
			}
		case "special":
			kind, hasSubpath, ok := fileSystemSpecialEntry(entry.Path)
			if !ok {
				continue
			}
			switch kind {
			case "project_roots", "current_working_directory":
				// An explicit subpath keeps the row custom even when it renders
				// as empty, matching Rust's Some(_) arm.
				if hasSubpath {
					otherWrites = true
				} else {
					workspaceWritable = true
				}
			case "root":
				otherWrites = true
			case "tmpdir":
				tmpdirWritable = true
			case "slash_tmp":
				slashTmpWritable = true
			}
		}
	}

	if workspaceWritable {
		entries := []string{"workdir"}
		if slashTmpWritable {
			entries = append(entries, "/tmp")
		}
		if tmpdirWritable {
			entries = append(entries, "$TMPDIR")
		}
		for _, root := range workspaceRoots {
			if root == nil || root.String() == cwdURI {
				continue
			}
			entries = append(entries, root.NativePathString())
		}
		return permissionSummaryWithNetwork("workspace-write ["+strings.Join(entries, ", ")+"]", networkAccess)
	}
	// `:slash_tmp` is a real writable directory only on POSIX executors.
	if otherWrites || tmpdirWritable || (pathConventionIsPosix(cwd) && slashTmpWritable) {
		return permissionSummaryWithNetwork("custom permissions", networkAccess)
	}
	return permissionSummaryWithNetwork("read-only", networkAccess)
}

func permissionSummaryWithNetwork(summary string, networkAccess bool) string {
	if !networkAccess {
		return summary
	}
	return summary + " (network access enabled)"
}

func pathURIRenderings(path *utils.PathURI) (uri string, native string) {
	if path == nil {
		return "", ""
	}
	return path.String(), path.NativePathString()
}

func pathConventionIsPosix(path *utils.PathURI) bool {
	if path == nil {
		return false
	}
	convention, ok := path.InferConvention()
	return ok && convention == utils.ConventionPosix
}

func fileSystemSpecialEntry(path FileSystemPath) (kind string, hasSubpath bool, ok bool) {
	if path.Value == nil || strings.TrimSpace(path.Value.Kind) == "" {
		return "", false, false
	}
	return strings.ToLower(strings.TrimSpace(path.Value.Kind)), path.Value.Subpath != nil, true
}
