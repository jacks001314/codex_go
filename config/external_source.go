package config

import (
	"path/filepath"
	"strings"
)

const (
	externalMigrationSourceClaude = "claude-code"
	externalMigrationSourceCursor = "cursor"
	externalCursorConfigDir       = ".cursor"
)

func normalizeExternalMigrationSource(source *string) string {
	if source != nil && strings.EqualFold(strings.TrimSpace(*source), externalMigrationSourceCursor) {
		return externalMigrationSourceCursor
	}
	return externalMigrationSourceClaude
}

func (s *ConfigService) externalAgentHomeForSource(source string) string {
	home := strings.TrimSpace(s.externalAgentHome)
	if source != externalMigrationSourceCursor || home == "" {
		return home
	}
	base := filepath.Base(filepath.Clean(home))
	if strings.EqualFold(base, externalClaudeConfigDir) {
		return filepath.Join(filepath.Dir(filepath.Clean(home)), externalCursorConfigDir)
	}
	return home
}
