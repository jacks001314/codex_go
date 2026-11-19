package tui

import "strings"

// PathURIBasename returns the last non-empty segment of a path string using
// either path convention, so a Windows path reported by a remote app server
// keeps its final component on a Unix client (Rust PathUri::basename, #62375).
// It mirrors Rust's segment walk: the root ("/", "\\") and an empty path have
// no basename, while a Windows drive root ("C:\\") answers "C:".
func PathURIBasename(path string) (string, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", false
	}
	trimmed := strings.TrimRight(path, `/\`)
	if trimmed == "" {
		return "", false
	}
	if index := strings.LastIndexAny(trimmed, `/\`); index >= 0 {
		segment := trimmed[index+1:]
		if segment == "" {
			return "", false
		}
		return segment, true
	}
	return trimmed, true
}
