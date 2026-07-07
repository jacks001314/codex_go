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
