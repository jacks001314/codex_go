package config

import (
	"fmt"
	"sort"
	"strings"

	"codex_go/utils"
)

// denyReadGlobMetacharacters are the characters that make a filesystem denial a
// glob pattern rather than a literal path (Rust config_requirements.rs
// is_glob_metacharacter).
const denyReadGlobMetacharacters = "*?["

// resolveFilesystemDenyReadPaths mirrors Rust's filesystem denial resolution
// (#44669): relative literals and relative glob prefixes resolve against the
// owning requirements layer's base directory, the pattern shape is preserved,
// entries are deduplicated in declaration order, and invalid spellings fail
// configuration loading instead of being silently skipped.
func resolveFilesystemDenyReadPaths(permissions map[string]any, baseDir string) error {
	context, err := NewHostConfigPathContext(baseDir)
	if err != nil {
		return err
	}
	return resolveFilesystemDenyReadPathsWithContext(permissions, context)
}

func resolveFilesystemDenyReadPathsWithContext(permissions map[string]any, context *ConfigPathContext) error {
	if len(permissions) == 0 {
		return nil
	}
	if context == nil {
		return fmt.Errorf("permissions.filesystem.deny_read requires a path context")
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
		value, err := resolveDenyReadPatternWithContext(pattern, context)
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
	context, err := NewHostConfigPathContext(baseDir)
	if err != nil {
		return "", err
	}
	return resolveDenyReadPatternWithContext(pattern, context)
}

func resolveDenyReadPatternWithContext(pattern string, context *ConfigPathContext) (string, error) {
	trimmed := strings.TrimSpace(pattern)
	if trimmed == "" {
		return "", fmt.Errorf("permissions.filesystem.deny_read path must not be empty")
	}
	convention := context.Convention()
	if err := utils.ValidateConfigPathText(trimmed, convention); err != nil {
		return "", err
	}
	prefix, suffix := splitDenyReadGlobForConvention(trimmed, convention)
	if convention == utils.ConventionWindows &&
		startsWithTwoSeparators(trimmed) &&
		!startsWithTwoSeparators(prefix) {
		return "", fmt.Errorf("filesystem denial glob requires a literal UNC server and share")
	}
	if prefix == "" {
		prefix = "."
	}
	resolvedPrefix, err := context.ResolvePathString(prefix)
	if err != nil {
		return "", err
	}
	prefixURI, ok := utils.InferredPathURI(resolvedPrefix)
	if !ok {
		return "", fmt.Errorf("permissions.filesystem.deny_read path must resolve to an absolute path: %q", prefix)
	}
	if err := prefixURI.ValidateGlobDirectory(convention); err != nil {
		return "", err
	}
	if suffix == "" {
		return resolvedPrefix, nil
	}
	separator := "/"
	if convention == utils.ConventionWindows {
		separator = `\`
	}
	normalized := resolvedPrefix
	if !strings.HasSuffix(normalized, "/") && !strings.HasSuffix(normalized, `\`) {
		normalized += separator
	}
	normalized += suffix
	if err := utils.ValidateConfigPathText(normalized, convention); err != nil {
		return "", err
	}
	return normalized, nil
}

// splitDenyReadGlobForConvention splits a denial pattern into its literal
// directory prefix and remaining pattern text. The returned prefix never has a
// trailing separator, so the pieces rejoin with one. The owning convention,
// rather than the host OS, decides which separators and drive roots apply.
func splitDenyReadGlobForConvention(pattern string, convention utils.PathConvention) (string, string) {
	index := strings.IndexAny(pattern, denyReadGlobMetacharacters)
	if index < 0 {
		return pattern, ""
	}
	literal := pattern[:index]
	separator := -1
	for i := len(literal) - 1; i >= 0; i-- {
		if literal[i] == '/' || convention == utils.ConventionWindows && literal[i] == '\\' {
			separator = i
			break
		}
	}
	switch {
	case separator == 0:
		if pattern[0] == '/' || convention == utils.ConventionWindows {
			return string(pattern[0]), pattern[1:]
		}
		return "", pattern
	case convention == utils.ConventionWindows && separator == 2 && len(pattern) > 2 && pattern[1] == ':':
		// A drive root such as `C:\*.key` keeps its root as the prefix.
		return pattern[:3], pattern[3:]
	case separator > 0:
		return literal[:separator], literal[separator+1:] + pattern[index:]
	default:
		return "", pattern
	}
}

func splitDenyReadGlob(pattern string) (string, string) {
	return splitDenyReadGlobForConvention(pattern, utils.NativePathConvention())
}

func startsWithTwoSeparators(value string) bool {
	if len(value) < 2 {
		return false
	}
	return (value[0] == '/' || value[0] == '\\') && (value[1] == '/' || value[1] == '\\')
}
