package integrity

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"codex_go/sandbox"
	"codex_go/sandbox/sandboxpath"

	"github.com/bmatcuk/doublestar/v4"
)

// ErrInvalidPolicy mirrors the io::ErrorKind::InvalidInput error upstream
// `prepare_policy` returns when a deny-read glob cannot be materialized
// (sandbox_integrity/policy.rs:24-28).
var ErrInvalidPolicy = errors.New("invalid sandbox integrity policy")

// PreparedPolicy mirrors Rust `sandbox_integrity::PreparedPolicy`
// (sandbox_integrity/policy.rs:13-16): the writable roots and glob read denials,
// resolved once per effective policy.
//
// Upstream takes a `FileSystemSandboxPolicy`; this port takes the policy's
// restricted entry list plus the cwd those entries resolve against, which is the
// Go carrier for the policy's resolved entries (see
// sandbox.ResolveFileSystemEntryPath). Upstream's `FileSystemSandboxKind` and
// `glob_scan_max_depth` have no entry-list equivalent here, and - exactly as
// upstream documents on `FileContentsChecker::new` - the caller handles
// full-disk write access separately.
type PreparedPolicy struct {
	// Roots mirrors `PreparedPolicy::roots`, i.e. upstream
	// `get_writable_roots_with_cwd` (permissions.rs:1483-1489). It is empty when
	// no entry grants write access.
	Roots []sandboxpath.WritableRoot
	// GlobDenials mirrors `PreparedPolicy::glob_denials`: nil when the policy
	// carries no deny-read glob, matching upstream's `Option<ReadDenyMatcher>`.
	GlobDenials *ReadDenyMatcher
}

// ReadDenyMatcher answers whether a resolved local path is still denied by the
// policy's deny-read globs, mirroring Rust `ReadDenyMatcher`
// (permissions.rs:316-321) as far as `sandbox_integrity` uses it.
//
// The port covers `try_new_for_local_paths` and `is_local_path_read_denied` for
// the glob-only policy `PreparePolicy` retains, matched against the executor's
// own local paths. Upstream's PathUri host projection, `~` expansion, and
// `is_local_path_read_denied_with_canonical_path` have no Go carrier yet and are
// not ported.
type ReadDenyMatcher struct {
	entries []sandbox.FileSystemSandboxEntry
	cwd     string
}

// IsLocalPathReadDenied mirrors Rust
// `ReadDenyMatcher::is_local_path_read_denied` (permissions.rs:400-416) for the
// glob-only policy PreparePolicy retains.
func (m *ReadDenyMatcher) IsLocalPathReadDenied(path string) bool {
	if m == nil || len(m.entries) == 0 {
		return false
	}
	return sandbox.ResolveFileSystemEntryAccess(m.entries, path, m.cwd) == sandbox.FileSystemAccessDeny
}

// IsLocalPathReadDeniedWithCanonical mirrors Rust's
// `ReadDenyMatcher::is_read_denied_with_canonical_path` (#41209): a walker may
// have already resolved the target, but the alias and the canonical location
// are both checked so denying either spelling denies the contents.
func (m *ReadDenyMatcher) IsLocalPathReadDeniedWithCanonical(path, canonicalPath string) bool {
	return m.IsLocalPathReadDenied(path) || m.IsLocalPathReadDenied(canonicalPath)
}

// PreparePolicy mirrors Rust `sandbox_integrity::prepare_policy`
// (sandbox_integrity/policy.rs:18-30).
//
// Writable roots already resolve precedence for exact-path rules, so only glob
// entries are kept for the read-deny matcher; there they are checked against the
// resolved contents instead of against the alias that names them.
func PreparePolicy(entries []sandbox.FileSystemSandboxEntry, cwd string) (*PreparedPolicy, error) {
	globEntries := make([]sandbox.FileSystemSandboxEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Path.Type != "glob_pattern" || entry.Access != sandbox.FileSystemAccessDeny {
			// Upstream keeps non-deny entries in glob_policy but its deny matcher
			// only consumes deny entries (permissions.rs:1185-1191).
			continue
		}
		pattern := entry.Path.Pattern
		if strings.HasPrefix(pattern, "~/") || strings.HasPrefix(pattern, `~\`) {
			// Rust #41209 resolves `~` against the executor home and fails
			// closed when it cannot (permissions.rs:1194-1210). Local integrity
			// checks run with the executor's own home.
			home, err := os.UserHomeDir()
			if err != nil || strings.TrimSpace(home) == "" {
				return nil, fmt.Errorf("%w: unable to resolve deny-read glob pattern %q without executor home", ErrInvalidPolicy, pattern)
			}
			entry.Path.Pattern = filepath.Join(home, pattern[2:])
			pattern = entry.Path.Pattern
		}
		materialized := materializeGlobPattern(pattern, cwd)
		if materialized == "" || !doublestar.ValidatePattern(filepath.ToSlash(materialized)) {
			return nil, fmt.Errorf("%w: invalid deny-read glob pattern %q", ErrInvalidPolicy, pattern)
		}
		globEntries = append(globEntries, entry)
	}
	var globDenials *ReadDenyMatcher
	if len(globEntries) > 0 {
		globDenials = &ReadDenyMatcher{entries: globEntries, cwd: cwd}
	}
	roots := writableRoots(entries, cwd)
	return &PreparedPolicy{Roots: roots, GlobDenials: globDenials}, nil
}

// materializeGlobPattern resolves a deny-read glob against cwd the same way the
// policy matcher resolves it (sandbox/policy_transforms.go
// `materializePermissionGlob`).
func materializeGlobPattern(pattern string, cwd string) string {
	trimmed := strings.TrimSpace(pattern)
	if trimmed == "" {
		return ""
	}
	if filepath.IsAbs(trimmed) || strings.TrimSpace(cwd) == "" {
		return filepath.Clean(trimmed)
	}
	return filepath.Clean(filepath.Join(cwd, trimmed))
}

// writableRoots mirrors the parts of upstream `get_writable_roots_with_cwd`
// (permissions.rs:1549-1760) that a restricted entry list decides:
//
//   - a write entry is a writable root only while the resolved access at its own
//     path is still a write (permissions.rs:1574-1587);
//   - narrower non-write entries carve out broader writable roots, and a default
//     carveout yields to an explicit entry for the same resolved path
//     (permissions.rs:1687, 1696-1746);
//   - the protected workspace metadata names come from the policy
//     (permissions.rs:1681-1682, 2526-2549).
//
// Upstream's PathUri alias handling, raw carveout preservation for nested
// symlinks, `RootMetadataWriteMounts`, and the cwd-dependent `.codex` carveout
// (`protect_missing_dot_codex`) are not ported; see the package evidence file.
func writableRoots(entries []sandbox.FileSystemSandboxEntry, cwd string) []sandboxpath.WritableRoot {
	var writablePaths []string
	seen := map[string]bool{}
	for _, entry := range entries {
		if entry.Access != sandbox.FileSystemAccessWrite || entry.Path.Type == "glob_pattern" {
			continue
		}
		path := sandbox.ResolveFileSystemEntryPath(entry.Path, cwd)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		// A write entry a narrower rule has already overridden is not a writable
		// root (upstream filters entries through can_write_path).
		if sandbox.ResolveFileSystemEntryAccess(entries, path, cwd) != sandbox.FileSystemAccessWrite {
			continue
		}
		writablePaths = append(writablePaths, path)
	}
	if len(writablePaths) == 0 {
		return nil
	}
	explicitPaths := resolvedEntryPaths(entries, cwd)
	carveouts := explicitCarveouts(entries, cwd)
	roots := sandboxpath.WritableRootsWithProtectedSubpaths(writablePaths)
	for index := range roots {
		root := &roots[index]
		root.ProtectedMetadataNames = protectedMetadataNames(entries, root.Root, cwd)
		root.ReadOnlySubpaths = dropListedPaths(root.ReadOnlySubpaths, explicitPaths)
		for _, carveout := range carveouts {
			if carveout == root.Root || !sandboxpath.PathWithin(carveout, root.Root) {
				continue
			}
			if !containsString(root.ReadOnlySubpaths, carveout) {
				root.ReadOnlySubpaths = append(root.ReadOnlySubpaths, carveout)
			}
		}
	}
	return roots
}

// resolvedEntryPaths mirrors upstream `resolved_entries_with_cwd` for concrete
// entries: every entry path resolved against cwd, deduplicated in entry order.
func resolvedEntryPaths(entries []sandbox.FileSystemSandboxEntry, cwd string) []string {
	var paths []string
	seen := map[string]bool{}
	for _, entry := range entries {
		if entry.Path.Type == "glob_pattern" {
			continue
		}
		path := sandbox.ResolveFileSystemEntryPath(entry.Path, cwd)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		paths = append(paths, path)
	}
	return paths
}

// explicitCarveouts returns the resolved paths of the non-write entries that
// still narrow a broader grant (upstream permissions.rs:1696-1744).
func explicitCarveouts(entries []sandbox.FileSystemSandboxEntry, cwd string) []string {
	var carveouts []string
	seen := map[string]bool{}
	for _, entry := range entries {
		if entry.Access == sandbox.FileSystemAccessWrite || entry.Path.Type == "glob_pattern" {
			continue
		}
		path := sandbox.ResolveFileSystemEntryPath(entry.Path, cwd)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		if sandbox.ResolveFileSystemEntryAccess(entries, path, cwd) == sandbox.FileSystemAccessWrite {
			continue
		}
		carveouts = append(carveouts, path)
	}
	return carveouts
}

// protectedMetadataNames mirrors upstream
// `protected_metadata_names_for_writable_root` (permissions.rs:2526-2549): a
// workspace metadata name stays protected under the root until the policy grants
// a write rule for that metadata path itself.
func protectedMetadataNames(entries []sandbox.FileSystemSandboxEntry, root string, cwd string) []string {
	var protected []string
	for _, name := range sandboxpath.DefaultProtectedMetadataNames() {
		metadataPath := filepath.Join(root, name)
		granted := false
		for _, entry := range entries {
			if entry.Access != sandbox.FileSystemAccessWrite || entry.Path.Type == "glob_pattern" {
				continue
			}
			resolved := sandbox.ResolveFileSystemEntryPath(entry.Path, cwd)
			if resolved != "" && sandboxpath.PathWithin(resolved, metadataPath) {
				granted = true
				break
			}
		}
		if !granted {
			protected = append(protected, name)
		}
	}
	return protected
}

// dropListedPaths removes the paths an explicit entry already governs, matching
// upstream `has_explicit_resolved_path_entry` (permissions.rs:2519-2524).
func dropListedPaths(paths []string, listed []string) []string {
	if len(paths) == 0 || len(listed) == 0 {
		return paths
	}
	dropped := make([]string, 0, len(paths))
	for _, path := range paths {
		if containsString(listed, path) {
			continue
		}
		dropped = append(dropped, path)
	}
	return dropped
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
