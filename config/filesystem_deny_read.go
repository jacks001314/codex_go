package config

import (
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// denyReadGlobMetacharacters are the characters that make a filesystem denial a
// glob pattern rather than a literal path (Rust config_requirements.rs
// is_glob_metacharacter).
const denyReadGlobMetacharacters = "*?["

// resolveFilesystemDenyReadPaths mirrors Rust's filesystem denial resolution
// (Rust #44669): relative literals and relative glob prefixes resolve against
// the owning requirements layer's base directory, the pattern shape is
// preserved, entries are deduplicated in declaration order, and invalid
// spellings fail configuration loading instead of being silently skipped.
func resolveFilesystemDenyReadPaths(permissions map[string]any, baseDir string) error {
	if len(permissions) == 0 {
		return nil
	}
	filesystem, ok := permissions["filesystem"].(map[string]any)
	if !ok {
		return nil
	}
	raw, ok := filesystem["deny_read"]
	if !ok {
		return nil
	}
	patterns, err := denyReadPatterns(raw)
	if err != nil {
		return err
	}
	resolved := make([]any, 0, len(patterns))
	seen := make(map[string]struct{}, len(patterns))
	for _, pattern := range patterns {
		value, err := resolveDenyReadPattern(pattern, baseDir)
		if err != nil {
			return err
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		resolved = append(resolved, value)
	}
	filesystem["deny_read"] = resolved
	return nil
}

func denyReadPatterns(raw any) ([]string, error) {
	switch items := raw.(type) {
	case []any:
		out := make([]string, 0, len(items))
		for _, item := range items {
			value, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("permissions.filesystem.deny_read entries must be strings")
			}
			out = append(out, value)
		}
		return out, nil
	case []string:
		return append([]string(nil), items...), nil
	case map[string]any:
		keys := make([]string, 0, len(items))
		for key := range items {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		return keys, nil
	case string:
		return []string{items}, nil
	default:
		return nil, fmt.Errorf("permissions.filesystem.deny_read must be a list or table")
	}
}

// FilesystemDenyReadPatterns returns the declared filesystem denial patterns in
// order, accepting the list and table forms used by requirements. Callers use it
// to convert already-resolved patterns into their runtime policy shape.
func FilesystemDenyReadPatterns(raw any) ([]string, error) {
	return denyReadPatterns(raw)
}

func resolveDenyReadPattern(pattern string, baseDir string) (string, error) {
	trimmed := strings.TrimSpace(pattern)
	if trimmed == "" {
		return "", fmt.Errorf("permissions.filesystem.deny_read path must not be empty")
	}
	if strings.ContainsRune(trimmed, 0) {
		return "", fmt.Errorf("permissions.filesystem.deny_read path must not contain NUL bytes")
	}
	prefix, suffix := splitDenyReadGlob(trimmed)
	resolvedPrefix := prefix
	if !filepath.IsAbs(prefix) {
		// An empty prefix (a bare glob such as `**/*.key`) resolves against the
		// layer base as well.
		if strings.TrimSpace(baseDir) == "" {
			return "", fmt.Errorf("permissions.filesystem.deny_read path must be absolute: %q", trimmed)
		}
		resolvedPrefix = filepath.Join(baseDir, prefix)
	}
	if suffix == "" {
		return filepath.Clean(resolvedPrefix), nil
	}
	return filepath.Clean(resolvedPrefix + string(filepath.Separator) + suffix), nil
}

// splitDenyReadGlob splits a denial pattern into its literal directory prefix and
// the remaining pattern text, mirroring Rust's split_glob_pattern. The returned
// prefix never carries a trailing separator, so the pieces rejoin with one.
func splitDenyReadGlob(pattern string) (string, string) {
	index := strings.IndexAny(pattern, denyReadGlobMetacharacters)
	if index < 0 {
		return pattern, ""
	}
	literal := pattern[:index]
	separator := strings.LastIndexAny(literal, `/\`)
	switch {
	case separator == 0:
		return string(filepath.Separator), pattern[1:]
	case runtime.GOOS == "windows" && separator == 2 && len(pattern) > 2 && pattern[1] == ':':
		// A drive root such as `C:\*.key` keeps its root as the prefix.
		return pattern[:3], pattern[3:]
	case separator > 0:
		return literal[:separator], literal[separator+1:] + pattern[index:]
	default:
		return "", pattern
	}
}
