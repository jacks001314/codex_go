package integrity

import (
	"errors"
	"os"
	"path/filepath"

	"codex_go/sandbox"
	"codex_go/sandbox/sandboxpath"
)

// ErrTargetNotAFile mirrors the io::ErrorKind::InvalidInput error upstream
// returns for a containment dependency that exists but is not a file
// (sandbox_integrity/contents.rs:58-64). Upstream carries no path in the
// message either, so this stays byte-identical to the upstream string.
var ErrTargetNotAFile = errors.New("containment dependency is not a file")

// FileContentsChecker mirrors Rust `sandbox_integrity::FileContentsChecker`
// (sandbox_integrity/contents.rs:13-16): policy access to an existing
// containment dependency's contents. It inspects current objects and does not
// assess ownership or OS ACLs.
type FileContentsChecker struct {
	policy *PreparedPolicy
}

// NewFileContentsChecker mirrors `FileContentsChecker::new`
// (sandbox_integrity/contents.rs:25-51).
//
// The caller passes the prepared executor policy: symbolic roots must already be
// materialized and any backend snapshot expansion of globs (including scan
// limits) must already be reflected in the entries. Callers must handle
// full-disk write access separately.
func NewFileContentsChecker(entries []sandbox.FileSystemSandboxEntry, cwd string) (*FileContentsChecker, error) {
	policy, err := PreparePolicy(entries, cwd)
	if err != nil {
		return nil, err
	}
	roots := make([]sandboxpath.WritableRoot, 0, len(policy.Roots))
	for _, root := range policy.Roots {
		resolvedRoot, found, err := resolveExistingPath(root.Root)
		if err != nil {
			return nil, err
		}
		if !found {
			// A writable root that no longer exists cannot be inspected.
			continue
		}
		info, err := os.Stat(resolvedRoot)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			// A root that is not a directory has no metadata names to protect.
			root.ProtectedMetadataNames = nil
		}
		// Resolve existing carveouts and protected metadata aliases while
		// retaining missing paths unchanged.
		joinedRoot := root.Root
		exclusions := make([]string, 0, len(root.ReadOnlySubpaths)+len(root.ProtectedMetadataNames))
		for _, path := range root.ReadOnlySubpaths {
			resolved, err := resolveExistingOrKeep(path)
			if err != nil {
				return nil, err
			}
			exclusions = append(exclusions, resolved)
		}
		for _, name := range root.ProtectedMetadataNames {
			resolved, err := resolveExistingOrKeep(filepath.Join(joinedRoot, name))
			if err != nil {
				return nil, err
			}
			exclusions = append(exclusions, resolved)
		}
		root.Root = resolvedRoot
		root.ReadOnlySubpaths = exclusions
		roots = append(roots, root)
	}
	policy.Roots = roots
	return &FileContentsChecker{policy: policy}, nil
}

// Check mirrors `FileContentsChecker::check`
// (sandbox_integrity/contents.rs:53-85): a missing target has no contents to
// mutate, a non-file target is an error, and a target whose resolved contents
// are denied by a deny-read glob is not reported - denying the alias does not
// establish protection of the resolved contents.
func (c *FileContentsChecker) Check(target string) (*Finding, error) {
	info, err := os.Stat(target)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, ErrTargetNotAFile
	}
	resolvedTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		return nil, err
	}
	if c.policy.GlobDenials.IsLocalPathReadDenied(resolvedTarget) {
		return nil, nil
	}
	writeRoots := make([]string, 0, len(c.policy.Roots))
	for index := range c.policy.Roots {
		if c.policy.Roots[index].IsPathWritable(resolvedTarget) {
			writeRoots = append(writeRoots, c.policy.Roots[index].Root)
		}
	}
	if len(writeRoots) == 0 {
		return nil, nil
	}
	return &Finding{
		Target:     target,
		WriteRoots: writeRoots,
		Details:    FindingDetails{ResolvedTarget: resolvedTarget},
	}, nil
}

// resolveExistingPath mirrors upstream `resolve_existing`
// (sandbox_integrity/contents.rs:87-93): resolve an existing object, report a
// missing path as absent, and propagate every other error.
func resolveExistingPath(path string) (string, bool, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, true, nil
	}
	if os.IsNotExist(err) {
		return "", false, nil
	}
	return "", false, err
}

// resolveExistingOrKeep mirrors upstream `resolve_existing(..).unwrap_or(path)`
// for a carveout: a missing carveout keeps its configured path.
func resolveExistingOrKeep(path string) (string, error) {
	resolved, found, err := resolveExistingPath(path)
	if err != nil {
		return "", err
	}
	if !found {
		return path, nil
	}
	return resolved, nil
}
