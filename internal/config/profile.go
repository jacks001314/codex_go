package config

import (
	"fmt"
	"strings"
	"unicode"
)

func NormalizeProfileName(profile string) (string, error) {
	name := strings.TrimSpace(profile)
	if name == "" {
		return "", fmt.Errorf("%w: profile name is required", ErrInvalidConfigRequest)
	}
	for _, r := range name {
		if r == '-' || r == '_' || r == '.' {
			continue
		}
		if r <= unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			continue
		}
		return "", fmt.Errorf("%w: invalid profile name %q", ErrInvalidConfigRequest, profile)
	}
	if strings.Contains(name, "..") || strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".") {
		return "", fmt.Errorf("%w: invalid profile name %q", ErrInvalidConfigRequest, profile)
	}
	return name, nil
}
