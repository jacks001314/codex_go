package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var externalHookEventNames = []string{
	"PreToolUse",
	"PermissionRequest",
	"PostToolUse",
	"PreCompact",
	"PostCompact",
	"SessionStart",
	"SessionEnd",
	"UserPromptSubmit",
	"SubagentStart",
	"SubagentStop",
	"Stop",
}

var externalHookEventsWithMatchers = map[string]bool{
	"PreToolUse":        true,
	"PermissionRequest": true,
	"PostToolUse":       true,
	"PreCompact":        true,
	"PostCompact":       true,
	"SessionStart":      true,
	"SessionEnd":        true,
	"SubagentStart":     true,
	"SubagentStop":      true,
}

type externalHookMigration struct {
	Groups map[string][]externalHookGroup
}

type externalHookFile struct {
	Hooks externalHookPayload `json:"hooks"`
}

type externalHookPayload struct {
	PreToolUse        []externalHookGroup `json:"PreToolUse,omitempty"`
	PermissionRequest []externalHookGroup `json:"PermissionRequest,omitempty"`
	PostToolUse       []externalHookGroup `json:"PostToolUse,omitempty"`
	PreCompact        []externalHookGroup `json:"PreCompact,omitempty"`
	PostCompact       []externalHookGroup `json:"PostCompact,omitempty"`
	SessionStart      []externalHookGroup `json:"SessionStart,omitempty"`
	SessionEnd        []externalHookGroup `json:"SessionEnd,omitempty"`
	UserPromptSubmit  []externalHookGroup `json:"UserPromptSubmit,omitempty"`
	SubagentStart     []externalHookGroup `json:"SubagentStart,omitempty"`
	SubagentStop      []externalHookGroup `json:"SubagentStop,omitempty"`
	Stop              []externalHookGroup `json:"Stop,omitempty"`
}

type externalHookGroup struct {
	Matcher *string               `json:"matcher,omitempty"`
	Hooks   []externalHookHandler `json:"hooks"`
}

type externalHookHandler struct {
	Type          string  `json:"type"`
	Command       string  `json:"command"`
	Timeout       *uint64 `json:"timeout,omitempty"`
	StatusMessage *string `json:"statusMessage,omitempty"`
}

func (s *ConfigService) detectExternalHooksMigration(scope externalMigrationScope) (ExternalAgentConfigMigrationItem, bool) {
	source, target := s.externalHooksPaths(scope)
	if !missingOrEmptyTextFile(target) {
		return ExternalAgentConfigMigrationItem{}, false
	}
	migration, err := buildExternalHooksMigration(source, filepath.Dir(target))
	if err != nil || len(migration.Groups) == 0 {
		return ExternalAgentConfigMigrationItem{}, false
	}
	details := NewMigrationDetails()
	for _, eventName := range externalHookEventNames {
		if len(migration.Groups[eventName]) > 0 {
			details.Hooks = append(details.Hooks, NamedMigration{Name: eventName})
		}
	}
	return ExternalAgentConfigMigrationItem{
		ItemType:    MigrationHooks,
		Description: fmt.Sprintf("Migrate hooks from %s to %s", source, target),
		CWD:         externalScopeCWD(scope),
		Details:     details,
	}, true
}

func (s *ConfigService) importExternalHooksMigration(item ExternalAgentConfigMigrationItem) ExternalAgentConfigImportTypeResult {
	result := ExternalAgentConfigImportTypeResult{
		ItemType:  MigrationHooks,
		Successes: []ExternalAgentConfigImportItemTypeSuccess{},
		Failures:  []ExternalAgentConfigImportItemTypeFailure{},
	}
	scope, ok := externalScopeFromCWD(item.CWD)
	if !ok {
		return externalCoreImportFailure(result, item, "scope_resolution", errors.New("migration working directory does not exist"))
	}
	source, target := s.externalHooksPaths(scope)
	if !missingOrEmptyTextFile(target) {
		return result
	}
	migration, err := buildExternalHooksMigration(source, filepath.Dir(target))
	if err != nil {
		return externalCoreImportFailure(result, item, "hooks_import", err)
	}
	if len(migration.Groups) == 0 {
		return result
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return externalCoreImportFailure(result, item, "hooks_import", err)
	}
	if err := copyExternalHookScripts(filepath.Join(source, "hooks"), filepath.Join(filepath.Dir(target), "hooks")); err != nil {
		return externalCoreImportFailure(result, item, "hooks_import", err)
	}
	data, err := json.MarshalIndent(externalHookFile{Hooks: externalHookPayloadFromGroups(migration.Groups)}, "", "  ")
	if err != nil {
		return externalCoreImportFailure(result, item, "hooks_import", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(target, data, 0o600); err != nil {
		return externalCoreImportFailure(result, item, "hooks_import", err)
	}
	for _, eventName := range externalHookEventNames {
		if len(migration.Groups[eventName]) > 0 {
			result.Successes = append(result.Successes, externalMigrationSuccess(MigrationHooks, eventName, eventName, externalScopeCWD(scope)))
		}
	}
	return result
}

func (s *ConfigService) externalHooksPaths(scope externalMigrationScope) (string, string) {
	if scope.home() {
		return s.externalAgentHome, filepath.Join(s.codexHome, "hooks.json")
	}
	return filepath.Join(scope.repoRoot, externalClaudeConfigDir), filepath.Join(scope.repoRoot, ".gcode", "hooks.json")
}

func buildExternalHooksMigration(sourceDir string, targetConfigDir string) (externalHookMigration, error) {
	settingsFiles := make([]map[string]any, 0, 2)
	var disableAllHooks *bool
	for _, name := range []string{"settings.json", "settings.local.json"} {
		path := filepath.Join(sourceDir, name)
		settings, found, err := readExternalHookSettings(path)
		if err != nil {
			return externalHookMigration{}, fmt.Errorf("invalid hooks settings: %w", err)
		}
		if !found {
			continue
		}
		if disabled, ok := settings["disableAllHooks"].(bool); ok {
			disableAllHooks = &disabled
		}
		settingsFiles = append(settingsFiles, settings)
	}
	if disableAllHooks != nil && *disableAllHooks {
		return externalHookMigration{Groups: map[string][]externalHookGroup{}}, nil
	}

	groups := map[string][]externalHookGroup{}
	for _, settings := range settingsFiles {
		hooks, _ := settings["hooks"].(map[string]any)
		for _, eventName := range externalHookEventNames {
			rawGroups, _ := hooks[eventName].([]any)
			for _, rawGroup := range rawGroups {
				groupObject, ok := rawGroup.(map[string]any)
				if !ok || externalHookHasUnknownKeys(groupObject, "matcher", "hooks") {
					continue
				}
				rawHandlers, _ := groupObject["hooks"].([]any)
				handlers := make([]externalHookHandler, 0, len(rawHandlers))
				for _, rawHandler := range rawHandlers {
					handlerObject, ok := rawHandler.(map[string]any)
					if !ok || externalHookHasUnknownKeys(handlerObject, "type", "command", "timeout", "timeoutSec", "statusMessage", "async") {
						continue
					}
					handlerType, ok := handlerObject["type"].(string)
					if !ok {
						handlerType = "command"
					}
					if handlerType != "command" || externalBool(handlerObject["async"]) {
						continue
					}
					command, _ := handlerObject["command"].(string)
					command = strings.TrimSpace(command)
					if command == "" {
						continue
					}
					handler := externalHookHandler{
						Type:    "command",
						Command: rewriteExternalHookCommand(command, targetConfigDir),
					}
					if rawTimeout, found := handlerObject["timeout"]; found {
						handler.Timeout = externalHookUint64(rawTimeout)
					} else if rawTimeout, found := handlerObject["timeoutSec"]; found {
						handler.Timeout = externalHookUint64(rawTimeout)
					}
					if statusMessage, ok := handlerObject["statusMessage"].(string); ok {
						statusMessage = rewriteClaudeTerms(statusMessage)
						handler.StatusMessage = &statusMessage
					}
					handlers = append(handlers, handler)
				}
				if len(handlers) == 0 {
					continue
				}
				group := externalHookGroup{Hooks: handlers}
				if externalHookEventsWithMatchers[eventName] {
					if matcher, ok := groupObject["matcher"].(string); ok {
						group.Matcher = &matcher
					}
				}
				groups[eventName] = append(groups[eventName], group)
			}
		}
	}
	return externalHookMigration{Groups: groups}, nil
}

func readExternalHookSettings(path string) (map[string]any, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, false, err
	}
	settings, _ := value.(map[string]any)
	if settings == nil {
		settings = map[string]any{}
	}
	return settings, true, nil
}

func externalHookHasUnknownKeys(value map[string]any, allowed ...string) bool {
	allowedKeys := make(map[string]bool, len(allowed))
	for _, key := range allowed {
		allowedKeys[key] = true
	}
	for key := range value {
		if !allowedKeys[key] {
			return true
		}
	}
	return false
}

func externalHookUint64(value any) *uint64 {
	var text string
	switch value := value.(type) {
	case json.Number:
		text = value.String()
	case string:
		text = value
	default:
		return nil
	}
	parsed, err := strconv.ParseUint(text, 10, 64)
	if err != nil {
		return nil
	}
	return &parsed
}

func externalHookPayloadFromGroups(groups map[string][]externalHookGroup) externalHookPayload {
	return externalHookPayload{
		PreToolUse:        groups["PreToolUse"],
		PermissionRequest: groups["PermissionRequest"],
		PostToolUse:       groups["PostToolUse"],
		PreCompact:        groups["PreCompact"],
		PostCompact:       groups["PostCompact"],
		SessionStart:      groups["SessionStart"],
		SessionEnd:        groups["SessionEnd"],
		UserPromptSubmit:  groups["UserPromptSubmit"],
		SubagentStart:     groups["SubagentStart"],
		SubagentStop:      groups["SubagentStop"],
		Stop:              groups["Stop"],
	}
}

func copyExternalHookScripts(source string, target string) error {
	if err := rejectRedirectedExternalTarget(target); err != nil {
		return err
	}
	entries, err := os.ReadDir(source)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := os.MkdirAll(target, 0o700); err != nil {
		return err
	}
	for _, entry := range entries {
		from := filepath.Join(source, entry.Name())
		to := filepath.Join(target, entry.Name())
		if entry.IsDir() {
			if err := copyExternalHookScripts(from, to); err != nil {
				return err
			}
			continue
		}
		if !entry.Type().IsRegular() || pathExists(to) {
			continue
		}
		data, err := os.ReadFile(from)
		if err != nil {
			return err
		}
		if err := os.WriteFile(to, data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func rewriteExternalHookCommand(command string, targetConfigDir string) string {
	return rewriteExternalHookCommandForSource(command, targetConfigDir, externalClaudeConfigDir)
}

func rewriteExternalHookCommandForSource(command string, targetConfigDir string, sourceConfigDir string) string {
	if strings.Contains(command, `.claude\hooks\`) || strings.Contains(command, "%CLAUDE_PROJECT_DIR%") || strings.Contains(command, "$env:CLAUDE_PROJECT_DIR") {
		return command
	}
	targetHooksDir := filepath.Join(targetConfigDir, "hooks")
	sourceHooksPath := strings.TrimSpace(sourceConfigDir) + "/hooks/"
	rewritten := replaceQuotedExternalHookPaths(command, '\'', sourceHooksPath, targetHooksDir)
	rewritten = replaceQuotedExternalHookPaths(rewritten, '"', sourceHooksPath, targetHooksDir)
	return replaceUnquotedExternalHookPaths(rewritten, sourceHooksPath, targetHooksDir)
}

func replaceQuotedExternalHookPaths(command string, quote byte, sourceHooksPath string, targetHooksDir string) string {
	rewritten := command
	searchStart := 0
	for searchStart < len(rewritten) {
		relativeStart := strings.IndexByte(rewritten[searchStart:], quote)
		if relativeStart < 0 {
			break
		}
		start := searchStart + relativeStart
		contentStart := start + 1
		relativeEnd := strings.IndexByte(rewritten[contentStart:], quote)
		if relativeEnd < 0 {
			break
		}
		end := contentStart + relativeEnd
		content := rewritten[contentStart:end]
		sourceStart := strings.Index(content, sourceHooksPath)
		if sourceStart < 0 {
			searchStart = end + 1
			continue
		}
		suffix := content[sourceStart+len(sourceHooksPath):]
		replacement, ok := externalHookPathReplacement(targetHooksDir, content, sourceStart, suffix)
		if !ok {
			searchStart = end + 1
			continue
		}
		rewritten = rewritten[:start] + replacement + rewritten[end+1:]
		searchStart = start + len(replacement)
	}
	return rewritten
}

func replaceUnquotedExternalHookPaths(command string, sourceHooksPath string, targetHooksDir string) string {
	rewritten := command
	searchStart := 0
	for {
		sourceStart := findUnquotedExternalHookPath(rewritten, sourceHooksPath, searchStart)
		if sourceStart < 0 {
			return rewritten
		}
		pathStart := externalShellPathStart(rewritten, sourceStart)
		pathEnd := externalShellPathEnd(rewritten, sourceStart+len(sourceHooksPath))
		if pathStart > 0 && rewritten[pathStart-1] == '=' {
			searchStart = sourceStart + len(sourceHooksPath)
			continue
		}
		path := rewritten[pathStart:pathEnd]
		suffix := rewritten[sourceStart+len(sourceHooksPath) : pathEnd]
		replacement, ok := externalHookPathReplacement(targetHooksDir, path, sourceStart-pathStart, suffix)
		if !ok {
			searchStart = sourceStart + len(sourceHooksPath)
			continue
		}
		rewritten = rewritten[:pathStart] + replacement + rewritten[pathEnd:]
		searchStart = pathStart + len(replacement)
	}
}

func findUnquotedExternalHookPath(command string, sourceHooksPath string, start int) int {
	inSingleQuote := false
	inDoubleQuote := false
	escaped := false
	for index := start; index < len(command); index++ {
		ch := command[index]
		if escaped {
			escaped = false
			continue
		}
		if !inSingleQuote && ch == '\\' {
			escaped = true
			continue
		}
		switch {
		case ch == '\'' && !inDoubleQuote:
			inSingleQuote = !inSingleQuote
		case ch == '"' && !inSingleQuote:
			inDoubleQuote = !inDoubleQuote
		case !inSingleQuote && !inDoubleQuote && strings.HasPrefix(command[index:], sourceHooksPath):
			return index
		}
	}
	return -1
}

func externalShellPathStart(command string, end int) int {
	start := 0
	for index := 0; index < end; index++ {
		if externalShellPathBoundary(command[index]) {
			start = index + 1
		}
	}
	return start
}

func externalShellPathEnd(command string, start int) int {
	escaped := false
	for index := start; index < len(command); index++ {
		ch := command[index]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if externalShellPathBoundary(ch) {
			return index
		}
	}
	return len(command)
}

func externalShellPathBoundary(ch byte) bool {
	return ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' || strings.ContainsRune("=;|&<>()", rune(ch))
}

func externalHookPathReplacement(targetHooksDir string, path string, sourceStart int, suffix string) (string, bool) {
	prefix := path[:sourceStart]
	pure := prefix == "" || prefix == "./" || strings.HasSuffix(prefix, "/")
	if !pure || strings.IndexFunc(prefix, func(ch rune) bool {
		return ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' || strings.ContainsRune("=;|&<>()", ch)
	}) >= 0 || suffix == "" || strings.ContainsAny(suffix, "\\$`*?[{}") {
		return "", false
	}
	return externalShellSingleQuote(filepath.Join(targetHooksDir, filepath.FromSlash(suffix))), true
}

func externalShellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
