package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/pelletier/go-toml/v2"
)

type externalCommandMigration struct {
	SourcePath  string
	SourceName  string
	Name        string
	Description string
	Body        string
}

type externalSubagentMigration struct {
	SourcePath     string
	Name           string
	Description    string
	PermissionMode string
	Effort         string
	Body           string
}

func (s *ConfigService) detectExternalMCPMigration(scope externalMigrationScope) (ExternalAgentConfigMigrationItem, bool) {
	migrated := s.buildExternalMCPMigration(scope)
	servers, _ := migrated["mcp_servers"].(map[string]any)
	if len(servers) == 0 {
		return ExternalAgentConfigMigrationItem{}, false
	}
	target := s.externalTargetConfigPath(scope)
	missing := missingExternalMCPServers(target, servers)
	if len(missing) == 0 {
		return ExternalAgentConfigMigrationItem{}, false
	}
	details := NewMigrationDetails()
	for _, name := range missing {
		details.MCPServers = append(details.MCPServers, NamedMigration{Name: name})
	}
	return ExternalAgentConfigMigrationItem{
		ItemType:    MigrationMCPServerConfig,
		Description: fmt.Sprintf("Migrate MCP servers from %s into %s", s.externalSourceRoot(scope), target),
		CWD:         externalScopeCWD(scope),
		Details:     details,
	}, true
}

func (s *ConfigService) detectExternalCommandsMigration(scope externalMigrationScope) (ExternalAgentConfigMigrationItem, bool) {
	source, target := s.externalCommandsPaths(scope)
	commands := discoverExternalCommands(source, target)
	if len(commands) == 0 {
		return ExternalAgentConfigMigrationItem{}, false
	}
	details := NewMigrationDetails()
	for _, command := range commands {
		details.Commands = append(details.Commands, NamedMigration{Name: command.Name})
	}
	return ExternalAgentConfigMigrationItem{
		ItemType:    MigrationCommands,
		Description: fmt.Sprintf("Migrate commands from %s to %s", source, target),
		CWD:         externalScopeCWD(scope),
		Details:     details,
	}, true
}

func (s *ConfigService) detectExternalSubagentsMigration(scope externalMigrationScope) (ExternalAgentConfigMigrationItem, bool) {
	source, target := s.externalSubagentPaths(scope)
	agents := discoverExternalSubagents(source, target)
	if len(agents) == 0 {
		return ExternalAgentConfigMigrationItem{}, false
	}
	details := NewMigrationDetails()
	for _, agent := range agents {
		details.Subagents = append(details.Subagents, NamedMigration{Name: agent.Name})
	}
	return ExternalAgentConfigMigrationItem{
		ItemType:    MigrationSubagents,
		Description: fmt.Sprintf("Migrate subagents from %s to %s", source, target),
		CWD:         externalScopeCWD(scope),
		Details:     details,
	}, true
}

func (s *ConfigService) importExternalToolsMigration(item ExternalAgentConfigMigrationItem) ExternalAgentConfigImportTypeResult {
	result := ExternalAgentConfigImportTypeResult{ItemType: item.ItemType, Successes: []ExternalAgentConfigImportItemTypeSuccess{}, Failures: []ExternalAgentConfigImportItemTypeFailure{}}
	scope, ok := externalScopeFromCWD(item.CWD)
	if !ok {
		return externalCoreImportFailure(result, item, "scope_resolution", fmt.Errorf("migration working directory does not exist"))
	}
	var err error
	switch item.ItemType {
	case MigrationMCPServerConfig:
		err = s.importExternalMCP(scope, &result)
	case MigrationCommands:
		err = s.importExternalCommands(scope, &result)
	case MigrationSubagents:
		err = s.importExternalSubagents(scope, &result)
	default:
		err = fmt.Errorf("unsupported tools migration type %s", item.ItemType)
	}
	if err != nil {
		return externalCoreImportFailure(result, item, strings.ToLower(string(item.ItemType))+"_import", err)
	}
	return result
}

func (s *ConfigService) importExternalMCP(scope externalMigrationScope, result *ExternalAgentConfigImportTypeResult) error {
	target := s.externalTargetConfigPath(scope)
	migrated := s.buildExternalMCPMigration(scope)
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
	for _, name := range missing {
		result.Successes = append(result.Successes, externalMigrationSuccess(MigrationMCPServerConfig, name, name, externalScopeCWD(scope)))
	}
	return nil
}

func (s *ConfigService) importExternalCommands(scope externalMigrationScope, result *ExternalAgentConfigImportTypeResult) error {
	source, target := s.externalCommandsPaths(scope)
	commands := discoverExternalCommands(source, target)
	for _, command := range commands {
		dir := filepath.Join(target, command.Name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		rendered := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n# %s\n\nUse this skill when the user asks to run the migrated source command `%s`.\n\n## Command Template\n\n%s\n",
			yamlQuoted(command.Name), yamlQuoted(rewriteClaudeTerms(command.Description)), command.Name, command.SourceName, externalBodyOrFallback(rewriteClaudeTerms(strings.TrimSpace(command.Body)), "No command template body was found."))
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(rendered), 0o600); err != nil {
			return err
		}
		result.Successes = append(result.Successes, externalMigrationSuccess(MigrationCommands, command.Name, command.Name, externalScopeCWD(scope)))
	}
	return nil
}

func (s *ConfigService) importExternalSubagents(scope externalMigrationScope, result *ExternalAgentConfigImportTypeResult) error {
	_, target := s.externalSubagentPaths(scope)
	agents := discoverExternalSubagentsForScope(s, scope, target)
	if len(agents) == 0 {
		return nil
	}
	if err := os.MkdirAll(target, 0o700); err != nil {
		return err
	}
	for _, agent := range agents {
		value := map[string]any{
			"name":                   agent.Name,
			"description":            rewriteClaudeTerms(agent.Description),
			"developer_instructions": externalBodyOrFallback(rewriteClaudeTerms(strings.TrimSpace(agent.Body)), "No subagent instructions were found."),
		}
		if effort := externalAgentEffort(agent.Effort); effort != "" {
			value["model_reasoning_effort"] = effort
		}
		if sandbox := externalAgentSandbox(agent.PermissionMode); sandbox != "" {
			value["sandbox_mode"] = sandbox
		}
		data, err := toml.Marshal(value)
		if err != nil {
			return err
		}
		if len(data) == 0 || data[len(data)-1] != '\n' {
			data = append(data, '\n')
		}
		if err := os.WriteFile(filepath.Join(target, strings.TrimSuffix(filepath.Base(agent.SourcePath), filepath.Ext(agent.SourcePath))+".toml"), data, 0o600); err != nil {
			return err
		}
		result.Successes = append(result.Successes, externalMigrationSuccess(MigrationSubagents, agent.Name, agent.Name, externalScopeCWD(scope)))
	}
	return nil
}

func (s *ConfigService) externalSourceRoot(scope externalMigrationScope) string {
	if scope.home() {
		return filepath.Dir(s.externalAgentHome)
	}
	return scope.repoRoot
}

func (s *ConfigService) externalCommandsPaths(scope externalMigrationScope) (string, string) {
	if scope.home() {
		return filepath.Join(s.externalAgentHome, "commands"), filepath.Join(filepath.Dir(s.codexHome), ".agents", "skills")
	}
	return filepath.Join(scope.repoRoot, externalClaudeConfigDir, "commands"), filepath.Join(scope.repoRoot, ".agents", "skills")
}

func (s *ConfigService) externalSubagentPaths(scope externalMigrationScope) (string, string) {
	if scope.home() {
		return filepath.Join(s.externalAgentHome, "agents"), filepath.Join(s.codexHome, "agents")
	}
	return filepath.Join(scope.repoRoot, externalClaudeConfigDir, "agents"), filepath.Join(scope.repoRoot, ".codex", "agents")
}

func (s *ConfigService) buildExternalMCPMigration(scope externalMigrationScope) map[string]any {
	root := s.externalSourceRoot(scope)
	settings, _ := readEffectiveClaudeSettings(s.externalSettingsPath(scope))
	enabled := externalStringSet(settings["enabledMcpjsonServers"])
	disabled := externalStringSet(settings["disabledMcpjsonServers"])
	servers := map[string]any{}
	for _, source := range []string{filepath.Join(root, ".mcp.json"), filepath.Join(root, ".claude.json")} {
		value, ok := readJSONObject(source)
		if !ok {
			continue
		}
		appendExternalMCPServers(servers, value, true)
		if projects, ok := value["projects"].(map[string]any); ok {
			for projectPath, raw := range projects {
				project, ok := raw.(map[string]any)
				if ok && externalSamePath(projectPath, root) {
					appendExternalMCPServers(servers, project, true)
				}
			}
		}
	}
	converted := map[string]any{}
	names := sortedExternalMapKeys(servers)
	for _, name := range names {
		raw, _ := servers[name].(map[string]any)
		if len(enabled) > 0 && !enabled[name] || disabled[name] || externalBool(raw["disabled"]) || raw["enabled"] == false {
			continue
		}
		if server, ok := convertExternalMCPServer(raw); ok {
			converted[name] = server
		}
	}
	if len(converted) == 0 {
		return map[string]any{}
	}
	return map[string]any{"mcp_servers": converted}
}

func appendExternalMCPServers(target map[string]any, value map[string]any, overwrite bool) {
	servers, _ := value["mcpServers"].(map[string]any)
	for name, server := range servers {
		if _, found := target[name]; !found || overwrite {
			target[name] = server
		}
	}
}

func convertExternalMCPServer(raw map[string]any) (map[string]any, bool) {
	typeName, _ := raw["type"].(string)
	if command := externalString(raw["command"]); command != "" {
		if typeName != "" && typeName != "stdio" || strings.Contains(command, "${") {
			return nil, false
		}
		server := map[string]any{"command": command}
		if args := externalStrings(raw["args"]); len(args) > 0 {
			for _, arg := range args {
				if strings.Contains(arg, "${") {
					return nil, false
				}
			}
			server["args"] = args
		}
		if env, ok := raw["env"].(map[string]any); ok {
			static, inherited := map[string]any{}, []string{}
			for key, value := range env {
				text := externalString(value)
				if text == "${"+key+"}" {
					inherited = append(inherited, key)
				} else if strings.Contains(text, "${") {
					return nil, false
				} else {
					static[key] = text
				}
			}
			sort.Strings(inherited)
			if len(static) > 0 {
				server["env"] = static
			}
			if len(inherited) > 0 {
				server["env_vars"] = inherited
			}
		}
		return server, true
	}
	if url := externalString(raw["url"]); url != "" {
		if typeName != "" && typeName != "http" && typeName != "streamable_http" || strings.Contains(url, "${") {
			return nil, false
		}
		server := map[string]any{"url": url}
		static, inherited := map[string]any{}, map[string]any{}
		if headers, ok := raw["headers"].(map[string]any); ok {
			for key, value := range headers {
				text := externalString(value)
				if strings.EqualFold(key, "authorization") && strings.HasPrefix(text, "Bearer ${") && strings.HasSuffix(text, "}") {
					server["bearer_token_env_var"] = strings.TrimSuffix(strings.TrimPrefix(text, "Bearer ${"), "}")
				} else if strings.HasPrefix(text, "${") && strings.HasSuffix(text, "}") && strings.Count(text, "${") == 1 {
					inherited[key] = strings.TrimSuffix(strings.TrimPrefix(text, "${"), "}")
				} else if strings.Contains(text, "${") {
					return nil, false
				} else {
					static[key] = text
				}
			}
		}
		if len(static) > 0 {
			server["http_headers"] = static
		}
		if len(inherited) > 0 {
			server["env_http_headers"] = inherited
		}
		return server, true
	}
	return nil, false
}

func missingExternalMCPServers(target string, servers map[string]any) []string {
	existing := map[string]any{}
	if data, err := os.ReadFile(target); err == nil {
		_ = toml.Unmarshal(data, &existing)
	}
	existingServers, _ := existing["mcp_servers"].(map[string]any)
	missing := make([]string, 0, len(servers))
	for name := range servers {
		if _, found := existingServers[name]; !found {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return missing
}

func discoverExternalCommands(source string, target string) []externalCommandMigration {
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
		frontmatter, body, ok := parseExternalFrontmatter(string(data))
		description := strings.TrimSpace(frontmatter["description"])
		if !ok || description == "" || externalUnsupportedTemplate(body) {
			continue
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			continue
		}
		sourceName := strings.TrimSuffix(filepath.ToSlash(relative), filepath.Ext(relative))
		sourceName = strings.ReplaceAll(sourceName, "/", "-")
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

func discoverExternalSubagents(source string, target string) []externalSubagentMigration {
	entries, err := os.ReadDir(source)
	if err != nil {
		return nil
	}
	var out []externalSubagentMigration
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".md") || strings.EqualFold(entry.Name(), "README.md") || pathExists(filepath.Join(target, strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))+".toml")) {
			continue
		}
		path := filepath.Join(source, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		frontmatter, body, ok := parseExternalFrontmatter(string(data))
		name, description := strings.TrimSpace(frontmatter["name"]), strings.TrimSpace(frontmatter["description"])
		if !ok || name == "" || description == "" || strings.TrimSpace(body) == "" {
			continue
		}
		out = append(out, externalSubagentMigration{SourcePath: path, Name: name, Description: description, PermissionMode: frontmatter["permissionMode"], Effort: frontmatter["effort"], Body: body})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SourcePath < out[j].SourcePath })
	return out
}

func discoverExternalSubagentsForScope(s *ConfigService, scope externalMigrationScope, target string) []externalSubagentMigration {
	source, _ := s.externalSubagentPaths(scope)
	return discoverExternalSubagents(source, target)
}

func parseExternalFrontmatter(content string) (map[string]string, string, bool) {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(normalized, "---\n") {
		return nil, content, false
	}
	rest := strings.TrimPrefix(normalized, "---\n")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil, content, false
	}
	bodyStart := end + len("\n---")
	if bodyStart < len(rest) && rest[bodyStart] == '\n' {
		bodyStart++
	}
	values := map[string]string{}
	for _, line := range strings.Split(rest[:end], "\n") {
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		value = strings.TrimSpace(value)
		if strings.HasPrefix(value, "[") || strings.HasPrefix(value, "{") {
			continue
		}
		values[strings.TrimSpace(key)] = strings.Trim(value, `"'`)
	}
	return values, rest[bodyStart:], true
}

func externalUnsupportedTemplate(body string) bool {
	if strings.Contains(body, "$ARGUMENTS") || strings.Contains(body, "!`") || strings.Contains(body, "! `") || strings.Contains(body, "{{") && strings.Contains(body, "}}") {
		return true
	}
	for index := 1; index <= 9; index++ {
		if strings.Contains(body, "$"+strconv.Itoa(index)) {
			return true
		}
	}
	for _, token := range strings.Fields(body) {
		if strings.HasPrefix(token, "@") && len(token) > 1 {
			return true
		}
	}
	return false
}

func externalSlug(value string) string {
	var out strings.Builder
	lastDash := false
	for _, ch := range value {
		if ch <= unicode.MaxASCII && (unicode.IsLetter(ch) || unicode.IsDigit(ch)) {
			out.WriteRune(unicode.ToLower(ch))
			lastDash = false
		} else if !lastDash {
			out.WriteByte('-')
			lastDash = true
		}
	}
	value = strings.Trim(out.String(), "-")
	if value == "" {
		return "migrated"
	}
	return value
}

func externalAgentEffort(value string) string {
	value = strings.TrimSpace(value)
	if value == "max" {
		value = "xhigh"
	}
	switch value {
	case "none", "minimal", "low", "medium", "high", "xhigh":
		return value
	default:
		return ""
	}
}

func externalAgentSandbox(value string) string {
	switch strings.TrimSpace(value) {
	case "acceptEdits":
		return "workspace-write"
	case "readOnly":
		return "read-only"
	default:
		return ""
	}
}

func externalBodyOrFallback(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func yamlQuoted(value string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`) + `"`
}

func externalString(value any) string {
	switch value := value.(type) {
	case string:
		return value
	case bool, float64, json.Number:
		return fmt.Sprint(value)
	default:
		return ""
	}
}

func externalStrings(value any) []string {
	if values, ok := value.([]any); ok {
		out := make([]string, 0, len(values))
		for _, value := range values {
			if text := externalString(value); text != "" {
				out = append(out, text)
			}
		}
		return out
	}
	if value := externalString(value); value != "" {
		return []string{value}
	}
	return nil
}

func externalStringSet(value any) map[string]bool {
	out := map[string]bool{}
	for _, value := range externalStrings(value) {
		out[value] = true
	}
	return out
}

func externalBool(value any) bool {
	valueBool, _ := value.(bool)
	return valueBool
}

func externalSamePath(left string, right string) bool {
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	return leftErr == nil && rightErr == nil && strings.EqualFold(filepath.Clean(leftAbs), filepath.Clean(rightAbs))
}

func sortedExternalMapKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
