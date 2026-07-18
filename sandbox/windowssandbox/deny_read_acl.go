package windowssandbox

import (
	"os"
	"path/filepath"
	"strings"
)

func PlanDenyReadACLPaths(paths []string) []string {
	planned := make([]string, 0, len(paths))
	seen := map[string]bool{}
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		path = filepath.Clean(path)
		pushPlannedDenyReadPath(&planned, seen, path)
		if _, err := os.Stat(path); err == nil {
			canonical, err := CanonicalizePath(path)
			if err == nil {
				pushPlannedDenyReadPath(&planned, seen, canonical)
			}
		}
	}
	return planned
}

func pushPlannedDenyReadPath(planned *[]string, seen map[string]bool, path string) {
	key := lexicalPathKey(path)
	if seen[key] {
		return
	}
	seen[key] = true
	*planned = append(*planned, path)
}

func lexicalPathKey(path string) string {
	return strings.ToLower(strings.TrimRight(strings.ReplaceAll(path, `\`, "/"), "/"))
}
