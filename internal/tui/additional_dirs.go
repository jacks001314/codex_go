package tui

import "strings"

type AdditionalDir struct {
	Path     string
	Writable bool
}

func NormalizeAdditionalDirs(paths []string, writable bool) []AdditionalDir {
	out := make([]AdditionalDir, 0, len(paths))
	seen := map[string]bool{}
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, AdditionalDir{Path: path, Writable: writable})
	}
	return out
}
