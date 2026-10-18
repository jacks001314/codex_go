package tui

import (
	"path/filepath"
	"strings"
)

// FormatInstructionSources renders AGENTS.md instruction paths for /status.
// Files inside or one level above the working directory stay project-relative;
// other files under the home directory collapse to "~" (Rust #40502).
func FormatInstructionSources(paths []string, cwd string, home string) string {
	cwd = cleanAbsolutePath(cwd)
	home = cleanAbsolutePath(home)
	seen := map[string]struct{}{}
	out := make([]string, 0, len(paths))
	for _, raw := range paths {
		path := strings.TrimSpace(raw)
		if path == "" {
			continue
		}
		display := filepath.Clean(path)
		if filepath.IsAbs(display) {
			if cwd != "" {
				if relative, ok := projectRelativeInstructionPath(cwd, display); ok {
					display = relative
				}
			}
			if filepath.IsAbs(display) && home != "" {
				if relative, err := filepath.Rel(home, display); err == nil &&
					relative != "." &&
					relative != ".." &&
					!strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
					display = "~" + string(filepath.Separator) + relative
				}
			}
		}
		display = filepath.ToSlash(display)
		if _, ok := seen[display]; ok {
			continue
		}
		seen[display] = struct{}{}
		out = append(out, display)
	}
	return strings.Join(out, ", ")
}

func projectRelativeInstructionPath(cwd string, path string) (string, bool) {
	relative, err := filepath.Rel(cwd, path)
	if err != nil || relative == "." || filepath.IsAbs(relative) {
		return "", false
	}
	parts := strings.Split(relative, string(filepath.Separator))
	parents := 0
	for _, part := range parts {
		if part != ".." {
			break
		}
		parents++
	}
	if parents <= 1 {
		return relative, true
	}
	return "", false
}

func cleanAbsolutePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsAbs(path) {
		return ""
	}
	return filepath.Clean(path)
}
