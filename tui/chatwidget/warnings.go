package chatwidget

import "strings"

const (
	fallbackModelMetadataWarningPrefix = "Model metadata for `"
	fallbackModelMetadataWarningSuffix = "` not found. Defaulting to fallback metadata; this can degrade performance and cause issues."
)

type WarningDisplayState struct {
	fallbackModelMetadataSlugs map[string]bool
}

func (s *WarningDisplayState) ShouldDisplay(message string) bool {
	slug, ok := FallbackModelMetadataWarningSlug(message)
	if !ok {
		return true
	}
	if s.fallbackModelMetadataSlugs == nil {
		s.fallbackModelMetadataSlugs = map[string]bool{}
	}
	if s.fallbackModelMetadataSlugs[slug] {
		return false
	}
	s.fallbackModelMetadataSlugs[slug] = true
	return true
}

func FallbackModelMetadataWarningSlug(message string) (string, bool) {
	if !strings.HasPrefix(message, fallbackModelMetadataWarningPrefix) || !strings.HasSuffix(message, fallbackModelMetadataWarningSuffix) {
		return "", false
	}
	slug := strings.TrimSuffix(strings.TrimPrefix(message, fallbackModelMetadataWarningPrefix), fallbackModelMetadataWarningSuffix)
	return slug, true
}
