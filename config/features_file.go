package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func ConfigPath(codexHome string) string {
	return filepath.Join(codexHome, "config.toml")
}

func ProfileConfigPath(codexHome string, profile string) string {
	return filepath.Join(codexHome, strings.TrimSpace(profile)+".config.toml")
}

func ResolveProfileConfigPath(codexHome string, profile string) (string, error) {
	name, err := NormalizeProfileName(profile)
	if err != nil {
		return "", err
	}
	return filepath.Join(codexHome, name+".config.toml"), nil
}

func LoadFeatureSettings(codexHome string) (map[string]bool, error) {
	cfg, err := Load(codexHome)
	if err != nil {
		return nil, err
	}
	return cfg.FeatureSettings(), nil
}

func SetFeature(codexHome, feature string, enabled bool) error {
	path := ConfigPath(codexHome)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return writeNewFeatureConfig(path, feature, enabled)
	}
	if err != nil {
		return err
	}

	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	// Rust ConfigEditsBuilder::set_feature_enabled writes `enabled` into an
	// existing structured feature table (core/config/edit.rs SetPath +
	// is_structured_feature_path), so nested keys such as
	// `features.sleep_tool.mode` survive an enable/disable toggle (#41243).
	if lines, updated := setStructuredFeatureEnabled(lines, feature, enabled); updated {
		return writeFeatureConfig(path, lines)
	}
	lines = setLegacyFeatureEnabled(lines, feature, enabled)
	return writeFeatureConfig(path, lines)
}

// setStructuredFeatureEnabled writes `enabled` inside `[features.<feature>]` or
// an inline `feature = { ... }` value under `[features]`, reporting whether
// either form was found.
func setStructuredFeatureEnabled(lines []string, feature string, enabled bool) ([]string, bool) {
	if lines, ok := setFeatureTableEnabled(lines, feature, enabled); ok {
		return lines, true
	}
	return setInlineFeatureEnabled(lines, feature, enabled)
}

func setFeatureTableEnabled(lines []string, feature string, enabled bool) ([]string, bool) {
	header := "[features." + feature + "]"
	for i, line := range lines {
		if strings.TrimSpace(stripComment(line)) != header {
			continue
		}
		for j := i + 1; j <= len(lines); j++ {
			if j == len(lines) {
				return insertConfigLine(lines, j, enabledFeatureLine(enabled)), true
			}
			body := strings.TrimSpace(stripComment(lines[j]))
			if strings.HasPrefix(body, "[") && strings.HasSuffix(body, "]") {
				return insertConfigLine(lines, j, enabledFeatureLine(enabled)), true
			}
			if key, _, ok := strings.Cut(body, "="); ok && strings.TrimSpace(key) == "enabled" {
				lines[j] = enabledFeatureLine(enabled)
				return lines, true
			}
		}
	}
	return lines, false
}

func setInlineFeatureEnabled(lines []string, feature string, enabled bool) ([]string, bool) {
	inFeatures := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(stripComment(line))
		if trimmed == "[features]" {
			inFeatures = true
			continue
		}
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			inFeatures = false
			continue
		}
		if !inFeatures {
			continue
		}
		key, rawValue, ok := strings.Cut(trimmed, "=")
		if !ok || strings.TrimSpace(key) != feature {
			continue
		}
		inline, ok := setInlineTableEnabled(strings.TrimSpace(rawValue), enabled)
		if !ok {
			return lines, false
		}
		lines[i] = fmt.Sprintf("%s = %s", feature, inline)
		return lines, true
	}
	return lines, false
}

func setInlineTableEnabled(value string, enabled bool) (string, bool) {
	inner := strings.TrimSpace(value)
	if !strings.HasPrefix(inner, "{") || !strings.HasSuffix(inner, "}") {
		return "", false
	}
	inner = strings.TrimSpace(inner[1 : len(inner)-1])
	if updated, ok := replaceInlineTableKey(inner, "enabled", fmt.Sprintf("%t", enabled)); ok {
		return "{ " + updated + " }", true
	}
	if inner == "" {
		return fmt.Sprintf("{ enabled = %t }", enabled), true
	}
	return fmt.Sprintf("{ enabled = %t, %s }", enabled, inner), true
}

// replaceInlineTableKey replaces the `key = value` entry of a flat inline table
// body.
func replaceInlineTableKey(inner, key, value string) (string, bool) {
	entries := strings.Split(inner, ",")
	found := false
	for i, entry := range entries {
		entryKey, _, ok := strings.Cut(entry, "=")
		if !ok || strings.TrimSpace(entryKey) != key {
			continue
		}
		entries[i] = key + " = " + value
		found = true
	}
	if !found {
		return "", false
	}
	return strings.Join(entries, ","), true
}

func setLegacyFeatureEnabled(lines []string, feature string, enabled bool) []string {

	inFeatures := false
	featuresSeen := false
	insertAt := -1
	updated := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(stripComment(line))
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if inFeatures && insertAt == -1 {
				insertAt = i
			}
			inFeatures = trimmed == "[features]"
			if inFeatures {
				featuresSeen = true
				insertAt = i + 1
			}
			continue
		}
		if !inFeatures {
			continue
		}
		key, _, ok := strings.Cut(trimmed, "=")
		if ok && strings.TrimSpace(key) == feature {
			lines[i] = fmt.Sprintf("%s = %t", feature, enabled)
			updated = true
		}
	}

	if !featuresSeen {
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
			lines = append(lines, "")
		}
		lines = append(lines, "[features]", fmt.Sprintf("%s = %t", feature, enabled))
	} else if !updated {
		if insertAt < 0 {
			insertAt = len(lines)
		}
		newLine := fmt.Sprintf("%s = %t", feature, enabled)
		lines = append(lines, "")
		copy(lines[insertAt+1:], lines[insertAt:])
		lines[insertAt] = newLine
	}
	return lines
}

func insertConfigLine(lines []string, index int, line string) []string {
	lines = append(lines, "")
	copy(lines[index+1:], lines[index:])
	lines[index] = line
	return lines
}

func enabledFeatureLine(enabled bool) string {
	return fmt.Sprintf("enabled = %t", enabled)
}

func writeFeatureConfig(path string, lines []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

func writeNewFeatureConfig(path, feature string, enabled bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	body := fmt.Sprintf("[features]\n%s = %t\n", feature, enabled)
	return os.WriteFile(path, []byte(body), 0o600)
}

func stripComment(line string) string {
	var quote rune
	for i, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			quote = r
		case r == '#':
			return line[:i]
		}
	}
	return line
}
