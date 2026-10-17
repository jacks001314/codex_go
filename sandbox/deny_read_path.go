package sandbox

import (
	"errors"
	"fmt"
	"strings"

	"codex_go/utils"
)

// denyReadGlobMetacharacters are the characters that classify a filesystem
// denial as a glob pattern (Rust config_requirements.rs is_glob_metacharacter).
const denyReadGlobMetacharacters = "*?["

// ParseDenyReadPath classifies a resolved filesystem denial as a literal path or
// a glob pattern, preserving the pattern shape (Rust #44669). Invalid spellings
// are rejected so a managed denial cannot be silently dropped.
func ParseDenyReadPath(pattern string) (FileSystemPath, error) {
	return ParseDenyReadPathForConvention(pattern, utils.NativePathConvention())
}

// ParseDenyReadPathForConvention classifies a denial using the owning
// executor's path grammar. Literal paths are validated as lossless native
// paths, while glob patterns retain their spelling after text validation.
func ParseDenyReadPathForConvention(pattern string, convention utils.PathConvention) (FileSystemPath, error) {
	trimmed := strings.TrimSpace(pattern)
	if trimmed == "" {
		return FileSystemPath{}, errors.New("filesystem deny_read path must not be empty")
	}
	if err := utils.ValidateConfigPathText(trimmed, convention); err != nil {
		return FileSystemPath{}, fmt.Errorf("filesystem deny_read path is invalid: %w", err)
	}
	if strings.ContainsAny(trimmed, denyReadGlobMetacharacters) {
		return FileSystemPath{Type: "glob_pattern", Pattern: trimmed}, nil
	}
	uri, ok := utils.InferredPathURI(trimmed)
	if !ok {
		return FileSystemPath{}, fmt.Errorf("filesystem deny_read path must be absolute: %q", trimmed)
	}
	if err := uri.ValidateConfigPath(convention); err != nil {
		return FileSystemPath{}, fmt.Errorf("filesystem deny_read path is invalid: %w", err)
	}
	rendered, err := uri.ToConfigPathString(convention)
	if err != nil {
		return FileSystemPath{}, fmt.Errorf("filesystem deny_read path is invalid: %w", err)
	}
	return FileSystemPath{Type: "path", Path: rendered}, nil
}
