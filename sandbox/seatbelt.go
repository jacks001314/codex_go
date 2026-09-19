package sandbox

import (
	"fmt"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
)

const macOSSeatbeltExecutable = "/usr/bin/sandbox-exec"

type seatbeltParameter struct {
	Name  string
	Value string
}

func createSeatbeltCommandArgs(command []string, cwd string, profile *PermissionProfile, allowUnixSockets []string) ([]string, error) {
	if len(command) == 0 || strings.TrimSpace(command[0]) == "" {
		return nil, fmt.Errorf("%w: command is required", ErrInvalidSandboxRunRequest)
	}
	policy, parameters, err := buildSeatbeltPolicy(cwd, profile, allowUnixSockets)
	if err != nil {
		return nil, err
	}
	args := []string{macOSSeatbeltExecutable}
	for _, parameter := range parameters {
		args = append(args, "-D", parameter.Name+"="+parameter.Value)
	}
	args = append(args, "-p", policy, "/usr/bin/env", "CODEX_SANDBOX=seatbelt")
	return append(args, command...), nil
}

func buildSeatbeltPolicy(cwd string, profile *PermissionProfile, allowUnixSockets []string) (string, []seatbeltParameter, error) {
	if profile == nil || profile.Disabled {
		return "(version 1)\n(allow default)\n", nil, nil
	}
	policy := profile.LegacySandboxPolicy()
	lines := []string{"(version 1)", "(allow default)"}
	var parameters []seatbeltParameter
	unreadable := seatbeltDeniedReadPaths(profile.DeniedReadEntries)
	// Rust #46571: the implicit scratch grants and the writable roots share the
	// same exclusion set, so a protected path (a read-only subpath or protected
	// project metadata) is never reopened by a broader allowance.
	protectedPaths := seatbeltProtectedPaths(policy, cwd)
	scratchRoots := cleanAbsoluteSeatbeltPaths([]string{"/tmp", "/var/tmp", "/private/tmp", "/private/var/tmp"})
	var scratchExclusions []string
	for _, scratchRoot := range scratchRoots {
		for _, excluded := range append(append([]string(nil), unreadable...), protectedPaths...) {
			if seatbeltSubpathWithin(excluded, scratchRoot) {
				scratchExclusions = appendUniqueSeatbeltPath(scratchExclusions, excluded)
			}
		}
	}

	if !policy.HasFullDiskWriteAccess() {
		lines = append(lines, "(deny file-write*)", `(allow file-write-data (literal "/dev/null"))`)
		// macOS #40961: ordinary sandboxed processes keep system scratch
		// directories for compatibility, while denying them to filesystem
		// helpers (which use a separate restricted policy). These grants are
		// process-only and mirror Rust's MACOS_PROCESS_PLATFORM_DEFAULTS.
		for _, scratch := range scratchRoots {
			name := fmt.Sprintf("SCRATCH_%d", len(parameters))
			parameters = append(parameters, seatbeltParameter{Name: name, Value: scratch})
			lines = append(lines, fmt.Sprintf(`(allow file-read* file-test-existence file-write* (subpath (param "%s")))`, name))
		}
		// Rust #46571: route the scratch write grants through the normal exclusions
		// so they respect unreadable paths, read-only paths, and protected
		// metadata. Scratch reads stay governed by the global deny-read policy,
		// which Rust applies to the exclusion set as well.
		for _, excluded := range scratchExclusions {
			name := fmt.Sprintf("SCRATCH_EXCLUDED_%d", len(parameters))
			parameters = append(parameters, seatbeltParameter{Name: name, Value: excluded})
			lines = append(lines, fmt.Sprintf(`(deny file-write* (subpath (param "%s")))`, name))
		}
		for _, root := range policy.GetWritableRootsWithCWD(cwd) {
			name := fmt.Sprintf("WRITABLE_ROOT_%d", len(parameters))
			parameters = append(parameters, seatbeltParameter{Name: name, Value: cleanSeatbeltPath(root.Root)})
			lines = append(lines, fmt.Sprintf(`(allow file-write* (subpath (param "%s")))`, name))
			for _, protected := range append(append([]string(nil), root.ReadOnlySubpaths...), protectedMetadataPaths(root.Root, root.ProtectedMetadataNames)...) {
				protectedName := fmt.Sprintf("PROTECTED_WRITE_%d", len(parameters))
				parameters = append(parameters, seatbeltParameter{Name: protectedName, Value: cleanSeatbeltPath(protected)})
				lines = append(lines, fmt.Sprintf(`(deny file-write* (subpath (param "%s")))`, protectedName))
			}
		}
	}

	// Rust #46583: deny XPC service lookups so sandboxed commands cannot reach
	// privileged macOS services.
	lines = append(lines, `(deny mach-lookup (xpc-service-name-prefix ""))`)

	for _, denied := range unreadable {
		name := fmt.Sprintf("DENIED_READ_%d", len(parameters))
		parameters = append(parameters, seatbeltParameter{Name: name, Value: denied})
		lines = append(lines, fmt.Sprintf(`(deny file-read* (subpath (param "%s")))`, name))
	}

	if !profile.AllowsNetwork() {
		lines = append(lines, "(deny network*)", "(deny system-socket)")
	}
	for _, socket := range cleanAbsoluteSeatbeltPaths(allowUnixSockets) {
		name := fmt.Sprintf("UNIX_SOCKET_%d", len(parameters))
		parameters = append(parameters, seatbeltParameter{Name: name, Value: socket})
		lines = append(lines,
			"(allow system-socket (socket-domain AF_UNIX))",
			fmt.Sprintf(`(allow network-bind (local unix-socket (subpath (param "%s"))))`, name),
			fmt.Sprintf(`(allow network-outbound (remote unix-socket (subpath (param "%s"))))`, name),
		)
	}
	// Rust #46571: renaming an allowed ancestor relocates its protected
	// descendants past their pathname carveouts. Keep these unlink denies last so
	// no broader allowance can reopen the rename operation.
	for index, ancestor := range seatbeltProtectedAncestors(policy, cwd) {
		name := fmt.Sprintf("PROTECTED_ANCESTOR_%d", index)
		parameters = append(parameters, seatbeltParameter{Name: name, Value: ancestor})
		lines = append(lines, fmt.Sprintf(`(deny file-write-unlink (require-all (vnode-type DIRECTORY) (literal (param "%s"))))`, name))
	}
	return strings.Join(lines, "\n") + "\n", parameters, nil
}

// seatbeltProtectedPaths returns the protected paths (a writable root's
// read-only subpaths and protected project metadata paths) that a broader
// allowance must not reopen.
func seatbeltProtectedPaths(policy *SandboxPolicy, cwd string) []string {
	paths := []string{}
	for _, root := range policy.GetWritableRootsWithCWD(cwd) {
		for _, protected := range root.ReadOnlySubpaths {
			paths = appendUniqueSeatbeltPath(paths, cleanSeatbeltPath(protected))
		}
		for _, protected := range protectedMetadataPaths(root.Root, root.ProtectedMetadataNames) {
			paths = appendUniqueSeatbeltPath(paths, protected)
		}
	}
	return paths
}

// seatbeltProtectedAncestors returns the writable-root directories that contain
// a protected path. Unlinking one of them would relocate the protected
// descendants past their pathname carveouts (Rust #46571). A protected path that
// is a symlink protects both its logical entry and its resolved target.
func seatbeltProtectedAncestors(policy *SandboxPolicy, cwd string) []string {
	seen := map[string]bool{}
	ancestors := []string{}
	for _, root := range policy.GetWritableRootsWithCWD(cwd) {
		rootPath := cleanSeatbeltPath(root.Root)
		for _, protected := range root.ReadOnlySubpaths {
			logical := cleanSeatbeltPath(protected)
			paths := []string{logical}
			if resolved := seatbeltSymlinkResolver(logical); resolved != "" {
				paths = append(paths, resolved)
			}
			for _, protectedPath := range paths {
				ancestor := parentSeatbeltPath(protectedPath)
				for ancestor != "" && seatbeltSubpathWithin(ancestor, rootPath) {
					if !seen[ancestor] {
						seen[ancestor] = true
						ancestors = append(ancestors, ancestor)
					}
					ancestor = parentSeatbeltPath(ancestor)
				}
			}
		}
	}
	sort.Strings(ancestors)
	return ancestors
}

// seatbeltResolvedSymlinkPath returns the resolved target of a symlinked path
// when it differs from the logical path, mirroring Rust's
// `normalize_path_for_sandbox` resolution of top-level aliases (#46571).
// seatbeltSymlinkResolver indirection lets tests exercise the resolved-ancestor
// protection on hosts without symlink privileges.
var seatbeltSymlinkResolver = seatbeltResolvedSymlinkPath

func seatbeltResolvedSymlinkPath(path string) string {
	cleaned := cleanSeatbeltPath(path)
	if cleaned == "" {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		return ""
	}
	resolved = cleanSeatbeltPath(resolved)
	if resolved == "" || resolved == cleaned {
		return ""
	}
	return resolved
}

// parentSeatbeltPath returns the parent directory of an absolute seatbelt path.
func parentSeatbeltPath(value string) string {
	cleaned := cleanSeatbeltPath(value)
	if cleaned == "" || cleaned == "/" || cleaned == "." {
		return ""
	}
	if strings.HasPrefix(cleaned, "/") {
		parent := pathpkg.Dir(cleaned)
		if parent == "/" || parent == "." {
			return ""
		}
		return parent
	}
	parent := filepath.Dir(cleaned)
	if parent == cleaned {
		return ""
	}
	return parent
}

// seatbeltSubpathWithin reports whether path is root itself or below it.
func seatbeltSubpathWithin(path string, root string) bool {
	path = cleanSeatbeltPath(path)
	root = cleanSeatbeltPath(root)
	if path == "" || root == "" {
		return false
	}
	if path == root {
		return true
	}
	if strings.HasPrefix(root, "/") {
		return strings.HasPrefix(path, strings.TrimRight(root, "/")+"/")
	}
	return strings.HasPrefix(path, strings.TrimRight(root, `\/`)+string(filepath.Separator))
}

// appendUniqueSeatbeltPath appends a cleaned path once.
func appendUniqueSeatbeltPath(paths []string, value string) []string {
	value = cleanSeatbeltPath(value)
	if value == "" {
		return paths
	}
	for _, existing := range paths {
		if existing == value {
			return paths
		}
	}
	return append(paths, value)
}

func protectedMetadataPaths(root string, names []string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		if strings.TrimSpace(name) != "" {
			out = append(out, joinSeatbeltPath(root, name))
		}
	}
	return out
}

func seatbeltDeniedReadPaths(entries []FileSystemSandboxEntry) []string {
	var paths []string
	for _, entry := range entries {
		if entry.Access != FileSystemAccessDeny || entry.Path.Type == "glob_pattern" || entry.Path.Value != nil {
			continue
		}
		if path := strings.TrimSpace(entry.Path.Path); isSeatbeltAbsolutePath(path) {
			paths = append(paths, cleanSeatbeltPath(path))
		}
	}
	return cleanAbsoluteSeatbeltPaths(paths)
}

func cleanAbsoluteSeatbeltPaths(paths []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, path := range paths {
		if !isSeatbeltAbsolutePath(path) {
			continue
		}
		path = cleanSeatbeltPath(path)
		if !seen[path] {
			seen[path] = true
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out
}

func isSeatbeltAbsolutePath(path string) bool {
	path = strings.TrimSpace(path)
	return strings.HasPrefix(path, "/") || strings.HasPrefix(path, `\\`) || (len(path) >= 3 && path[1] == ':' && (path[2] == '/' || path[2] == '\\'))
}

func cleanSeatbeltPath(value string) string {
	if strings.HasPrefix(value, "/") {
		return pathpkg.Clean(value)
	}
	return filepath.Clean(value)
}

func joinSeatbeltPath(root string, element string) string {
	if strings.HasPrefix(root, "/") {
		return pathpkg.Join(root, element)
	}
	return filepath.Join(root, element)
}
