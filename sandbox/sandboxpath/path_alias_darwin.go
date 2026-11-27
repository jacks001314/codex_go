//go:build darwin

package sandboxpath

import (
	"path/filepath"
	"strings"
)

// normalizePathForContainment aligns case variants in the path's root prefix
// with the writable root when the underlying macOS filesystem is
// case-insensitive. The path remains unchanged when identity cannot be proved,
// so containment checks fail closed.
func normalizePathForContainment(path, root string) (string, string) {
	rootParts := pathComponents(root)
	pathParts := pathComponents(path)
	if len(rootParts) == 0 || len(pathParts) < len(rootParts) {
		return path, root
	}
	normalized := append([]string(nil), pathParts...)
	for index := range rootParts {
		expected, actual := rootParts[index], pathParts[index]
		if expected == actual {
			continue
		}
		if !strings.EqualFold(expected, actual) {
			return path, root
		}
		parent := filepath.Join(rootParts[:index]...)
		if parent == "" {
			parent = string(filepath.Separator)
		}
		insensitive, err := filesystemCaseInsensitive(parent)
		if err != nil || !insensitive {
			return path, root
		}
		normalized[index] = expected
	}
	return filepath.Join(normalized...), root
}
