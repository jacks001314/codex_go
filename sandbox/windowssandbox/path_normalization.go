package windowssandbox

import (
	"path/filepath"
	"strings"
)

func CanonicalizePath(path string) (string, error) {
	if path == "" {
		return "", ErrInvalidRequest
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path), nil
	}
	evaluated, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return filepath.Clean(abs), nil
	}
	return filepath.Clean(evaluated), nil
}

func CanonicalPathKey(path string) string {
	canonical, err := CanonicalizePath(path)
	if err != nil {
		canonical = filepath.Clean(path)
	}
	return strings.ToLower(strings.ReplaceAll(canonical, `\`, "/"))
}
