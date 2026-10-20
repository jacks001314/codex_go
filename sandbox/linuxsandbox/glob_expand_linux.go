//go:build linux

package linuxsandbox

import (
	"path/filepath"
	"sort"
)

func expandUnreadableGlobsInProcess(patterns []string, cwd string, maxDepth *int) ([]string, error) {
	expanded := map[string]bool{}
	for _, pattern := range patterns {
		matches, err := expandLinuxDenyGlob(pattern, cwd, maxDepth)
		if err != nil {
			return nil, err
		}
		for _, match := range matches {
			expanded[filepath.Clean(match)] = true
		}
	}
	paths := make([]string, 0, len(expanded))
	for path := range expanded {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}
