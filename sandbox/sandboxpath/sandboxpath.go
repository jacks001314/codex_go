// Package sandboxpath holds the filesystem-policy view and the pre-sandbox
// executable discovery rule that the outer permission layer (`sandbox`) and the
// Linux sandbox helper (`sandbox/linuxsandbox`) share.
//
// The helper cannot import package sandbox - that package imports the helper to
// launch it - so the shared types live in this leaf package instead (Rust
// codex-sandboxing/src/bwrap.rs `find_pre_sandbox_executable_in_path` plus
// protocol::WritableRoot).
package sandboxpath

import (
	"os/exec"
	"path/filepath"
	"strings"
)

// WritableRoot mirrors Rust `protocol::WritableRoot`: a writable root with the
// subpaths that stay read-only even when the root is writable, plus the
// workspace metadata names that must not be created or replaced under it.
type WritableRoot struct {
	Root                   string
	ReadOnlySubpaths       []string
	ProtectedMetadataNames []string
}

// IsPathWritable mirrors Rust `WritableRoot::is_path_writable`: the path is
// under the root, outside every read-only carveout, and does not introduce a
// protected metadata name.
func (r *WritableRoot) IsPathWritable(path string) bool {
	if r == nil {
		return false
	}
	root := cleanAbsolute(r.Root)
	target := cleanAbsolute(path)
	if !PathWithin(target, root) {
		return false
	}
	for _, subpath := range r.ReadOnlySubpaths {
		if PathWithin(target, cleanAbsolute(subpath)) {
			return false
		}
	}
	if r.PathContainsProtectedMetadataName(target) {
		return false
	}
	return true
}

// PathContainsProtectedMetadataName mirrors Rust
// `WritableRoot::path_contains_protected_metadata_name`: the first component of
// the path below the root is one of the metadata names.
func (r *WritableRoot) PathContainsProtectedMetadataName(path string) bool {
	if r == nil {
		return false
	}
	root := cleanAbsolute(r.Root)
	target := cleanAbsolute(path)
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == "." || strings.HasPrefix(relative, "..") {
		return false
	}
	first := relative
	if index := strings.IndexAny(relative, `/\`); index >= 0 {
		first = relative[:index]
	}
	for _, name := range r.ProtectedMetadataNames {
		if first == name {
			return true
		}
	}
	return false
}

// DefaultProtectedMetadataNames are the workspace metadata names a writable root
// keeps read-only unless the policy grants an explicit write rule.
func DefaultProtectedMetadataNames() []string {
	return []string{".git", ".agents", ".gcode", ".aws"}
}

// ProtectedSubpaths mirrors the read-only carveouts a writable root carries by
// default.
func ProtectedSubpaths(root string) []string {
	return []string{
		filepath.Join(root, ".git"),
		filepath.Join(root, ".agents"),
		filepath.Join(root, ".gcode"),
		// AWS profiles can select credential helpers the application executes,
		// so a writable root keeps `.aws` protected by default (Rust #48176).
		filepath.Join(root, ".aws"),
	}
}

// WritableRootsWithProtectedSubpaths builds the writable-root set for a list of
// paths, mirroring the roots the sandbox mounts.
func WritableRootsWithProtectedSubpaths(paths []string) []WritableRoot {
	seen := map[string]bool{}
	var out []WritableRoot
	for _, path := range paths {
		path = cleanAbsolute(path)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, WritableRoot{
			Root:                   path,
			ReadOnlySubpaths:       ProtectedSubpaths(path),
			ProtectedMetadataNames: DefaultProtectedMetadataNames(),
		})
	}
	return out
}

// FilesystemPolicy is the resolved filesystem-policy view the pre-sandbox PATH
// filter needs (Rust FileSystemSandboxPolicy).
type FilesystemPolicy struct {
	// FullDiskWriteAccess mirrors
	// `FileSystemSandboxPolicy::has_full_disk_write_access`: writes are
	// unrestricted, so every path counts as sandbox-writable.
	FullDiskWriteAccess bool
	// WritableRoots mirrors
	// `get_writable_roots_with_cwd_inheriting_root_metadata`, which returns no
	// roots at all for a full-disk-write policy.
	WritableRoots []WritableRoot
}

// FindExecutableInPath mirrors Rust
// `sandboxing::find_pre_sandbox_executable_in_path` (Rust #51211): the first
// canonical PATH candidate for `program` that the filesystem policy cannot
// replace.
//
// A candidate is refused when it sits under a non-root process cwd, when the
// policy may write it and the current user can modify it, or when such an
// ancestor directory exists. Read-only carveouts are bind mounts that cannot be
// renamed themselves, so the walk keeps going above them. Host-protected
// installations such as `/usr/bin/bwrap` stay usable even under a full-disk
// write policy, because the canonicalized candidate is outside every writable
// root and the current user cannot modify its ancestors.
func FindExecutableInPath(program string, pathEnv string, cwd string, policy FilesystemPolicy) string {
	return findExecutableInPath(program, pathEnv, cwd, policy, CurrentUserCanModify)
}

// findExecutableInPath is FindExecutableInPath with the effective-permission
// check injected so tests can pin the access-error classification.
func findExecutableInPath(program string, pathEnv string, cwd string, policy FilesystemPolicy, canModify func(string) bool) string {
	if strings.TrimSpace(program) == "" {
		return ""
	}
	canonicalCWD := CanonicalDirectory(cwd)
	cwdIsRoot := canonicalCWD == "" || filepath.Dir(canonicalCWD) == canonicalCWD
	roots := CanonicalWritableRoots(policy.WritableRoots)
	policyCanWrite := func(path string) bool {
		if policy.FullDiskWriteAccess {
			return true
		}
		for index := range roots {
			if roots[index].IsPathWritable(path) {
				return true
			}
		}
		return false
	}
	for _, dir := range filepath.SplitList(pathEnv) {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		resolved := CanonicalExecutable(filepath.Join(dir, program))
		if resolved == "" {
			continue
		}
		if !cwdIsRoot && canonicalCWD != "" && PathWithin(resolved, canonicalCWD) {
			continue
		}
		// A writable, user-modifiable candidate can be replaced between the
		// probe and the exec, so it is not a trustworthy launcher.
		if policyCanWrite(resolved) && canModify(resolved) {
			continue
		}
		if replaceableAncestor(resolved, roots, policyCanWrite, canModify) {
			continue
		}
		return resolved
	}
	return ""
}

// replaceableAncestor reports whether a parent directory of `path` can be
// renamed or unlinked by the current user through writes the policy allows,
// which would let an attacker redirect the installation path.
func replaceableAncestor(path string, roots []WritableRoot, policyCanWrite func(string) bool, canModify func(string) bool) bool {
	component := path
	for {
		parent := filepath.Dir(component)
		if parent == component {
			// Reached the filesystem root, which has no parent.
			return false
		}
		// Read-only carveouts are bind mounts and cannot themselves be
		// renamed/unlinked. Continue checking above them, since a mutable
		// ancestor could still redirect the installation path.
		if policyCanWrite(parent) && !isReadOnlyMount(component, roots) && canModify(parent) {
			return true
		}
		component = parent
	}
}

func isReadOnlyMount(path string, roots []WritableRoot) bool {
	for index := range roots {
		for _, subpath := range roots[index].ReadOnlySubpaths {
			if subpath == path {
				return true
			}
		}
	}
	return false
}

// CanonicalWritableRoots canonicalizes every root and re-anchors its read-only
// carveouts under the canonical root, exactly like the upstream filter does
// before comparing candidates (a symlinked writable root must still match its
// canonical children).
func CanonicalWritableRoots(roots []WritableRoot) []WritableRoot {
	out := make([]WritableRoot, 0, len(roots))
	for _, root := range roots {
		canonical := CanonicalDirectory(root.Root)
		if canonical == "" {
			out = append(out, root)
			continue
		}
		updated := WritableRoot{
			Root:                   canonical,
			ProtectedMetadataNames: root.ProtectedMetadataNames,
		}
		for _, subpath := range root.ReadOnlySubpaths {
			if relative, err := filepath.Rel(root.Root, subpath); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				updated.ReadOnlySubpaths = append(updated.ReadOnlySubpaths, filepath.Join(canonical, relative))
				continue
			}
			updated.ReadOnlySubpaths = append(updated.ReadOnlySubpaths, subpath)
		}
		out = append(out, updated)
	}
	return out
}

// CanonicalDirectory resolves symlinks and makes the path absolute, keeping the
// input when it cannot be resolved.
func CanonicalDirectory(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	resolved := path
	if evaluated, err := filepath.EvalSymlinks(path); err == nil {
		resolved = evaluated
	}
	absolute, err := filepath.Abs(resolved)
	if err != nil {
		return ""
	}
	return absolute
}

// CanonicalExecutable resolves a PATH candidate to its canonical executable
// path, or "" when it does not resolve to an executable.
func CanonicalExecutable(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return ""
	}
	found, err := exec.LookPath(resolved)
	if err != nil || strings.TrimSpace(found) == "" {
		return ""
	}
	absolute, err := filepath.Abs(resolved)
	if err != nil {
		return ""
	}
	return absolute
}

// PathWithin reports whether path equals root or lives below it.
func PathWithin(path string, root string) bool {
	if path == "" || root == "" {
		return false
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	if relative == "." {
		return true
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func cleanAbsolute(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	if absolute, err := filepath.Abs(path); err == nil {
		return filepath.Clean(absolute)
	}
	return filepath.Clean(path)
}
