package chatwidget

import (
	"strings"

	"codex_go/tui/history_cell"
)

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
	// count is the number of diagnostics the footer reports (Rust's `count`).
	count int
	// dismissed mirrors Rust's `dismissed`: a diagnostic is hidden only while its
	// identity still carries the same details, so an identity that acquires more
	// details is shown again.
	dismissed map[historycell.WarningId]string
}

// VisibleEntries mirrors `WarningDisplayState::visible_entries`: the merged
// diagnostics with the dismissed versions removed.
func (s *WarningDisplayState) VisibleEntries(cells []historycell.WarningCell) []historycell.WarningEntry {
	entries := historycell.WarningEntries(cells)
	if s == nil || len(s.dismissed) == 0 {
		return entries
	}
	visible := make([]historycell.WarningEntry, 0, len(entries))
	for _, entry := range entries {
		if details, ok := s.dismissed[entry.ID]; ok && details == entry.Details {
			continue
		}
		visible = append(visible, entry)
	}
	return visible
}

// ApplyDecisions mirrors the `AppEvent::UpdateWarnings` arm: dismissed entries
// record their exact details, and a kept entry clears a dismissal only when the
// stored details still match.
func (s *WarningDisplayState) ApplyDecisions(dismissed []historycell.WarningEntry, kept []historycell.WarningEntry) {
	if s == nil {
		return
	}
	if len(dismissed) > 0 && s.dismissed == nil {
		s.dismissed = map[historycell.WarningId]string{}
	}
	for _, entry := range dismissed {
		s.dismissed[entry.ID] = entry.Details
	}
	for _, entry := range kept {
		if details, ok := s.dismissed[entry.ID]; ok && details == entry.Details {
			delete(s.dismissed, entry.ID)
		}
	}
}

// SyncWarnings mirrors `ChatWidget::sync_warnings`: the footer count is the
// distinct identity count until a diagnostic was dismissed, after which it is the
// number of still-visible diagnostics.
func (s *WarningDisplayState) SyncWarnings(cells []historycell.WarningCell) {
	if s == nil {
		return
	}
	if len(s.dismissed) == 0 {
		s.count = historycell.WarningCount(cells)
		return
	}
	s.count = len(s.VisibleEntries(cells))
}

// WarningCount reports the last synchronized diagnostic count.
func (s *WarningDisplayState) WarningCount() int {
	if s == nil {
		return 0
	}
	return s.count
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
