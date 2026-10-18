package sandbox

import (
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"

	"codex_go/utils"

	"github.com/bmatcuk/doublestar/v4"
)

// IntersectPermissionProfiles restricts a client grant to permissions that
// were actually requested. CWD-dependent entries are materialized so stored
// grants cannot change meaning when reused from another working directory.
func IntersectPermissionProfiles(requested, granted RequestPermissionProfile, cwd string) RequestPermissionProfile {
	var result RequestPermissionProfile
	if permissionNetworkEnabled(requested.Network) && permissionNetworkEnabled(granted.Network) {
		enabled := true
		result.Network = &AdditionalNetworkPermissions{Enabled: &enabled}
	}
	if requested.FileSystem == nil {
		return result
	}

	requestedEntries := permissionFileSystemEntries(requested.FileSystem)
	grantedEntries := permissionFileSystemEntries(granted.FileSystem)
	accepted := make([]FileSystemSandboxEntry, 0, len(grantedEntries))
	for _, entry := range grantedEntries {
		if !grantedPermissionEntryWithinRequest(requestedEntries, entry, cwd) {
			continue
		}
		entry = materializePermissionEntry(entry, cwd)
		appendUniquePermissionEntry(&accepted, entry)
	}

	entries := append([]FileSystemSandboxEntry(nil), accepted...)
	requestedDenies := retainConstrainingPermissionDenies(requestedEntries, accepted, cwd, &entries)
	grantedDenies := retainConstrainingPermissionDenies(grantedEntries, accepted, cwd, &entries)
	if len(entries) == 0 {
		return result
	}
	result.FileSystem = &AdditionalFileSystemPermissions{
		Entries:          entries,
		GlobScanMaxDepth: mergePermissionGlobDepth(requestedDenies, requested.FileSystem.GlobScanMaxDepth, grantedDenies, permissionGlobDepth(granted.FileSystem)),
	}
	return result
}

func permissionNetworkEnabled(network *AdditionalNetworkPermissions) bool {
	return network != nil && network.Enabled != nil && *network.Enabled
}

func permissionFileSystemEntries(fileSystem *AdditionalFileSystemPermissions) []FileSystemSandboxEntry {
	if fileSystem == nil {
		return nil
	}
	entries := make([]FileSystemSandboxEntry, 0, len(fileSystem.Entries)+len(fileSystem.Read)+len(fileSystem.Write))
	for _, entry := range fileSystem.Entries {
		appendUniquePermissionEntry(&entries, clonePermissionEntry(entry))
	}
	for _, path := range fileSystem.Read {
		appendUniquePermissionEntry(&entries, FileSystemSandboxEntry{Path: FileSystemPath{Type: "path", Path: path}, Access: FileSystemAccessRead})
	}
	for _, path := range fileSystem.Write {
		appendUniquePermissionEntry(&entries, FileSystemSandboxEntry{Path: FileSystemPath{Type: "path", Path: path}, Access: FileSystemAccessWrite})
	}
	return entries
}

func grantedPermissionEntryWithinRequest(requested []FileSystemSandboxEntry, granted FileSystemSandboxEntry, cwd string) bool {
	if !permissionAccessCanRead(granted.Access) || !supportsSymbolicSlashTmp() && isSymbolicSlashTmpPath(granted.Path) {
		return false
	}
	if path := resolvePermissionPath(granted.Path, cwd); path != "" {
		return permissionAccessCovers(resolveRequestedPermissionAccess(requested, path, cwd), granted.Access)
	}
	for _, entry := range requested {
		if permissionAccessCovers(entry.Access, granted.Access) && permissionPathsEqual(entry.Path, granted.Path) {
			return true
		}
	}
	return false
}

func resolveRequestedPermissionAccess(entries []FileSystemSandboxEntry, target, cwd string) FileSystemAccessMode {
	return resolveRequestedPermissionAccessForConvention(entries, target, cwd, utils.NativePathConvention())
}

func resolveRequestedPermissionAccessForConvention(entries []FileSystemSandboxEntry, target, cwd string, convention utils.PathConvention) FileSystemAccessMode {
	bestSpecificity := -1
	bestPrecedence := -1
	var best FileSystemAccessMode
	for _, entry := range entries {
		entryPath := resolvePermissionPathForConvention(entry.Path, cwd, convention)
		matches := entryPath != "" && pathWithinForConvention(target, entryPath, convention)
		if entry.Path.Type == "glob_pattern" {
			pattern := materializePermissionGlobForConvention(entry.Path.Pattern, cwd, convention)
			patternSlash := slashPathForConvention(pattern, convention)
			targetSlash := slashPathForConvention(target, convention)
			if convention == utils.ConventionWindows {
				// Rust #41209: Windows glob matching folds case and uses
				// normalized separators; POSIX matching stays byte-oriented.
				patternSlash = strings.ToLower(patternSlash)
				targetSlash = strings.ToLower(targetSlash)
			}
			matched, err := doublestar.Match(patternSlash, targetSlash)
			matches = err == nil && matched
			entryPath = permissionGlobStaticPrefix(pattern)
		}
		if !matches {
			continue
		}
		specificity := pathComponentCount(entryPath)
		precedence := permissionAccessPrecedence(entry.Access)
		if specificity > bestSpecificity || specificity == bestSpecificity && precedence > bestPrecedence {
			best = entry.Access
			bestSpecificity = specificity
			bestPrecedence = precedence
		}
	}
	return best
}

func resolvePermissionPath(path FileSystemPath, cwd string) string {
	switch path.Type {
	case "path", "":
		return cleanRunPathWithCWD(path.Path, cwd)
	case "glob_pattern":
		return ""
	case "special":
		if path.Value == nil {
			return ""
		}
		switch strings.ToLower(path.Value.Kind) {
		case "root":
			return rootPathForCWD(cwd)
		case "project_roots", "current_working_directory":
			if path.Value.Subpath != nil && *path.Value.Subpath != "" {
				return cleanRunPathWithCWD(*path.Value.Subpath, cwd)
			}
			return cleanRunPath(cwd)
		case "tmpdir":
			return cleanRunPath(os.Getenv("TMPDIR"))
		case "slash_tmp":
			if !supportsSymbolicSlashTmp() {
				return ""
			}
			info, err := os.Stat("/tmp")
			if err == nil && info.IsDir() {
				return cleanRunPath("/tmp")
			}
		}
	}
	return ""
}

// resolvePermissionPathForConvention resolves an entry path using the
// executor's convention, so a POSIX policy evaluated on Windows is not rewritten
// with backslashes (Rust #41209).
func resolvePermissionPathForConvention(path FileSystemPath, cwd string, convention utils.PathConvention) string {
	if convention == "" {
		return resolvePermissionPath(path, cwd)
	}
	switch path.Type {
	case "path", "":
		raw := strings.TrimSpace(path.Path)
		if raw == "" {
			return ""
		}
		if convention == utils.ConventionWindows {
			raw = strings.ReplaceAll(raw, `\`, "/")
			if !windowsPathAbsoluteForConvention(raw) && strings.TrimSpace(cwd) != "" {
				raw = pathpkg.Join(strings.ReplaceAll(cwd, `\`, "/"), raw)
			}
			return strings.ReplaceAll(pathpkg.Clean(raw), "/", `\`)
		}
		if !pathpkg.IsAbs(raw) && pathpkg.IsAbs(cwd) {
			raw = pathpkg.Join(cwd, raw)
		}
		return pathpkg.Clean(raw)
	case "special":
		if path.Value == nil {
			return ""
		}
		switch strings.ToLower(path.Value.Kind) {
		case "root":
			if convention == utils.ConventionWindows {
				if volume := windowsVolumeForConvention(cwd); volume != "" {
					return volume + `\`
				}
				return ""
			}
			return "/"
		case "project_roots", "current_working_directory":
			if path.Value.Subpath != nil && *path.Value.Subpath != "" {
				return resolvePermissionPathForConvention(FileSystemPath{Type: "path", Path: *path.Value.Subpath}, cwd, convention)
			}
			return resolvePermissionPathForConvention(FileSystemPath{Type: "path", Path: cwd}, cwd, convention)
		case "tmpdir":
			if convention == utils.ConventionPosix {
				return resolvePermissionPathForConvention(FileSystemPath{Type: "path", Path: os.Getenv("TMPDIR")}, cwd, convention)
			}
			return resolvePermissionPath(path, cwd)
		case "slash_tmp":
			if convention == utils.ConventionPosix {
				return "/tmp"
			}
			return ""
		}
	}
	return resolvePermissionPath(path, cwd)
}

func materializePermissionGlobForConvention(pattern string, cwd string, convention utils.PathConvention) string {
	trimmed := strings.TrimSpace(pattern)
	if trimmed == "" {
		return ""
	}
	if convention == "" {
		return materializePermissionGlob(trimmed, cwd)
	}
	if convention == utils.ConventionWindows {
		trimmed = strings.ReplaceAll(trimmed, `\`, "/")
		if !windowsPathAbsoluteForConvention(trimmed) && strings.TrimSpace(cwd) != "" {
			trimmed = pathpkg.Join(strings.ReplaceAll(cwd, `\`, "/"), trimmed)
		}
		return strings.ReplaceAll(pathpkg.Clean(trimmed), "/", `\`)
	}
	if !pathpkg.IsAbs(trimmed) && pathpkg.IsAbs(cwd) {
		trimmed = pathpkg.Join(cwd, trimmed)
	}
	return pathpkg.Clean(trimmed)
}

func slashPathForConvention(value string, convention utils.PathConvention) string {
	if convention == utils.ConventionWindows {
		return pathpkg.Clean(strings.ReplaceAll(value, `\`, "/"))
	}
	return pathpkg.Clean(value)
}

func windowsPathAbsoluteForConvention(value string) bool {
	legacy := utils.NewLegacyAppPathString(value)
	convention, ok := legacy.InferAbsolutePathConvention()
	return ok && convention == utils.ConventionWindows
}

func windowsVolumeForConvention(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.ReplaceAll(value, `\`, "/")
	if len(value) >= 2 && value[1] == ':' {
		return value[:2]
	}
	if strings.HasPrefix(value, "//") {
		parts := strings.SplitN(strings.TrimPrefix(value, "//"), "/", 3)
		if len(parts) >= 2 {
			return `\\` + parts[0] + `\` + parts[1]
		}
	}
	return ""
}

func materializePermissionEntry(entry FileSystemSandboxEntry, cwd string) FileSystemSandboxEntry {
	entry = clonePermissionEntry(entry)
	if entry.Path.Type == "special" && entry.Path.Value != nil {
		switch strings.ToLower(entry.Path.Value.Kind) {
		case "project_roots", "current_working_directory":
			if path := resolvePermissionPath(entry.Path, cwd); path != "" {
				entry.Path = FileSystemPath{Type: "path", Path: path}
			}
		}
	} else if entry.Path.Type == "glob_pattern" {
		entry.Path.Pattern = materializePermissionGlob(entry.Path.Pattern, cwd)
	}
	return entry
}

func retainConstrainingPermissionDenies(source, accepted []FileSystemSandboxEntry, cwd string, output *[]FileSystemSandboxEntry) []FileSystemSandboxEntry {
	var retained []FileSystemSandboxEntry
	for _, deny := range source {
		if deny.Access != FileSystemAccessDeny || !permissionDenyConstrainsGrant(deny, accepted, cwd) {
			continue
		}
		deny = materializePermissionEntry(deny, cwd)
		appendUniquePermissionEntry(output, deny)
		appendUniquePermissionEntry(&retained, deny)
	}
	return retained
}

func permissionDenyConstrainsGrant(deny FileSystemSandboxEntry, accepted []FileSystemSandboxEntry, cwd string) bool {
	for _, grant := range accepted {
		if !permissionAccessCanRead(grant.Access) {
			continue
		}
		grantPath := resolvePermissionPath(grant.Path, cwd)
		if grantPath == "" {
			continue
		}
		denyPath := resolvePermissionPath(deny.Path, cwd)
		if deny.Path.Type == "glob_pattern" {
			denyPath = permissionGlobStaticPrefix(materializePermissionGlob(deny.Path.Pattern, cwd))
		}
		if denyPath != "" && pathsMayOverlap(denyPath, grantPath) {
			return true
		}
	}
	return false
}

// pathsMayOverlap reports whether two resolved filesystem paths occupy the
// same lexical subtree, matching Rust #41001's URI-native overlap check. The
// resolved paths are converted to PathUri values and compared with Overlaps;
// when a path is opaque or has ambiguous encoded component boundaries the
// comparison fails closed (treated as overlapping) so a deny is preserved
// rather than silently dropped.
func pathsMayOverlap(denyPath, grantPath string) bool {
	denyURI, err := uriFromPolicyPath(denyPath)
	if err != nil {
		return true
	}
	grantURI, err := uriFromPolicyPath(grantPath)
	if err != nil {
		return true
	}
	overlap, ok := denyURI.Overlaps(grantURI)
	if !ok {
		// Ambiguous component boundaries: fail closed.
		return true
	}
	return overlap
}

func uriFromPolicyPath(nativePath string) (*utils.PathURI, error) {
	if strings.TrimSpace(nativePath) == "" {
		return nil, &utils.ParseError{Reason: "empty path"}
	}
	if utils.CrossPlatformSlash(nativePath) != nativePath && strings.Contains(nativePath, `\`) {
		// Prefer the Windows convention for non-forward-slash paths so drive
		// letters and UNC hosts are preserved.
		convention := utils.ConventionWindows
		return utils.FromAbsoluteNativePath(filepath.ToSlash(nativePath), convention)
	}
	return utils.FromHostNativePath(filepath.Clean(nativePath))
}

func materializePermissionGlob(pattern, cwd string) string {
	if filepath.IsAbs(pattern) || strings.TrimSpace(cwd) == "" {
		return filepath.Clean(pattern)
	}
	return filepath.Clean(filepath.Join(cwd, pattern))
}

func permissionGlobStaticPrefix(pattern string) string {
	firstGlob := len(pattern)
	for _, marker := range []string{"*", "?", "[", "]"} {
		if index := strings.Index(pattern, marker); index >= 0 && index < firstGlob {
			firstGlob = index
		}
	}
	prefix := pattern[:firstGlob]
	if firstGlob < len(pattern) && !strings.HasSuffix(prefix, string(filepath.Separator)) && !strings.HasSuffix(prefix, "/") && !strings.HasSuffix(prefix, `\`) {
		prefix = filepath.Dir(prefix)
	}
	return cleanRunPath(prefix)
}

func mergePermissionGlobDepth(leftEntries []FileSystemSandboxEntry, leftDepth *uint32, rightEntries []FileSystemSandboxEntry, rightDepth *uint32) *uint32 {
	leftHasGlob := permissionEntriesHaveDenyGlob(leftEntries)
	rightHasGlob := permissionEntriesHaveDenyGlob(rightEntries)
	if !leftHasGlob && !rightHasGlob {
		return nil
	}
	if leftHasGlob && leftDepth == nil || rightHasGlob && rightDepth == nil {
		return nil
	}
	var depth uint32
	if leftDepth != nil {
		depth = *leftDepth
	}
	if rightDepth != nil && *rightDepth > depth {
		depth = *rightDepth
	}
	if depth == 0 {
		return nil
	}
	return &depth
}

func permissionGlobDepth(fileSystem *AdditionalFileSystemPermissions) *uint32 {
	if fileSystem == nil {
		return nil
	}
	return fileSystem.GlobScanMaxDepth
}

func permissionEntriesHaveDenyGlob(entries []FileSystemSandboxEntry) bool {
	for _, entry := range entries {
		if entry.Access == FileSystemAccessDeny && entry.Path.Type == "glob_pattern" {
			return true
		}
	}
	return false
}

func permissionAccessCanRead(access FileSystemAccessMode) bool {
	return access == FileSystemAccessRead || access == FileSystemAccessWrite
}

func permissionAccessCovers(requested, granted FileSystemAccessMode) bool {
	switch granted {
	case FileSystemAccessRead:
		return permissionAccessCanRead(requested)
	case FileSystemAccessWrite:
		return requested == FileSystemAccessWrite
	default:
		return false
	}
}

func permissionAccessPrecedence(access FileSystemAccessMode) int {
	switch access {
	case FileSystemAccessDeny:
		return 3
	case FileSystemAccessWrite:
		return 2
	case FileSystemAccessRead:
		return 1
	default:
		return 0
	}
}

func permissionPathsEqual(left, right FileSystemPath) bool {
	if left.Type != right.Type {
		return false
	}
	switch left.Type {
	case "path", "":
		return cleanRunPath(left.Path) == cleanRunPath(right.Path)
	case "glob_pattern":
		return left.Pattern == right.Pattern
	case "special":
		if left.Value == nil || right.Value == nil || !strings.EqualFold(left.Value.Kind, right.Value.Kind) {
			return false
		}
		return ptrStringValue(left.Value.Subpath) == ptrStringValue(right.Value.Subpath)
	default:
		return false
	}
}

func appendUniquePermissionEntry(entries *[]FileSystemSandboxEntry, entry FileSystemSandboxEntry) {
	for _, existing := range *entries {
		if existing.Access == entry.Access && permissionPathsEqual(existing.Path, entry.Path) {
			return
		}
	}
	*entries = append(*entries, entry)
}

func clonePermissionEntry(entry FileSystemSandboxEntry) FileSystemSandboxEntry {
	clone := entry
	if entry.Path.Value != nil {
		value := *entry.Path.Value
		if value.Subpath != nil {
			subpath := *value.Subpath
			value.Subpath = &subpath
		}
		clone.Path.Value = &value
	}
	return clone
}

// ResolveFileSystemEntryPath resolves a concrete entry path against cwd,
// mirroring the per-entry path resolution Rust performs before it collects
// writable roots (FileSystemSandboxPolicy::resolved_entries_with_cwd). Glob
// entries carry no single path and resolve to "".
//
// It is exported for package sandbox/integrity, which rebuilds upstream's
// writable-root view from a restricted entry list.
func ResolveFileSystemEntryPath(path FileSystemPath, cwd string) string {
	return resolvePermissionPath(path, cwd)
}

// ResolveFileSystemEntryAccess returns the effective access the entry list
// grants to target, using the specificity-then-precedence resolution the runtime
// applies to a restricted policy (Rust
// LocalFileSystemPolicyMatcher::resolve_access). Glob entries are matched with
// the shared doublestar matcher, and "" means no entry covers target.
//
// It is exported for package sandbox/integrity, which mirrors the policy
// resolution upstream's writable-root collection and deny-read matcher both use.
func ResolveFileSystemEntryAccess(entries []FileSystemSandboxEntry, target, cwd string) FileSystemAccessMode {
	return resolveRequestedPermissionAccessForConvention(entries, target, cwd, utils.NativePathConvention())
}

// ResolveFileSystemEntryAccessForConvention resolves access using the executor's
// path convention. Callers comparing a candidate from another host must use
// this entry point so Windows case folding does not leak into POSIX policies.
func ResolveFileSystemEntryAccessForConvention(entries []FileSystemSandboxEntry, target, cwd string, convention utils.PathConvention) FileSystemAccessMode {
	return resolveRequestedPermissionAccessForConvention(entries, target, cwd, convention)
}
