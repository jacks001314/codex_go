package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	codexplugin "codex_go/plugin"

	"github.com/pelletier/go-toml/v2"
)

const (
	externalCursorHomeConfigFile    = "cli-config.json"
	externalCursorProjectConfigFile = "cli.json"
	externalCursorSandboxFile       = "sandbox.json"
	externalCursorRulesFile         = ".cursorrules"
)

func (s *ConfigService) detectExternalCursorMigrations(scope externalMigrationScope) []ExternalAgentConfigMigrationItem {
	items := make([]ExternalAgentConfigMigrationItem, 0, 8)
	cwd := externalScopeCWD(scope)
	if source, target, migrated, ok := s.externalCursorConfigMigration(scope); ok && externalConfigHasMissing(target, migrated) {
		items = append(items, ExternalAgentConfigMigrationItem{ItemType: MigrationConfig, Description: fmt.Sprintf("Migrate %s into %s", source, target), CWD: cwd})
	}
	if item, ok := s.detectExternalCursorMCPMigration(scope); ok {
		items = append(items, item)
	}
	if item, ok := s.detectExternalCursorHooksMigration(scope); ok {
		items = append(items, item)
	}
	if source, target, names := s.externalCursorSkillsMigration(scope); len(names) > 0 {
		details := NewMigrationDetails()
		for _, name := range names {
			details.Skills = append(details.Skills, NamedMigration{Name: name})
		}
		items = append(items, ExternalAgentConfigMigrationItem{ItemType: MigrationSkills, Description: fmt.Sprintf("Migrate skills from %s to %s", source, target), CWD: cwd, Details: details})
	}
	if item, ok := s.detectExternalCursorCommandsMigration(scope); ok {
		items = append(items, item)
	}
	if item, ok := s.detectExternalCursorSubagentsMigration(scope); ok {
		items = append(items, item)
	}
	if sources, target := s.externalCursorInstructionMigration(scope); len(sources) > 0 && missingOrEmptyTextFile(target) {
		items = append(items, ExternalAgentConfigMigrationItem{ItemType: MigrationAgentsMD, Description: fmt.Sprintf("Migrate %s to %s", strings.Join(sources, ", "), target), CWD: cwd})
	}
	if item, ok := s.detectExternalCursorPluginsMigration(scope); ok {
		items = append(items, item)
	}
	return items
}

func (s *ConfigService) importExternalCursorMigration(item ExternalAgentConfigMigrationItem) ExternalAgentConfigImportTypeResult {
	result := ExternalAgentConfigImportTypeResult{ItemType: item.ItemType, Successes: []ExternalAgentConfigImportItemTypeSuccess{}, Failures: []ExternalAgentConfigImportItemTypeFailure{}}
	scope, ok := externalScopeFromCWD(item.CWD)
	if !ok {
		return externalCoreImportFailure(result, item, "scope_resolution", errors.New("migration working directory does not exist"))
	}
	var err error
	switch item.ItemType {
	case MigrationConfig:
		err = s.importExternalCursorConfig(scope, &result)
	case MigrationMCPServerConfig:
		err = s.importExternalCursorMCP(scope, &result)
	case MigrationHooks:
		return s.importExternalCursorHooksMigration(item)
	case MigrationSkills:
		err = s.importExternalCursorSkills(scope, &result)
	case MigrationCommands:
		err = s.importExternalCursorCommands(scope, &result)
	case MigrationSubagents:
		err = s.importExternalCursorSubagents(scope, &result)
	case MigrationAgentsMD:
		err = s.importExternalCursorInstructions(scope, &result)
	case MigrationPlugins:
		return s.importExternalCursorPluginsMigration(item)
	default:
		err = fmt.Errorf("unsupported Cursor migration type %s", item.ItemType)
	}
	if err != nil {
		return externalCoreImportFailure(result, item, strings.ToLower(string(item.ItemType))+"_import", err)
	}
	return result
}

func (s *ConfigService) externalCursorHome() string {
	return s.externalAgentHomeForSource(externalMigrationSourceCursor)
}

func (s *ConfigService) externalCursorConfigDir(scope externalMigrationScope) string {
	if scope.home() {
		return s.externalCursorHome()
	}
	return filepath.Join(scope.repoRoot, externalCursorConfigDir)
}

func (s *ConfigService) externalCursorSettingsPath(scope externalMigrationScope) string {
	name := externalCursorProjectConfigFile
	if scope.home() {
		name = externalCursorHomeConfigFile
	}
	return filepath.Join(s.externalCursorConfigDir(scope), name)
}

func (s *ConfigService) externalCursorConfigMigration(scope externalMigrationScope) (string, string, map[string]any, bool) {
	sourceDir := s.externalCursorConfigDir(scope)
	source := s.externalCursorSettingsPath(scope)
	settings, ok := readJSONObject(source)
	sandbox, sandboxOK := readJSONObject(filepath.Join(sourceDir, externalCursorSandboxFile))
	if sandboxOK {
		if !ok {
			settings = map[string]any{}
		}
		settings["__cursorSandbox"] = sandbox
		ok = true
	}
	if !ok {
		return "", "", nil, false
	}
	migrated := buildCursorConfigMigration(settings)
	if len(migrated) == 0 {
		return "", "", nil, false
	}
	return source, s.externalTargetConfigPath(scope), migrated, true
}

func buildCursorConfigMigration(settings map[string]any) map[string]any {
	migrated := buildClaudeConfigMigration(map[string]any{"env": settings["env"]})
	sandbox, _ := settings["__cursorSandbox"].(map[string]any)
	mode := ""
	switch externalString(sandbox["type"]) {
	case "workspace_readwrite":
		mode = "workspace-write"
	case "read_only":
		mode = "read-only"
	}
	if mode != "" {
		migrated["sandbox_mode"] = mode
	}
	if mode != "workspace-write" {
		return migrated
	}
	workspace := map[string]any{}
	var roots []string
	for _, path := range externalStrings(sandbox["additionalReadwritePaths"]) {
		if filepath.IsAbs(path) {
			roots = append(roots, path)
		}
	}
	if len(roots) > 0 {
		workspace["writable_roots"] = roots
	}
	if externalBool(sandbox["disableTmpWrite"]) {
		workspace["exclude_slash_tmp"] = true
		workspace["exclude_tmpdir_env_var"] = true
	}
	if network, ok := sandbox["networkPolicy"].(map[string]any); ok && externalString(network["default"]) == "allow" {
		workspace["network_access"] = true
	}
	if len(workspace) > 0 {
		migrated["sandbox_workspace_write"] = workspace
	}
	return migrated
}

func (s *ConfigService) importExternalCursorConfig(scope externalMigrationScope, result *ExternalAgentConfigImportTypeResult) error {
	source, target, migrated, ok := s.externalCursorConfigMigration(scope)
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
	if err := writeExternalTOML(target, existing); err != nil {
		return err
	}
	result.Successes = append(result.Successes, externalMigrationSuccess(MigrationConfig, source, target, externalScopeCWD(scope)))
	return nil
}

func (s *ConfigService) buildExternalCursorMCPMigration(scope externalMigrationScope) map[string]any {
	value, ok := readJSONObject(filepath.Join(s.externalCursorConfigDir(scope), "mcp.json"))
	if !ok {
		return map[string]any{}
	}
	servers := map[string]any{}
	appendExternalMCPServers(servers, value, true)
	converted := map[string]any{}
	for _, name := range sortedExternalMapKeys(servers) {
		raw, _ := servers[name].(map[string]any)
		if server, ok := convertExternalMCPServer(raw); ok {
			converted[name] = server
		}
	}
	if len(converted) == 0 {
		return map[string]any{}
	}
	return map[string]any{"mcp_servers": converted}
}

func (s *ConfigService) detectExternalCursorMCPMigration(scope externalMigrationScope) (ExternalAgentConfigMigrationItem, bool) {
	migrated := s.buildExternalCursorMCPMigration(scope)
	servers, _ := migrated["mcp_servers"].(map[string]any)
	missing := missingExternalMCPServers(s.externalTargetConfigPath(scope), servers)
	if len(missing) == 0 {
		return ExternalAgentConfigMigrationItem{}, false
	}
	details := NewMigrationDetails()
	for _, name := range missing {
		details.MCPServers = append(details.MCPServers, NamedMigration{Name: name})
	}
	source := filepath.Join(s.externalCursorConfigDir(scope), "mcp.json")
	return ExternalAgentConfigMigrationItem{ItemType: MigrationMCPServerConfig, Description: fmt.Sprintf("Migrate MCP servers from %s into %s", source, s.externalTargetConfigPath(scope)), CWD: externalScopeCWD(scope), Details: details}, true
}

func (s *ConfigService) importExternalCursorMCP(scope externalMigrationScope, result *ExternalAgentConfigImportTypeResult) error {
	target := s.externalTargetConfigPath(scope)
	migrated := s.buildExternalCursorMCPMigration(scope)
	servers, _ := migrated["mcp_servers"].(map[string]any)
	missing := missingExternalMCPServers(target, servers)
	if len(missing) == 0 {
		return nil
	}
	existing := map[string]any{}
	if data, err := os.ReadFile(target); err == nil && strings.TrimSpace(string(data)) != "" {
		if err := toml.Unmarshal(data, &existing); err != nil {
			return fmt.Errorf("invalid existing config.toml: %w", err)
		}
	}
	existingServers, ok := existing["mcp_servers"].(map[string]any)
	if !ok {
		existingServers = map[string]any{}
		existing["mcp_servers"] = existingServers
	}
	for _, name := range missing {
		existingServers[name] = servers[name]
	}
	if err := writeExternalTOML(target, existing); err != nil {
		return err
	}
	for _, name := range missing {
		result.Successes = append(result.Successes, externalMigrationSuccess(MigrationMCPServerConfig, name, name, externalScopeCWD(scope)))
	}
	return nil
}

func writeExternalTOML(target string, values map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	data, err := toml.Marshal(values)
	if err != nil {
		return err
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}
	return os.WriteFile(target, data, 0o600)
}

func (s *ConfigService) externalCursorSkillsMigration(scope externalMigrationScope) (string, string, []string) {
	source := filepath.Join(s.externalCursorConfigDir(scope), "skills")
	target := filepath.Join(scope.repoRoot, ".agents", "skills")
	if scope.home() {
		target = filepath.Join(filepath.Dir(s.codexHome), ".agents", "skills")
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		return source, target, nil
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() && !pathExists(filepath.Join(target, entry.Name())) {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return source, target, names
}

func (s *ConfigService) importExternalCursorSkills(scope externalMigrationScope, result *ExternalAgentConfigImportTypeResult) error {
	source, target, names := s.externalCursorSkillsMigration(scope)
	for _, name := range names {
		if err := copyExternalDirectoryWithRewrite(filepath.Join(source, name), filepath.Join(target, name), rewriteCursorTerms); err != nil {
			return err
		}
		result.Successes = append(result.Successes, externalMigrationSuccess(MigrationSkills, name, name, externalScopeCWD(scope)))
	}
	return nil
}

func (s *ConfigService) externalCursorCommandsPaths(scope externalMigrationScope) (string, string) {
	source := filepath.Join(s.externalCursorConfigDir(scope), "commands")
	target := filepath.Join(scope.repoRoot, ".agents", "skills")
	if scope.home() {
		target = filepath.Join(filepath.Dir(s.codexHome), ".agents", "skills")
	}
	return source, target
}

func discoverCursorCommands(source string, target string) []externalCommandMigration {
	var files []string
	_ = filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err == nil && entry != nil && !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".md") && !strings.EqualFold(entry.Name(), "README.md") {
			files = append(files, path)
		}
		return nil
	})
	sort.Strings(files)
	byName := map[string][]externalCommandMigration{}
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		frontmatter, body, _ := parseExternalFrontmatter(string(data))
		relative, err := filepath.Rel(source, path)
		if err != nil {
			continue
		}
		sourceName := strings.ReplaceAll(strings.TrimSuffix(filepath.ToSlash(relative), filepath.Ext(relative)), "/", "-")
		description := strings.TrimSpace(frontmatter["description"])
		if description == "" {
			description = fmt.Sprintf("Migrated source command `%s`", sourceName)
		}
		if externalUnsupportedTemplate(body) {
			continue
		}
		name := externalSlug("source-command-" + sourceName)
		if len([]rune(name)) > 64 || pathExists(filepath.Join(target, name)) {
			continue
		}
		byName[name] = append(byName[name], externalCommandMigration{SourcePath: path, SourceName: sourceName, Name: name, Description: description, Body: body})
	}
	var out []externalCommandMigration
	for _, name := range sortedExternalMapKeys(byName) {
		if len(byName[name]) == 1 {
			out = append(out, byName[name][0])
		}
	}
	return out
}

func (s *ConfigService) detectExternalCursorCommandsMigration(scope externalMigrationScope) (ExternalAgentConfigMigrationItem, bool) {
	source, target := s.externalCursorCommandsPaths(scope)
	commands := discoverCursorCommands(source, target)
	if len(commands) == 0 {
		return ExternalAgentConfigMigrationItem{}, false
	}
	details := NewMigrationDetails()
	for _, command := range commands {
		details.Commands = append(details.Commands, NamedMigration{Name: command.Name})
	}
	return ExternalAgentConfigMigrationItem{ItemType: MigrationCommands, Description: fmt.Sprintf("Migrate commands from %s to %s", source, target), CWD: externalScopeCWD(scope), Details: details}, true
}

func (s *ConfigService) importExternalCursorCommands(scope externalMigrationScope, result *ExternalAgentConfigImportTypeResult) error {
	source, target := s.externalCursorCommandsPaths(scope)
	for _, command := range discoverCursorCommands(source, target) {
		dir := filepath.Join(target, command.Name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		rendered := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n# %s\n\nUse this skill when the user asks to run the migrated source command `%s`.\n\n## Command Template\n\n%s\n",
			yamlQuoted(command.Name), yamlQuoted(rewriteCursorTerms(command.Description)), command.Name, command.SourceName, externalBodyOrFallback(rewriteCursorTerms(strings.TrimSpace(command.Body)), "No command template body was found."))
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(rendered), 0o600); err != nil {
			return err
		}
		result.Successes = append(result.Successes, externalMigrationSuccess(MigrationCommands, command.Name, command.Name, externalScopeCWD(scope)))
	}
	return nil
}

func (s *ConfigService) externalCursorSubagentPaths(scope externalMigrationScope) (string, string) {
	source := filepath.Join(s.externalCursorConfigDir(scope), "agents")
	target := filepath.Join(scope.repoRoot, ".codex", "agents")
	if scope.home() {
		target = filepath.Join(s.codexHome, "agents")
	}
	return source, target
}

func (s *ConfigService) detectExternalCursorSubagentsMigration(scope externalMigrationScope) (ExternalAgentConfigMigrationItem, bool) {
	source, target := s.externalCursorSubagentPaths(scope)
	agents := discoverExternalSubagents(source, target)
	if len(agents) == 0 {
		return ExternalAgentConfigMigrationItem{}, false
	}
	details := NewMigrationDetails()
	for _, agent := range agents {
		details.Subagents = append(details.Subagents, NamedMigration{Name: agent.Name})
	}
	return ExternalAgentConfigMigrationItem{ItemType: MigrationSubagents, Description: fmt.Sprintf("Migrate subagents from %s to %s", source, target), CWD: externalScopeCWD(scope), Details: details}, true
}

func (s *ConfigService) importExternalCursorSubagents(scope externalMigrationScope, result *ExternalAgentConfigImportTypeResult) error {
	source, target := s.externalCursorSubagentPaths(scope)
	for _, agent := range discoverExternalSubagents(source, target) {
		value := map[string]any{"name": agent.Name, "description": rewriteCursorTerms(agent.Description), "developer_instructions": externalBodyOrFallback(rewriteCursorTerms(strings.TrimSpace(agent.Body)), "No subagent instructions were found.")}
		if effort := externalAgentEffort(agent.Effort); effort != "" {
			value["model_reasoning_effort"] = effort
		}
		if sandbox := externalAgentSandbox(agent.PermissionMode); sandbox != "" {
			value["sandbox_mode"] = sandbox
		}
		if err := writeExternalTOML(filepath.Join(target, strings.TrimSuffix(filepath.Base(agent.SourcePath), filepath.Ext(agent.SourcePath))+".toml"), value); err != nil {
			return err
		}
		result.Successes = append(result.Successes, externalMigrationSuccess(MigrationSubagents, agent.Name, agent.Name, externalScopeCWD(scope)))
	}
	return nil
}

func (s *ConfigService) externalCursorInstructionMigration(scope externalMigrationScope) ([]string, string) {
	if scope.home() {
		return nil, filepath.Join(s.codexHome, "AGENTS.md")
	}
	source := filepath.Join(scope.repoRoot, externalCursorRulesFile)
	if nonEmptyTextFile(source) {
		return []string{source}, filepath.Join(scope.repoRoot, "AGENTS.md")
	}
	return nil, filepath.Join(scope.repoRoot, "AGENTS.md")
}

func (s *ConfigService) importExternalCursorInstructions(scope externalMigrationScope, result *ExternalAgentConfigImportTypeResult) error {
	sources, target := s.externalCursorInstructionMigration(scope)
	if len(sources) == 0 || !missingOrEmptyTextFile(target) {
		return nil
	}
	data, err := os.ReadFile(sources[0])
	if err != nil {
		return err
	}
	if err := os.WriteFile(target, []byte(rewriteCursorTerms(string(data))), 0o600); err != nil {
		return err
	}
	result.Successes = append(result.Successes, externalMigrationSuccess(MigrationAgentsMD, sources[0], target, externalScopeCWD(scope)))
	return nil
}

func rewriteCursorTerms(content string) string {
	rewritten := replaceExternalTerm(content, externalCursorRulesFile, "AGENTS.md")
	return replaceExternalTermCaseSensitive(rewritten, "Cursor", "Codex")
}

func replaceExternalTermCaseSensitive(input string, needle string, replacement string) string {
	if needle == "" {
		return input
	}
	var output strings.Builder
	last, search := 0, 0
	for search < len(input) {
		relative := strings.Index(input[search:], needle)
		if relative < 0 {
			break
		}
		start := search + relative
		end := start + len(needle)
		if (start == 0 || !externalWordByte(input[start-1])) && (end == len(input) || !externalWordByte(input[end])) {
			output.WriteString(input[last:start])
			output.WriteString(replacement)
			last = end
		}
		search = end
	}
	if last == 0 {
		return input
	}
	output.WriteString(input[last:])
	return output.String()
}

func copyExternalDirectoryWithRewrite(source string, target string, rewrite func(string) string) error {
	if err := os.MkdirAll(target, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		from, to := filepath.Join(source, entry.Name()), filepath.Join(target, entry.Name())
		if entry.IsDir() {
			if err := copyExternalDirectoryWithRewrite(from, to, rewrite); err != nil {
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
			data = []byte(rewrite(string(data)))
		}
		if err := os.WriteFile(to, data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func (s *ConfigService) detectExternalCursorHooksMigration(scope externalMigrationScope) (ExternalAgentConfigMigrationItem, bool) {
	sourceDir := s.externalCursorConfigDir(scope)
	target := filepath.Join(s.codexHome, "hooks.json")
	if !scope.home() {
		target = filepath.Join(scope.repoRoot, ".codex", "hooks.json")
	}
	if !missingOrEmptyTextFile(target) {
		return ExternalAgentConfigMigrationItem{}, false
	}
	migration, err := buildExternalCursorHooksMigration(sourceDir, filepath.Dir(target))
	if err != nil || len(migration.Groups) == 0 {
		return ExternalAgentConfigMigrationItem{}, false
	}
	details := NewMigrationDetails()
	for _, eventName := range externalHookEventNames {
		if len(migration.Groups[eventName]) > 0 {
			details.Hooks = append(details.Hooks, NamedMigration{Name: eventName})
		}
	}
	return ExternalAgentConfigMigrationItem{ItemType: MigrationHooks, Description: fmt.Sprintf("Migrate hooks from %s to %s", sourceDir, target), CWD: externalScopeCWD(scope), Details: details}, true
}

func (s *ConfigService) importExternalCursorHooksMigration(item ExternalAgentConfigMigrationItem) ExternalAgentConfigImportTypeResult {
	result := ExternalAgentConfigImportTypeResult{ItemType: MigrationHooks, Successes: []ExternalAgentConfigImportItemTypeSuccess{}, Failures: []ExternalAgentConfigImportItemTypeFailure{}}
	scope, ok := externalScopeFromCWD(item.CWD)
	if !ok {
		return externalCoreImportFailure(result, item, "scope_resolution", errors.New("migration working directory does not exist"))
	}
	sourceDir := s.externalCursorConfigDir(scope)
	target := filepath.Join(s.codexHome, "hooks.json")
	if !scope.home() {
		target = filepath.Join(scope.repoRoot, ".codex", "hooks.json")
	}
	if !missingOrEmptyTextFile(target) {
		return result
	}
	migration, err := buildExternalCursorHooksMigration(sourceDir, filepath.Dir(target))
	if err != nil {
		return externalCoreImportFailure(result, item, "hooks_import", err)
	}
	if len(migration.Groups) == 0 {
		return result
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return externalCoreImportFailure(result, item, "hooks_import", err)
	}
	if err := copyExternalHookScripts(filepath.Join(sourceDir, "hooks"), filepath.Join(filepath.Dir(target), "hooks")); err != nil {
		return externalCoreImportFailure(result, item, "hooks_import", err)
	}
	data, err := json.MarshalIndent(externalHookFile{Hooks: externalHookPayloadFromGroups(migration.Groups)}, "", "  ")
	if err != nil {
		return externalCoreImportFailure(result, item, "hooks_import", err)
	}
	if err := os.WriteFile(target, append(data, '\n'), 0o600); err != nil {
		return externalCoreImportFailure(result, item, "hooks_import", err)
	}
	for _, eventName := range externalHookEventNames {
		if len(migration.Groups[eventName]) > 0 {
			result.Successes = append(result.Successes, externalMigrationSuccess(MigrationHooks, eventName, eventName, externalScopeCWD(scope)))
		}
	}
	return result
}

func buildExternalCursorHooksMigration(sourceDir string, targetConfigDir string) (externalHookMigration, error) {
	settings, found, err := readExternalHookSettings(filepath.Join(sourceDir, "hooks.json"))
	if err != nil {
		return externalHookMigration{}, fmt.Errorf("invalid hooks config: %w", err)
	}
	if !found {
		return externalHookMigration{Groups: map[string][]externalHookGroup{}}, nil
	}
	sourceHooks, _ := settings["hooks"].(map[string]any)
	eventMappings := []struct{ source, target string }{
		{"preToolUse", "PreToolUse"}, {"postToolUse", "PostToolUse"}, {"preCompact", "PreCompact"}, {"postCompact", "PostCompact"},
		{"sessionStart", "SessionStart"}, {"subagentStart", "SubagentStart"}, {"subagentStop", "SubagentStop"}, {"beforeSubmitPrompt", "UserPromptSubmit"}, {"stop", "Stop"},
	}
	groups := map[string][]externalHookGroup{}
	for _, mapping := range eventMappings {
		handlers, _ := sourceHooks[mapping.source].([]any)
		for _, rawHandler := range handlers {
			handlerObject, ok := rawHandler.(map[string]any)
			if !ok || externalHookHasUnknownKeys(handlerObject, "command", "failClosed", "matcher", "statusMessage", "timeout", "timeoutSec", "type") {
				continue
			}
			handlerType := externalString(handlerObject["type"])
			if handlerType != "" && handlerType != "command" {
				continue
			}
			command := strings.TrimSpace(externalString(handlerObject["command"]))
			if command == "" {
				continue
			}
			handler := externalHookHandler{Type: "command", Command: rewriteExternalHookCommandForSource(command, targetConfigDir, externalCursorConfigDir)}
			if rawTimeout, found := handlerObject["timeout"]; found {
				handler.Timeout = externalHookUint64(rawTimeout)
			} else if rawTimeout, found := handlerObject["timeoutSec"]; found {
				handler.Timeout = externalHookUint64(rawTimeout)
			}
			if status := externalString(handlerObject["statusMessage"]); status != "" {
				status = rewriteCursorTerms(status)
				handler.StatusMessage = &status
			}
			group := externalHookGroup{Hooks: []externalHookHandler{handler}}
			if externalHookEventsWithMatchers[mapping.target] {
				if matcher, ok := handlerObject["matcher"].(string); ok {
					group.Matcher = &matcher
				}
			}
			groups[mapping.target] = append(groups[mapping.target], group)
		}
	}
	return externalHookMigration{Groups: groups}, nil
}

func (s *ConfigService) detectExternalCursorPluginsMigration(scope externalMigrationScope) (ExternalAgentConfigMigrationItem, bool) {
	if !scope.home() {
		return ExternalAgentConfigMigrationItem{}, false
	}
	groups, _ := s.externalCursorCachedPlugins()
	configuredIDs, configuredMarketplaces := s.externalConfiguredPlugins()
	details := NewMigrationDetails()
	for _, marketplace := range sortedExternalMapKeys(groups) {
		var names []string
		for _, name := range groups[marketplace] {
			if configuredIDs[name+"@"+marketplace] {
				continue
			}
			if available, configured := configuredMarketplaces[marketplace]; configured && !available[name] {
				continue
			}
			names = append(names, name)
		}
		if len(names) > 0 {
			details.Plugins = append(details.Plugins, PluginMigration{MarketplaceName: marketplace, PluginNames: names})
		}
	}
	if len(details.Plugins) == 0 {
		return ExternalAgentConfigMigrationItem{}, false
	}
	cache := filepath.Join(s.externalCursorHome(), "plugins", "cache")
	return ExternalAgentConfigMigrationItem{ItemType: MigrationPlugins, Description: fmt.Sprintf("Migrate cached plugins from %s", cache), Details: details}, true
}

func (s *ConfigService) externalCursorCachedPlugins() (map[string][]string, map[string]externalMarketplaceImportSource) {
	home := s.externalCursorHome()
	marketplacesRoot := filepath.Join(home, "plugins", "marketplaces")
	cacheRoot := filepath.Join(home, "plugins", "cache")
	groups := map[string][]string{}
	sources := map[string]externalMarketplaceImportSource{}
	entries, _ := os.ReadDir(marketplacesRoot)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		marketplaceRoot := filepath.Join(marketplacesRoot, entry.Name())
		manifest, ok := readJSONObject(filepath.Join(marketplaceRoot, ".cursor-plugin", "marketplace.json"))
		if !ok {
			continue
		}
		marketplaceName := strings.TrimSpace(externalString(manifest["name"]))
		if marketplaceName == "" {
			continue
		}
		available := map[string]bool{}
		if plugins, ok := manifest["plugins"].([]any); ok {
			for _, raw := range plugins {
				if pluginValue, ok := raw.(map[string]any); ok {
					if name := strings.TrimSpace(externalString(pluginValue["name"])); name != "" {
						available[name] = true
					}
				}
			}
		}
		cacheEntries, _ := os.ReadDir(filepath.Join(cacheRoot, entry.Name()))
		for _, cached := range cacheEntries {
			if cached.IsDir() && available[cached.Name()] {
				groups[marketplaceName] = append(groups[marketplaceName], cached.Name())
			}
		}
		if len(groups[marketplaceName]) > 0 {
			sort.Strings(groups[marketplaceName])
			sources[marketplaceName] = externalMarketplaceImportSource{Source: marketplaceRoot}
		}
	}
	return groups, sources
}

func (s *ConfigService) importExternalCursorPluginsMigration(item ExternalAgentConfigMigrationItem) ExternalAgentConfigImportTypeResult {
	result := ExternalAgentConfigImportTypeResult{ItemType: MigrationPlugins, Successes: []ExternalAgentConfigImportItemTypeSuccess{}, Failures: []ExternalAgentConfigImportItemTypeFailure{}}
	if item.Details == nil {
		return externalCoreImportFailure(result, item, "plugin_import", errors.New("plugins migration item is missing details"))
	}
	_, sources := s.externalCursorCachedPlugins()
	service := codexplugin.NewPluginService()
	service.SetCodexHome(s.codexHome)
	configured := map[string]bool{}
	for _, marketplace := range service.List(&codexplugin.PluginListParams{IncludeInstalled: true}).Marketplaces {
		configured[marketplace.Name] = true
	}
	for _, group := range item.Details.Plugins {
		marketplace := strings.TrimSpace(group.MarketplaceName)
		if !configured[marketplace] {
			source, found := sources[marketplace]
			if !found {
				for _, name := range group.PluginNames {
					appendExternalPluginFailure(&result, item.CWD, name+"@"+marketplace, "external agent plugin marketplace source was not found: "+marketplace)
				}
				continue
			}
			if _, err := service.AddMarketplace(&codexplugin.MarketplaceAddParams{Name: marketplace, Source: source.Source}); err != nil {
				for _, name := range group.PluginNames {
					appendExternalPluginFailure(&result, item.CWD, name+"@"+marketplace, err.Error())
				}
				continue
			}
			configured[marketplace] = true
		}
		for _, name := range group.PluginNames {
			pluginID := strings.TrimSpace(name) + "@" + marketplace
			installed, err := service.Install(&codexplugin.PluginInstallParams{PluginID: pluginID})
			if err != nil {
				appendExternalPluginFailure(&result, item.CWD, pluginID, err.Error())
				continue
			}
			target := pluginID
			if installed != nil && strings.TrimSpace(installed.PluginID) != "" {
				target = strings.TrimSpace(installed.PluginID)
			}
			result.Successes = append(result.Successes, externalMigrationSuccess(MigrationPlugins, pluginID, target, item.CWD))
		}
	}
	return result
}
