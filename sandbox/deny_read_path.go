package sandbox

import (
	"errors"
	"fmt"
	"strings"
)

// denyReadGlobMetacharacters are the characters that classify a filesystem
// denial as a glob pattern (Rust config_requirements.rs is_glob_metacharacter).
const denyReadGlobMetacharacters = "*?["

// ParseDenyReadPath classifies a resolved filesystem denial as a literal path or
// a glob pattern, preserving the pattern shape (Rust #44669). Invalid spellings
// are rejected so a managed denial cannot be silently dropped.
func ParseDenyReadPath(pattern string) (FileSystemPath, error) {
	trimmed := strings.TrimSpace(pattern)
	if trimmed == "" {
		return FileSystemPath{}, errors.New("filesystem deny_read path must not be empty")
	}
	if strings.ContainsRune(trimmed, 0) {
		return FileSystemPath{}, fmt.Errorf("filesystem deny_read path must not contain NUL bytes: %q", trimmed)
	}
	if strings.ContainsAny(trimmed, denyReadGlobMetacharacters) {
		return FileSystemPath{Type: "glob_pattern", Pattern: trimmed}, nil
	}
	return FileSystemPath{Type: "path", Path: trimmed}, nil
}
