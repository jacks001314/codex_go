package sandbox

import "sort"

// Rust parity: codex-rs/protocol/src/permissions.rs
// (`FileSystemSandboxPolicy::get_unreadable_roots_with_cwd` and
// `get_unreadable_globs_with_cwd`).
//
// These enumerate the restrictions a resolved profile still enforces, which the
// Guardian permission context reports as evidence. They never resolve or relax a
// restriction, and an unrestricted (or disabled) profile has none.

// UnreadableRootsWithCWD mirrors Rust's `get_unreadable_roots_with_cwd`: the
// deny entries a restricted profile still cannot read with this cwd. Entries a
// more specific rule makes readable are dropped, and the filesystem root itself
// is never materialized - restricted policies already deny reads outside their
// allow roots, so listing the root would erase narrower readable carveouts when
// downstream sandboxes apply deny masks last.
func UnreadableRootsWithCWD(profile *PermissionProfile, cwd string) []string {
	wire := rustPermissionProfileWireFromPermissionProfile(profile)
	if wire.Type != "managed" || wire.FileSys == nil || wire.FileSys.Type != "restricted" {
		return nil
	}
	root := rootPathForCWD(cwd)
	seen := map[string]bool{}
	var roots []string
	for _, entry := range wire.FileSys.Entries {
		if entry.Access != string(FileSystemAccessDeny) || entry.Path.Type == "glob_pattern" {
			continue
		}
		path := entry.Path.resolvedRuntimePath(cwd)
		if path == "" || path == root || seen[path] {
			continue
		}
		if wire.canReadPathWithCWD(path, cwd) {
			// A readable carveout keeps this deny entry from being unreadable.
			continue
		}
		seen[path] = true
		roots = append(roots, path)
	}
	return roots
}

// UnreadableGlobsWithCWD mirrors Rust's `get_unreadable_globs_with_cwd`: every
// deny glob pattern resolved against the cwd, sorted and deduplicated. Rust
// excludes concrete paths and special paths here, and so does this.
func UnreadableGlobsWithCWD(profile *PermissionProfile, cwd string) []string {
	wire := rustPermissionProfileWireFromPermissionProfile(profile)
	if wire.Type != "managed" || wire.FileSys == nil || wire.FileSys.Type != "restricted" {
		return nil
	}
	var patterns []string
	for _, entry := range wire.FileSys.Entries {
		if entry.Access != string(FileSystemAccessDeny) || entry.Path.Type != "glob_pattern" {
			continue
		}
		pattern := cleanRunPathWithCWD(entry.Path.Pattern, cwd)
		if pattern == "" {
			continue
		}
		patterns = append(patterns, pattern)
	}
	sort.Strings(patterns)
	return dedupeSortedStrings(patterns)
}

func dedupeSortedStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}
