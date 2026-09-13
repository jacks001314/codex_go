package chatwidget

import "strings"

const (
	fallbackModelMetadataWarningPrefix = "Model metadata for `"
	fallbackModelMetadataWarningSuffix = "` not found. Defaulting to fallback metadata; this can degrade performance and cause issues."
)

type WarningDisplayState struct {
	fallbackModelMetadataSlugs map[string]bool
	// startupConfigWarnings mirrors Rust's startup_config_warnings: a config
	// warning that already reached the startup warnings cell must not be
	// displayed again when it arrives later as an ordinary thread warning.
	startupConfigWarnings map[string]bool
}

func (s *WarningDisplayState) ShouldDisplay(message string) bool {
	if s == nil {
		return true
	}
	if s.startupConfigWarnings[message] {
		return false
	}
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

// MarkStartupConfigWarning records a warning that was shown in the startup
// warnings cell so later duplicates are suppressed
// (WarningDisplayState.startup_config_warnings).
func (s *WarningDisplayState) MarkStartupConfigWarning(message string) {
	if s == nil || strings.TrimSpace(message) == "" {
		return
	}
	if s.startupConfigWarnings == nil {
		s.startupConfigWarnings = map[string]bool{}
	}
	s.startupConfigWarnings[message] = true
}

func FallbackModelMetadataWarningSlug(message string) (string, bool) {
	if !strings.HasPrefix(message, fallbackModelMetadataWarningPrefix) || !strings.HasSuffix(message, fallbackModelMetadataWarningSuffix) {
		return "", false
	}
	slug := strings.TrimSuffix(strings.TrimPrefix(message, fallbackModelMetadataWarningPrefix), fallbackModelMetadataWarningSuffix)
	return slug, true
}
