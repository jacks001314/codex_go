package config

import (
	"path/filepath"
	"strings"
)

const (
	externalMigrationSourceClaude = "claude-code"
	// externalMigrationSourceClaudeLocalSkills imports only the skills a user
	// authored locally, skipping the marketplace-synced directory (Rust #60492).
	externalMigrationSourceClaudeLocalSkills = "claude-code-local-skills"
	externalMigrationSourceCursor            = "cursor"
	externalCursorConfigDir                  = ".cursor"
)

// externalSkillImportMode mirrors Rust's SkillImportMode (#60492).
type externalSkillImportMode int

const (
	externalSkillImportAll externalSkillImportMode = iota
	externalSkillImportLocalOnly
)

// externalSkillImportModeForSource mirrors Rust's from_migration_source: the
// dedicated local-skills source keeps the Claude source but imports only
// locally authored skills.
func externalSkillImportModeForSource(source *string) externalSkillImportMode {
	if source != nil && *source == externalMigrationSourceClaudeLocalSkills {
		return externalSkillImportLocalOnly
	}
	return externalSkillImportAll
}

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
