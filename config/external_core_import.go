package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

const (
	externalClaudeConfigDir  = ".claude"
	externalClaudeConfigFile = "settings.json"
	externalClaudeDocFile    = "CLAUDE.md"
)

type externalMigrationScope struct {
	repoRoot string
}

func (s externalMigrationScope) home() bool {
	return strings.TrimSpace(s.repoRoot) == ""
}

func (s *ConfigService) detectExternalCoreMigrations(scope externalMigrationScope) []ExternalAgentConfigMigrationItem {
	items := make([]ExternalAgentConfigMigrationItem, 0, 7)
	cwd := externalScopeCWD(scope)
	if source, target, migrated, ok := s.externalConfigMigration(scope); ok && externalConfigHasMissing(target, migrated) {
		items = append(items, ExternalAgentConfigMigrationItem{
			ItemType:    MigrationConfig,
			Description: fmt.Sprintf("Migrate %s into %s", source, target),
			CWD:         cwd,
		})
	}
	if item, ok := s.detectExternalMCPMigration(scope); ok {
		items = append(items, item)
	}
	if item, ok := s.detectExternalHooksMigration(scope); ok {
		items = append(items, item)
	}
	if source, target, names := s.externalSkillsMigration(scope); len(names) > 0 {
		details := NewMigrationDetails()
		for _, name := range names {
			details.Skills = append(details.Skills, NamedMigration{Name: name})
		}
		items = append(items, ExternalAgentConfigMigrationItem{
			ItemType:    MigrationSkills,
			Description: fmt.Sprintf("Migrate skills from %s to %s", source, target),
			CWD:         cwd,
			Details:     details,
		})
	}
	if item, ok := s.detectExternalCommandsMigration(scope); ok {
		items = append(items, item)
	}
	if item, ok := s.detectExternalSubagentsMigration(scope); ok {
		items = append(items, item)
	}
	if sources, target := s.externalInstructionMigration(scope); len(sources) > 0 && missingOrEmptyTextFile(target) {
		items = append(items, ExternalAgentConfigMigrationItem{
			ItemType:    MigrationAgentsMD,
			Description: fmt.Sprintf("Migrate %s to %s", strings.Join(sources, ", "), target),
			CWD:         cwd,
		})
	}
	if item, ok := s.detectExternalPluginsMigration(scope); ok {
		items = append(items, item)
	}
	return items
}

func (s *ConfigService) importExternalCoreMigration(item ExternalAgentConfigMigrationItem) ExternalAgentConfigImportTypeResult {
	result := ExternalAgentConfigImportTypeResult{
		ItemType:  item.ItemType,
		Successes: []ExternalAgentConfigImportItemTypeSuccess{},
		Failures:  []ExternalAgentConfigImportItemTypeFailure{},
	}
	scope, ok := externalScopeFromCWD(item.CWD)
	if !ok {
		return externalCoreImportFailure(result, item, "scope_resolution", errors.New("migration working directory does not exist"))
	}
	var err error
	switch item.ItemType {
	case MigrationConfig:
		err = s.importExternalConfig(scope, &result)
	case MigrationSkills:
		err = s.importExternalSkills(scope, &result)
	case MigrationAgentsMD:
		err = s.importExternalInstructions(scope, &result)
	default:
		err = fmt.Errorf("unsupported core migration type %s", item.ItemType)
	}
	if err != nil {
		return externalCoreImportFailure(result, item, strings.ToLower(string(item.ItemType))+"_import", err)
	}
	return result
}

func (s *ConfigService) externalConfigMigration(scope externalMigrationScope) (string, string, map[string]any, bool) {
	source := s.externalSettingsPath(scope)
	settings, ok := readEffectiveClaudeSettings(source)
	if !ok {
		return "", "", nil, false
	}
	migrated := buildClaudeConfigMigration(settings)
	if len(migrated) == 0 {
		return "", "", nil, false
	}
	return source, s.externalTargetConfigPath(scope), migrated, true
}

func (s *ConfigService) externalSkillsMigration(scope externalMigrationScope) (string, string, []string) {
	var source, target string
	if scope.home() {
		source = filepath.Join(s.externalAgentHome, "skills")
		target = filepath.Join(filepath.Dir(s.codexHome), ".agents", "skills")
	} else {
		source = filepath.Join(scope.repoRoot, externalClaudeConfigDir, "skills")
		target = filepath.Join(scope.repoRoot, ".agents", "skills")
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		return source, target, nil
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() && !pathExists(filepath.Join(target, entry.Name())) {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return source, target, names
}

func (s *ConfigService) externalInstructionMigration(scope externalMigrationScope) ([]string, string) {
	if scope.home() {
		source := filepath.Join(s.externalAgentHome, externalClaudeDocFile)
		if nonEmptyTextFile(source) {
			return []string{source}, filepath.Join(s.codexHome, "AGENTS.md")
		}
		return nil, filepath.Join(s.codexHome, "AGENTS.md")
	}
	target := filepath.Join(scope.repoRoot, "AGENTS.md")
	for _, source := range []string{
		filepath.Join(scope.repoRoot, externalClaudeDocFile),
		filepath.Join(scope.repoRoot, externalClaudeConfigDir, externalClaudeDocFile),
	} {
		if nonEmptyTextFile(source) {
			return []string{source}, target
		}
	}
	return nil, target
}

func (s *ConfigService) externalSettingsPath(scope externalMigrationScope) string {
	if scope.home() {
		return filepath.Join(s.externalAgentHome, externalClaudeConfigFile)
	}
	return filepath.Join(scope.repoRoot, externalClaudeConfigDir, externalClaudeConfigFile)
}

func (s *ConfigService) externalTargetConfigPath(scope externalMigrationScope) string {
	if scope.home() {
		return filepath.Join(s.codexHome, "config.toml")
	}
	return filepath.Join(scope.repoRoot, ".codex", "config.toml")
}

func (s *ConfigService) importExternalConfig(scope externalMigrationScope, result *ExternalAgentConfigImportTypeResult) error {
	source, target, migrated, ok := s.externalConfigMigration(scope)
	if !ok {
		return nil
	}
	existing := map[string]any{}
	if data, err := os.ReadFile(target); err == nil && strings.TrimSpace(string(data)) != "" {
		if err := toml.Unmarshal(data, &existing); err != nil {
			return fmt.Errorf("invalid existing config.toml: %w", err)
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if !mergeMissingExternalConfig(existing, migrated) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	data, err := toml.Marshal(existing)
	if err != nil {
		return err
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}
	if err := os.WriteFile(target, data, 0o600); err != nil {
		return err
	}
	result.Successes = append(result.Successes, externalMigrationSuccess(MigrationConfig, source, target, externalScopeCWD(scope)))
	return nil
}

func (s *ConfigService) importExternalSkills(scope externalMigrationScope, result *ExternalAgentConfigImportTypeResult) error {
	source, target, names := s.externalSkillsMigration(scope)
	if len(names) == 0 {
		return nil
	}
	if err := os.MkdirAll(target, 0o700); err != nil {
		return err
	}
	for _, name := range names {
		if err := copyExternalSkill(filepath.Join(source, name), filepath.Join(target, name)); err != nil {
			return err
		}
		result.Successes = append(result.Successes, externalMigrationSuccess(MigrationSkills, name, name, externalScopeCWD(scope)))
	}
	return nil
}

func (s *ConfigService) importExternalInstructions(scope externalMigrationScope, result *ExternalAgentConfigImportTypeResult) error {
	sources, target := s.externalInstructionMigration(scope)
	if len(sources) == 0 || !missingOrEmptyTextFile(target) {
		return nil
	}
	parts := make([]string, 0, len(sources))
	for _, source := range sources {
		data, err := os.ReadFile(source)
		if err != nil {
			return err
		}
		parts = append(parts, rewriteClaudeTerms(string(data)))
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(target, []byte(strings.Join(parts, "\n\n")), 0o600); err != nil {
		return err
	}
	result.Successes = append(result.Successes, externalMigrationSuccess(MigrationAgentsMD, strings.Join(sources, ", "), target, externalScopeCWD(scope)))
	return nil
}

func readEffectiveClaudeSettings(path string) (map[string]any, bool) {
	settings, ok := readJSONObject(path)
	local, localOK := readJSONObject(filepath.Join(filepath.Dir(path), "settings.local.json"))
	if localOK {
		if !ok {
			settings = map[string]any{}
		}
		mergeExternalJSON(settings, local)
		ok = true
	}
	return settings, ok
}

func readJSONObject(path string) (map[string]any, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var value map[string]any
	if json.Unmarshal(data, &value) != nil || value == nil {
		return nil, false
	}
	return value, true
}

func buildClaudeConfigMigration(settings map[string]any) map[string]any {
	migrated := map[string]any{}
	if env, ok := settings["env"].(map[string]any); ok {
		set := map[string]any{}
		for key, value := range env {
			switch value := value.(type) {
			case string:
				set[key] = value
			case bool, float64, json.Number:
				set[key] = fmt.Sprint(value)
			}
		}
		if len(set) > 0 {
			migrated["shell_environment_policy"] = map[string]any{"inherit": "core", "set": set}
		}
	}
	if sandbox, ok := settings["sandbox"].(map[string]any); ok && sandbox["enabled"] == true {
		migrated["sandbox_mode"] = "workspace-write"
	}
	return migrated
}

func externalConfigHasMissing(target string, migrated map[string]any) bool {
	data, err := os.ReadFile(target)
	if errors.Is(err, os.ErrNotExist) || strings.TrimSpace(string(data)) == "" {
		return len(migrated) > 0
	}
	if err != nil {
		return false
	}
	existing := map[string]any{}
	if toml.Unmarshal(data, &existing) != nil {
		return true
	}
	return mergeMissingExternalConfig(existing, migrated)
}

func mergeMissingExternalConfig(existing map[string]any, incoming map[string]any) bool {
	changed := false
	for key, incomingValue := range incoming {
		existingValue, found := existing[key]
		if !found {
			existing[key] = incomingValue
			changed = true
			continue
		}
		existingTable, existingOK := existingValue.(map[string]any)
		incomingTable, incomingOK := incomingValue.(map[string]any)
		if existingOK && incomingOK && mergeMissingExternalConfig(existingTable, incomingTable) {
			changed = true
		}
	}
	return changed
}

func mergeExternalJSON(existing map[string]any, incoming map[string]any) {
	for key, incomingValue := range incoming {
		if incomingTable, ok := incomingValue.(map[string]any); ok {
			if existingTable, ok := existing[key].(map[string]any); ok {
				mergeExternalJSON(existingTable, incomingTable)
				continue
			}
		}
		existing[key] = incomingValue
	}
}

func externalScopeFromCWD(cwd *string) (externalMigrationScope, bool) {
	if cwd == nil || strings.TrimSpace(*cwd) == "" {
		return externalMigrationScope{}, true
	}
	root := externalRepoRoot(*cwd)
	return externalMigrationScope{repoRoot: root}, root != ""
}

func externalScopeCWD(scope externalMigrationScope) *string {
	if scope.home() {
		return nil
	}
	cwd := scope.repoRoot
	return &cwd
}

func externalRepoRoot(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return ""
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return ""
	}
	info, err := os.Stat(abs)
	if err != nil {
		return ""
	}
	if !info.IsDir() {
		abs = filepath.Dir(abs)
	}
	if root := nearestGitRoot(abs); root != "" {
		return root
	}
	return filepath.Clean(abs)
}

func missingOrEmptyTextFile(path string) bool {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	data, err := os.ReadFile(path)
	return err == nil && strings.TrimSpace(string(data)) == ""
}

func nonEmptyTextFile(path string) bool {
	data, err := os.ReadFile(path)
	return err == nil && strings.TrimSpace(string(data)) != ""
}

func copyExternalSkill(source string, target string) error {
	if err := os.MkdirAll(target, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		from := filepath.Join(source, entry.Name())
		to := filepath.Join(target, entry.Name())
		if entry.IsDir() {
			if err := copyExternalSkill(from, to); err != nil {
				return err
			}
			continue
		}
		if !entry.Type().IsRegular() {
			continue
		}
		data, err := os.ReadFile(from)
		if err != nil {
			return err
		}
		if strings.EqualFold(entry.Name(), "SKILL.md") {
			data = []byte(rewriteClaudeTerms(string(data)))
		}
		if err := os.WriteFile(to, data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func rewriteClaudeTerms(content string) string {
	rewritten := replaceExternalTerm(content, externalClaudeDocFile, "AGENTS.md")
	for _, term := range []string{"claude code", "claude-code", "claude_code", "claudecode", "claude"} {
		rewritten = replaceExternalTerm(rewritten, term, "Codex")
	}
	return rewritten
}

func replaceExternalTerm(input string, needle string, replacement string) string {
	lowerInput := strings.ToLower(input)
	lowerNeedle := strings.ToLower(needle)
	if lowerNeedle == "" {
		return input
	}
	var output strings.Builder
	last := 0
	search := 0
	for search < len(input) {
		relative := strings.Index(lowerInput[search:], lowerNeedle)
		if relative < 0 {
			break
		}
		start := search + relative
		end := start + len(lowerNeedle)
		before := start == 0 || !externalWordByte(input[start-1])
		after := end == len(input) || !externalWordByte(input[end])
		if before && after {
			output.WriteString(input[last:start])
			output.WriteString(replacement)
			last = end
		}
		search = start + 1
	}
	if last == 0 {
		return input
	}
	output.WriteString(input[last:])
	return output.String()
}

func externalWordByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || value == '_'
}

func externalMigrationSuccess(itemType MigrationItemType, source string, target string, cwd *string) ExternalAgentConfigImportItemTypeSuccess {
	return ExternalAgentConfigImportItemTypeSuccess{ItemType: itemType, Source: &source, Target: &target, CWD: cloneStringPtr(cwd)}
}

func externalCoreImportFailure(result ExternalAgentConfigImportTypeResult, item ExternalAgentConfigMigrationItem, stage string, err error) ExternalAgentConfigImportTypeResult {
	errorType := stage
	result.Failures = append(result.Failures, ExternalAgentConfigImportItemTypeFailure{
		ItemType:     item.ItemType,
		ErrorType:    &errorType,
		FailureStage: stage,
		Message:      err.Error(),
		CWD:          cloneStringPtr(item.CWD),
	})
	return result
}
