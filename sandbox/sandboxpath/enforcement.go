package sandboxpath

// Rust parity: codex-rs/protocol/src/permissions/enforcement.rs (#59009).
//
// Every sandbox backend projects the same filesystem policy into grants,
// denials and glob masks. Resolving that projection once keeps the backends
// from drifting: the caller supplies only the parts that are genuinely
// backend-specific (how a special path resolves, whether deny globs expand
// into concrete masks, and how a writable root is decorated).

import (
	"sort"
	"strings"
)

// FileSystemPolicyProjection mirrors Rust's `FileSystemPolicyProjection`: how a
// backend binds filesystem roots after access and precedence are resolved.
type FileSystemPolicyProjection int

const (
	// ProjectionNative resolves roots and carveouts to their effective native
	// paths.
	ProjectionNative FileSystemPolicyProjection = iota
	// ProjectionPreserveMutableRoots normalizes trusted system aliases while
	// leaving mutable components for the sandbox to bind.
	ProjectionPreserveMutableRoots
	// ProjectionLinuxMounts inherits root metadata writes from the root mount
	// and retains literal paths for read denials.
	ProjectionLinuxMounts
)

// EnforcementEntry is one resolved filesystem entry of the policy. The caller
// resolves special paths (project roots, temp directories, the filesystem root)
// before building the request, so only concrete paths and deny glob patterns
// reach the shared resolver.
type EnforcementEntry struct {
	// Access is the entry's access mode: "read", "write" or "deny".
	Access string
	// Path is the resolved absolute path of a concrete entry.
	Path string
	// Pattern is a deny glob pattern. It is never set together with Path.
	Pattern string
}

// ResolvedFileSystemPermissions mirrors Rust's
// `ResolvedFileSystemPermissions`: filesystem permissions resolved on the host
// that will enforce them. Roots retain policy order and deny globs stay
// patterns for the sandbox to enforce or expand.
type ResolvedFileSystemPermissions struct {
	HasFullDiskReadAccess   bool
	HasFullDiskWriteAccess  bool
	IncludePlatformDefaults bool
	ReadableRoots           []string
	WritableRoots           []WritableRoot
	UnreadableRoots         []string
	UnreadableGlobs         []string
	GlobScanMaxDepth        *int
}

// EnforcementRequest describes one backend's view of the policy.
type EnforcementRequest struct {
	CWD                     string
	HasFullDiskReadAccess   bool
	HasFullDiskWriteAccess  bool
	IncludePlatformDefaults bool
	// PlatformDefaultReadRoots are the backend's well-known system read roots,
	// appended when the policy opts into platform defaults.
	PlatformDefaultReadRoots []string
	Entries                  []EnforcementEntry
	GlobScanMaxDepth         *int
	Projection               FileSystemPolicyProjection
	// Project maps a resolved grant path to the path this backend binds. Nil
	// keeps the resolved path.
	Project func(string) string
	// ExpandGlob turns a deny glob into concrete masks for backends that cannot
	// evaluate the glob syntax themselves (Linux mounts). When nil the patterns
	// stay in UnreadableGlobs for the backend to enforce directly (Seatbelt).
	ExpandGlob func(pattern string, maxDepth *int) ([]string, error)
	// CanRead reports whether a narrower readable grant reopens a deny path.
	// Nil means no carveouts are considered.
	CanRead func(string) bool
	// DecorateWritableRoots attaches read-only carveouts and protected metadata
	// names to the resolved writable roots.
	DecorateWritableRoots func([]string) []WritableRoot
}

// ResolveForEnforcement collects the grants, denials, glob patterns and
// platform defaults one sandbox backend enforces. A fully writable policy
// binds no roots at all but still reports its deny globs, because those stay
// mandatory masks (Rust #59009).
func ResolveForEnforcement(req EnforcementRequest) (ResolvedFileSystemPermissions, error) {
	resolved := ResolvedFileSystemPermissions{
		HasFullDiskReadAccess:  req.HasFullDiskReadAccess,
		HasFullDiskWriteAccess: req.HasFullDiskWriteAccess,
		GlobScanMaxDepth:       req.GlobScanMaxDepth,
	}
	resolved.IncludePlatformDefaults = req.IncludePlatformDefaults && !req.HasFullDiskReadAccess
	resolved.UnreadableGlobs = denyGlobPatterns(req.Entries)
	if req.HasFullDiskReadAccess && req.HasFullDiskWriteAccess {
		return resolved, nil
	}

	if !req.HasFullDiskReadAccess {
		resolved.ReadableRoots = readableRoots(req)
	}
	if !req.HasFullDiskWriteAccess {
		resolved.WritableRoots = writableRoots(req)
	}
	unreadable, err := unreadableRoots(req)
	if err != nil {
		return ResolvedFileSystemPermissions{}, err
	}
	resolved.UnreadableRoots = unreadable
	return resolved, nil
}

func denyGlobPatterns(entries []EnforcementEntry) []string {
	var patterns []string
	for _, entry := range entries {
		if !entryIsDeny(entry) || strings.TrimSpace(entry.Pattern) == "" {
			continue
		}
		patterns = append(patterns, entry.Pattern)
	}
	if len(patterns) == 0 {
		return nil
	}
	sort.Strings(patterns)
	out := patterns[:1]
	for _, pattern := range patterns[1:] {
		if pattern != out[len(out)-1] {
			out = append(out, pattern)
		}
	}
	return out
}

func readableRoots(req EnforcementRequest) []string {
	seen := map[string]bool{}
	var roots []string
	add := func(path string) {
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		roots = append(roots, path)
	}
	for _, entry := range req.Entries {
		if entryIsDeny(entry) || strings.TrimSpace(entry.Pattern) != "" {
			continue
		}
		add(projectPath(req, entry.Path))
	}
	if req.IncludePlatformDefaults {
		for _, root := range req.PlatformDefaultReadRoots {
			add(projectPath(req, root))
		}
	}
	return roots
}

func writableRoots(req EnforcementRequest) []WritableRoot {
	seen := map[string]bool{}
	var paths []string
	for _, entry := range req.Entries {
		if !strings.EqualFold(strings.TrimSpace(entry.Access), "write") {
			continue
		}
		if strings.TrimSpace(entry.Pattern) != "" {
			continue
		}
		path := projectPath(req, entry.Path)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		paths = append(paths, path)
	}
	if len(paths) == 0 {
		return nil
	}
	if req.DecorateWritableRoots == nil {
		out := make([]WritableRoot, 0, len(paths))
		for _, path := range paths {
			out = append(out, WritableRoot{Root: path})
		}
		return out
	}
	return req.DecorateWritableRoots(paths)
}

func unreadableRoots(req EnforcementRequest) ([]string, error) {
	seen := map[string]bool{}
	var roots []string
	add := func(path string) {
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		roots = append(roots, path)
	}
	for _, entry := range req.Entries {
		if !entryIsDeny(entry) {
			continue
		}
		if strings.TrimSpace(entry.Pattern) != "" {
			if req.ExpandGlob == nil {
				continue
			}
			matches, err := req.ExpandGlob(entry.Pattern, req.GlobScanMaxDepth)
			if err != nil {
				return nil, err
			}
			for _, match := range matches {
				add(projectPath(req, match))
			}
			continue
		}
		path := projectPath(req, entry.Path)
		if path == "" || isFilesystemRoot(path) {
			continue
		}
		if req.CanRead != nil && req.CanRead(path) {
			// A readable carveout keeps this deny entry from being unreadable.
			continue
		}
		add(path)
	}
	return roots, nil
}

func projectPath(req EnforcementRequest, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if req.Project == nil {
		return path
	}
	return req.Project(path)
}

func entryIsDeny(entry EnforcementEntry) bool {
	return strings.EqualFold(strings.TrimSpace(entry.Access), "deny")
}

func isFilesystemRoot(path string) bool {
	switch path {
	case "/", `\`:
		return true
	}
	// A Windows volume root such as `C:\` names the filesystem root too.
	trimmed := strings.TrimRight(path, `/\`)
	return len(trimmed) == 2 && trimmed[1] == ':'
}
