package config

import (
	"fmt"
	"strconv"
	"strings"
)

type Override struct {
	Path  string
	Value any
}

func ParseOverrides(raw []string) ([]Override, error) {
	out := make([]Override, 0, len(raw))
	for _, item := range raw {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			return nil, fmt.Errorf("invalid override (missing '='): %s", item)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" {
			return nil, fmt.Errorf("empty key in override: %s", item)
		}
		out = append(out, Override{
			Path:  CanonicalizeKey(key),
			Value: parseValue(value),
		})
	}
	return out, nil
}

func CanonicalizeKey(key string) string {
	if key == "use_legacy_landlock" {
		return "features.use_legacy_landlock"
	}
	return key
}

func ApplyOverrides(root map[string]any, overrides []Override) {
	for _, override := range overrides {
		apply(root, strings.Split(override.Path, "."), override.Value, nil)
	}
}

func parseValue(raw string) any {
	if raw == "" {
		return ""
	}
	if unquoted, ok := unquote(raw); ok {
		return unquoted
	}
	if raw == "true" {
		return true
	}
	if raw == "false" {
		return false
	}
	if i, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(raw, 64); err == nil {
		return f
	}
	if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
		return parseArray(strings.TrimSpace(raw[1 : len(raw)-1]))
	}
	if strings.HasPrefix(raw, "{") && strings.HasSuffix(raw, "}") {
		return parseInlineTable(strings.TrimSpace(raw[1 : len(raw)-1]))
	}
	return strings.Trim(raw, "\"'")
}

func unquote(raw string) (string, bool) {
	if len(raw) < 2 {
		return "", false
	}
	if (raw[0] == '"' && raw[len(raw)-1] == '"') || (raw[0] == '\'' && raw[len(raw)-1] == '\'') {
		return raw[1 : len(raw)-1], true
	}
	return "", false
}

func parseArray(raw string) []any {
	if raw == "" {
		return []any{}
	}
	parts := splitTopLevel(raw, ',')
	out := make([]any, 0, len(parts))
	for _, part := range parts {
		out = append(out, parseValue(strings.TrimSpace(part)))
	}
	return out
}

func parseInlineTable(raw string) map[string]any {
	out := map[string]any{}
	if raw == "" {
		return out
	}
	for _, part := range splitTopLevel(raw, ',') {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(key)] = parseValue(strings.TrimSpace(value))
	}
	return out
}

func splitTopLevel(raw string, sep rune) []string {
	var parts []string
	var current strings.Builder
	depth := 0
	var quote rune
	for _, r := range raw {
		switch {
		case quote != 0:
			current.WriteRune(r)
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			quote = r
			current.WriteRune(r)
		case r == '[' || r == '{':
			depth++
			current.WriteRune(r)
		case r == ']' || r == '}':
			depth--
			current.WriteRune(r)
		case r == sep && depth == 0:
			parts = append(parts, current.String())
			current.Reset()
		default:
			current.WriteRune(r)
		}
	}
	parts = append(parts, current.String())
	return parts
}

func apply(root map[string]any, parts []string, value any, path []string) {
	if len(parts) == 0 {
		return
	}
	key := parts[0]
	childPath := append(append([]string(nil), path...), key)
	if len(parts) == 1 {
		if isMultiAgentV2FeaturePath(childPath) {
			// Mirrors Rust apply_toml_override (config/src/overrides.rs):
			// multi_agent_v2 boolean/table overrides preserve nested config in
			// either ordering instead of silently replacing the lower value.
			switch existing := root[key].(type) {
			case map[string]any:
				if enabled, ok := value.(bool); ok {
					existing["enabled"] = enabled
					return
				}
				if overlayTable, ok := value.(map[string]any); ok {
					mergeConfigMaps(existing, overlayTable)
					return
				}
			case bool:
				if _, ok := value.(map[string]any); ok {
					table := map[string]any{"enabled": existing}
					mergeConfigMaps(table, value.(map[string]any))
					root[key] = table
					return
				}
			}
		}
		root[key] = value
		return
	}
	next, ok := root[key].(map[string]any)
	if !ok {
		if isMultiAgentV2FeaturePath(childPath) {
			// Mirrors Rust: a legacy boolean toggle on the traversal path is
			// converted to an enabled table before descending.
			if enabled, isBool := root[key].(bool); isBool {
				next = map[string]any{"enabled": enabled}
			} else {
				next = map[string]any{}
			}
		} else {
			next = map[string]any{}
		}
		root[key] = next
	}
	apply(next, parts[1:], value, childPath)
}
