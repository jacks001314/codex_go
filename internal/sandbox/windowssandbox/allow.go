package windowssandbox

import (
	"os"
	"sort"
)

type AllowDenyPaths struct {
	Allow map[string]struct{}
	Deny  map[string]struct{}
}

func ComputeAllowPathsForPermissions(permissions *ResolvedWindowsSandboxPermissions, commandCWD string, envMap map[string]string) AllowDenyPaths {
	paths := AllowDenyPaths{
		Allow: map[string]struct{}{},
		Deny:  map[string]struct{}{},
	}
	if permissions == nil {
		return paths
	}
	for _, writableRoot := range permissions.WritableRootsForCWD(commandCWD, envMap) {
		if canonical, ok := canonicalExistingPath(writableRoot.Root); ok {
			paths.Allow[canonical] = struct{}{}
		}
		for _, readOnlySubpath := range writableRoot.ReadOnlySubpaths {
			if canonical, ok := canonicalExistingPath(readOnlySubpath); ok {
				paths.Deny[canonical] = struct{}{}
			}
		}
	}
	return paths
}

func (p AllowDenyPaths) AllowSlice() []string {
	return sortedStringSetKeys(p.Allow)
}

func (p AllowDenyPaths) DenySlice() []string {
	return sortedStringSetKeys(p.Deny)
}

func canonicalExistingPath(path string) (string, bool) {
	if _, err := os.Stat(path); err != nil {
		return "", false
	}
	canonical, err := CanonicalizePath(path)
	if err != nil {
		return cleanWindowsSandboxAbs(path), true
	}
	return canonical, true
}

func sortedStringSetKeys(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
