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
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	normalizedTarget := target
	if value, ok := metadataCaseAlias(target, root); ok {
		normalizedTarget = value
	}
	for _, subpath := range r.ReadOnlySubpaths {
		normalizedSubpath := cleanAbsolute(subpath)
		if value, ok := metadataCaseAlias(normalizedSubpath, root); ok {
			normalizedSubpath = value
		}
		if PathWithin(normalizedTarget, normalizedSubpath) {
			return false
		}
	}
	if r.PathContainsProtectedMetadataName(normalizedTarget) {
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
	target, root = normalizePathForContainment(target, root)
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == "." || strings.HasPrefix(relative, "..") {
		return false
	}
	first := relative
	if index := strings.IndexAny(relative, `/\`); index >= 0 {
		first = relative[:index]
	}
	for _, name := range r.ProtectedMetadataNames {
		// Rust relies on Seatbelt to resolve case aliases in the kernel. Go
		// checks them directly so a missing `.git` cannot be created as `.GIT`
		// on case-insensitive platforms.
		if first == name || ((runtime.GOOS == "windows" || runtime.GOOS == "darwin") && strings.EqualFold(first, name)) {
			return true
		}
	}
	return false
}

// DefaultProtectedMetadataNames are the workspace metadata names a writable root
// keeps read-only unless the policy grants an explicit write rule. Rust's
// `PROTECTED_METADATA_PATH_NAMES` lists `.git`, `.agents`, `.codex`, `.aws`;
// `.gcode` stays as this port's own project metadata directory.
func DefaultProtectedMetadataNames() []string {
	return []string{".git", ".agents", ".codex", ".gcode", ".aws"}
}

// ProtectedSubpaths mirrors Rust
// `default_read_only_subpaths_for_writable_root(root, false)`: a writable root
// only carves out metadata that exists, because protected metadata names
// already keep missing paths read-only through PathContainsProtectedMetadataName.
func ProtectedSubpaths(root string) []string {
	return protectedSubpathsForWritableRoot(root, false)
}

// protectedSubpathsForWritableRoot mirrors Rust
// `default_read_only_subpaths_for_writable_root`: `.git` (plus the Git
// directory a `.git` pointer file names) is carved out when it exists at all,
// `.agents`/`.aws` when they are directories, and `.codex` when it is a
// directory or when protectMissingDotCodex marks the writable root as the
// session cwd. `.gcode` is this port's own project metadata directory, so it
// follows the `.codex` rule.
func protectedSubpathsForWritableRoot(root string, protectMissingDotCodex bool) []string {
	root = cleanAbsolute(root)
	if root == "" {
		return nil
	}
	var subpaths []string
	seen := map[string]bool{}
	add := func(path string) {
		path = cleanAbsolute(path)
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		subpaths = append(subpaths, path)
	}
	topLevelGit := filepath.Join(root, ".git")
	if info, err := os.Stat(topLevelGit); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
		// Worktrees and submodules point at a Git directory that may live under
		// another writable root, so the target is protected before the pointer
		// file itself (Rust #47974).
		if !info.IsDir() {
			if target, ok := GitDirPointerTarget(topLevelGit); ok {
				add(target)
			}
		}
		add(topLevelGit)
	}
	// Order mirrors Rust's default_read_only_subpaths_for_writable_root:
	// `.git`, `.agents`, `.codex`, `.aws`; the port's `.gcode` follows.
	if path := filepath.Join(root, ".agents"); directoryExists(path) {
		add(path)
	}
	for _, name := range []string{".codex", ".gcode"} {
		path := filepath.Join(root, name)
		if directoryExists(path) {
			add(path)
			continue
		}
		// Keep the workspace root's project metadata read-only even before the
		// directory exists, so first-time creation goes through the
		// protected-path approval flow.
		if protectMissingDotCodex {
			add(path)
		}
	}
	if path := filepath.Join(root, ".aws"); directoryExists(path) {
		add(path)
	}
	return subpaths
}

func directoryExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// WritableRootsWithProtectedSubpaths builds the writable-root set for a list of
// paths, mirroring the roots the sandbox mounts.
//
// Rust #47974 (`Preserve Git directory protections across writable roots`): a
// `<root>/.git` pointer file can name a Git directory that lives inside another
// writable root, so a broader grant would otherwise leave that metadata
// writable even though the pointer itself is protected. Each resolved Git
// directory is appended as a read-only carveout of whichever writable root
// contains it.
func WritableRootsWithProtectedSubpaths(paths []string) []WritableRoot {
	return WritableRootsWithProtectedSubpathsForCwd(paths, "")
}

// WritableRootsWithProtectedSubpathsForCwd builds the writable-root set the
// sandbox mounts, treating cwd as the session workspace root: Rust keeps a
// missing `<cwd>/.codex` protected (`protect_missing_dot_codex`) while other
// roots only protect metadata that exists.
func WritableRootsWithProtectedSubpathsForCwd(paths []string, cwd string) []WritableRoot {
	cwd = cleanAbsolute(cwd)
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
			ReadOnlySubpaths:       protectedSubpathsForWritableRoot(path, cwd != "" && path == cwd),
			ProtectedMetadataNames: DefaultProtectedMetadataNames(),
		})
	}
	addResolvedGitDirCarveouts(out)
	return out
}

// addResolvedGitDirCarveouts appends the Git directory named by each writable
// root's `.git` pointer file to the read-only carveouts of every writable root
// that contains it (Rust #47974).
func addResolvedGitDirCarveouts(roots []WritableRoot) {
	for source := range roots {
		target, ok := GitDirPointerTarget(filepath.Join(roots[source].Root, ".git"))
		if !ok {
			continue
		}
		// Follow symlink aliases so the carveout is anchored at the real path
		// (Rust canonicalizes the resolved exclusion target).
		resolved, err := filepath.EvalSymlinks(target)
		if err != nil {
			continue
		}
		resolved = cleanAbsolute(resolved)
		if resolved == "" || isExplicitWritableRoot(roots, resolved) {
			continue
		}
		for other := range roots {
			if resolved == roots[other].Root || !PathWithin(resolved, roots[other].Root) {
				continue
			}
			if !containsSubpath(roots[other].ReadOnlySubpaths, resolved) {
				roots[other].ReadOnlySubpaths = append(roots[other].ReadOnlySubpaths, resolved)
			}
		}
	}
}

// GitDirPointerTarget mirrors Rust `resolve_gitdir_from_file`
// (codex-rs/protocol/src/permissions.rs): when `dotGit` is a `gitdir: <path>`
// pointer file, report the Git directory it names, resolving a relative target
// against the pointer's directory. It reports false for a directory, a file
// that is not a `gitdir:` pointer, an empty target, or a missing target.
func GitDirPointerTarget(dotGit string) (string, bool) {
	info, err := os.Stat(dotGit)
	if err != nil || info.IsDir() {
		return "", false
	}
	contents, err := os.ReadFile(dotGit)
	if err != nil {
		return "", false
	}
	prefix, rest, ok := strings.Cut(strings.TrimSpace(string(contents)), ":")
	if !ok || strings.TrimSpace(prefix) != "gitdir" {
		return "", false
	}
	raw := strings.TrimSpace(rest)
	if raw == "" {
		return "", false
	}
	target := filepath.Clean(raw)
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(dotGit), target)
	}
	if _, err := os.Stat(target); err != nil {
		return "", false
	}
	return target, true
}

// isExplicitWritableRoot mirrors Rust `has_explicit_resolved_path_entry`: an
// explicit write entry keeps winning over the derived Git directory carveout.
func isExplicitWritableRoot(roots []WritableRoot, path string) bool {
	for index := range roots {
		if roots[index].Root == path {
			return true
		}
	}
	return false
}

// containsSubpath reports whether subpaths already contains path.
func containsSubpath(subpaths []string, path string) bool {
	for _, subpath := range subpaths {
		if subpath == path {
			return true
		}
	}
	return false
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
	fileIsWritable := func(path string) bool {
		if !policyCanWrite(path) || !canModify(path) {
			return false
		}
		info, err := os.Lstat(path)
		return err != nil || info.Mode()&os.ModeSymlink == 0
	}
	pathIsReplaceable := func(path string) bool {
		return replaceableAncestor(path, roots, policyCanWrite, canModify)
	}
	for _, dir := range filepath.SplitList(pathEnv) {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		dir = strings.TrimSpace(dir)
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(cwd, dir)
		}
		lookup := program
		if !strings.ContainsAny(program, `/\`) && !strings.HasPrefix(program, "~") {
			lookup = filepath.Join(dir, program)
		} else if !filepath.IsAbs(lookup) {
			lookup = filepath.Join(cwd, lookup)
		}
		if !filepath.IsAbs(lookup) || pathIsReplaceable(lookup) {
			continue
		}
		if resolvedParent := CanonicalDirectory(filepath.Dir(lookup)); resolvedParent != "" {
			alias := filepath.Join(resolvedParent, filepath.Base(lookup))
			if (!cwdIsRoot && canonicalCWD != "" && PathWithin(alias, canonicalCWD)) ||
				fileIsWritable(alias) ||
				pathIsReplaceable(alias) {
				continue
			}
		}
		resolved := CanonicalExecutable(lookup)
		if resolved == "" {
			continue
		}
		if !cwdIsRoot && canonicalCWD != "" && PathWithin(resolved, canonicalCWD) {
			continue
		}
		// A writable, user-modifiable candidate can be replaced between the
		// probe and the exec, so it is not a trustworthy launcher.
		if fileIsWritable(resolved) {
			continue
		}
		if pathIsReplaceable(resolved) {
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
		canonicalParent := CanonicalDirectory(parent)
		if canonicalParent == "" {
			return true
		}
		alias := filepath.Join(canonicalParent, filepath.Base(component))
		if policyCanWrite(canonicalParent) && !isReadOnlyMount(alias, roots) &&
			canReplaceChild(canonicalParent, alias, canModify) {
			return true
		}
		component = parent
	}
}

func canReplaceChild(parent string, child string, canModify func(string) bool) bool {
	if !canModify(parent) {
		return false
	}
	return currentUserCanReplaceChild(parent, child)
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
	path, root = normalizePathForContainment(path, root)
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
