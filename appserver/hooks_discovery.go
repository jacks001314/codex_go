package appserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"codex_go/config"
	"codex_go/features"
	"codex_go/plugin"
)

const defaultDiscoveredHookTimeoutSec int64 = 600

// Mirrors Rust hooks/src/events/session_end.rs and output_spill.rs constants.
const (
	sessionEndHookDefaultTimeoutSec int64 = 1
	sessionEndHookMaxTimeoutSec     int64 = 3
	defaultHookOutputTokenLimit     int64 = 2500
)

type HookDiscoveryService struct {
	CodexHome         string
	Config            *config.ConfigService
	States            map[string]*HookState
	BypassTrust       bool
	PluginHookSources []plugin.HookSource
	// McpToolHooksEnabled lists mcp_tool hooks instead of warning that MCP
	// invocation is not available yet (Rust hooks engine #38705). The runner
	// executes them only when a McpToolHookExecutor is configured.
	McpToolHooksEnabled bool
}

func NewHookDiscoveryService(codexHome string) *HookDiscoveryService {
	return &HookDiscoveryService{CodexHome: codexHome}
}

type HookState struct {
	Enabled     *bool
	TrustedHash *string
}

func (s *HookDiscoveryService) Discover(params *HookListParams, defaultCWD string) *HookListResponse {
	cwds := hookDiscoveryCWDs(params, defaultCWD)
	response := &HookListResponse{Data: make([]HookListEntry, 0, len(cwds))}
	for _, cwd := range cwds {
		entry := HookListEntry{CWD: cwd}
		if !s.hooksFeatureEnabled(cwd) {
			response.Data = append(response.Data, entry)
			continue
		}
		s.appendUserHooks(&entry)
		s.appendProjectHooks(&entry, cwd)
		s.appendPluginHooks(&entry)
		s.appendManagedRequirementHooks(&entry)
		sortHooks(entry.Hooks)
		response.Data = append(response.Data, entry)
	}
	return response
}

// ManagedRequiredHookLoadErrors returns the required load errors for managed
// hooks discovered for the given working directory. This is the fail-closed
// signal callers can use before starting a session (Rust #38394).
func (s *HookDiscoveryService) ManagedRequiredHookLoadErrors(cwd string) []string {
	response := s.Discover(&HookListParams{CWDs: []string{strings.TrimSpace(cwd)}}, "")
	if response == nil || len(response.Data) == 0 {
		return nil
	}
	return append([]string(nil), response.Data[0].RequiredLoadErrors...)
}

func (s *HookDiscoveryService) appendManagedRequirementHooks(entry *HookListEntry) {
	if s == nil || s.Config == nil || entry == nil {
		return
	}
	read := s.Config.Requirements()
	if read == nil || read.Requirements == nil || read.Requirements.Hooks == nil {
		return
	}
	managed := read.Requirements.Hooks
	events := []struct {
		name   HookEventName
		groups []config.ConfiguredHookGroup
	}{
		{HookEventPreToolUse, managed.PreToolUse},
		{HookEventPermissionRequest, managed.PermissionRequest},
		{HookEventPostToolUse, managed.PostToolUse},
		{HookEventPreCompact, managed.PreCompact},
		{HookEventPostCompact, managed.PostCompact},
		{HookEventSessionStart, managed.SessionStart},
		{HookEventSessionEnd, managed.SessionEnd},
		{HookEventUserPromptSubmit, managed.UserPromptSubmit},
		{HookEventSubagentStart, managed.SubagentStart},
		{HookEventSubagentStop, managed.SubagentStop},
		{HookEventStop, managed.Stop},
		{HookEventInterrupt, managed.Interrupt},
	}
	displayOrder := int64(len(entry.Hooks))
	for _, event := range events {
		for groupIndex, group := range event.groups {
			for handlerIndex, handler := range group.Hooks {
				key := hookDiscoveryKey("managed", event.name, groupIndex, handlerIndex)
				switch handler.Type {
				case string(HookHandlerCommand):
					command := strings.TrimSpace(handler.Command)
					if command == "" {
						entry.RequiredLoadErrors = append(entry.RequiredLoadErrors, fmt.Sprintf("skipping empty hook command in managed requirements for %s", event.name))
						continue
					}
					timeout := hookNormalizedCommandTimeout(event.name, handler.TimeoutSec)
					entry.Hooks = append(entry.Hooks, HookMetadata{
						Key:           key,
						EventName:     event.name,
						HandlerType:   HookHandlerCommand,
						ExecutionMode: hookDiscoveryExecutionMode(handler.Async, event.name),
						Matcher:       cloneStringPtrAppserver(group.Matcher),
						Command:       &command,
						TimeoutSec:    timeout,
						StatusMessage: cloneStringPtrAppserver(handler.StatusMessage),
						SourcePath:    "managed",
						Source:        HookSourceCloudRequirements,
						DisplayOrder:  displayOrder,
						Enabled:       true,
						IsManaged:     true,
						CurrentHash:   hookDiscoveryHash(event.name, group.Matcher, handler.Command, handler.Async, timeout, handler.StatusMessage, nil),
						TrustStatus:   HookTrustManaged,
					})
					displayOrder++
				case string(HookHandlerMCPTool), string(HookHandlerPrompt), string(HookHandlerAgent):
					entry.RequiredLoadErrors = append(entry.RequiredLoadErrors, fmt.Sprintf("skipping unsupported managed hook %s in %s", handler.Type, event.name))
				}
			}
		}
	}
}

func (s *HookDiscoveryService) hooksFeatureEnabled(cwd string) bool {
	if s == nil || s.Config == nil {
		return true
	}
	cwd = strings.TrimSpace(cwd)
	readParams := &config.ConfigReadParams{}
	if cwd != "" {
		readParams.CWD = &cwd
	}
	read, err := s.Config.Read(readParams)
	if err != nil || read == nil {
		return true
	}
	return features.Enabled((&config.Config{Values: read.Config}).FeatureSettings(), "hooks")
}

func MergeHookListResponses(left *HookListResponse, right *HookListResponse) *HookListResponse {
	if left == nil && right == nil {
		return &HookListResponse{Data: []HookListEntry{}}
	}
	entries := map[string]*HookListEntry{}
	add := func(response *HookListResponse) {
		if response == nil {
			return
		}
		for i := range response.Data {
			source := response.Data[i]
			cwd := strings.TrimSpace(source.CWD)
			if cwd == "" {
				cwd = "."
			}
			entry := entries[cwd]
			if entry == nil {
				entry = &HookListEntry{CWD: cwd}
				entries[cwd] = entry
			}
			entry.Hooks = append(entry.Hooks, source.Hooks...)
			entry.Warnings = append(entry.Warnings, source.Warnings...)
			entry.Errors = append(entry.Errors, source.Errors...)
			entry.RequiredLoadErrors = append(entry.RequiredLoadErrors, source.RequiredLoadErrors...)
		}
	}
	add(left)
	add(right)

	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := &HookListResponse{Data: make([]HookListEntry, 0, len(keys))}
	for _, key := range keys {
		entry := cloneEntry(*entries[key])
		sortHooks(entry.Hooks)
		out.Data = append(out.Data, entry)
	}
	return out
}

func (s *HookDiscoveryService) appendUserHooks(entry *HookListEntry) {
	if s == nil || strings.TrimSpace(s.CodexHome) == "" {
		return
	}
	configPath := filepath.Join(s.CodexHome, "config.toml")
	appendHooksTOML(entry, &hookDiscoverySource{
		Path:                configPath,
		KeySource:           configPath,
		Source:              HookSourceUser,
		States:              s.States,
		McpToolHooksEnabled: s.McpToolHooksEnabled,
	})
	sourcePath := filepath.Join(s.CodexHome, "hooks.json")
	appendHooksJSON(entry, &hookDiscoverySource{
		Path:                sourcePath,
		KeySource:           "file:" + sourcePath,
		Source:              HookSourceUser,
		States:              s.States,
		McpToolHooksEnabled: s.McpToolHooksEnabled,
	})
}

func (s *HookDiscoveryService) appendProjectHooks(entry *HookListEntry, cwd string) {
	for _, folder := range s.projectHookFolders(cwd) {
		configPath := filepath.Join(folder, "config.toml")
		appendHooksTOML(entry, &hookDiscoverySource{
			Path:                configPath,
			KeySource:           configPath,
			Source:              HookSourceProject,
			States:              s.States,
			BypassTrust:         s != nil && s.BypassTrust,
			McpToolHooksEnabled: s.McpToolHooksEnabled,
		})
		sourcePath := filepath.Join(folder, "hooks.json")
		appendHooksJSON(entry, &hookDiscoverySource{
			Path:                sourcePath,
			KeySource:           "file:" + sourcePath,
			Source:              HookSourceProject,
			States:              s.States,
			BypassTrust:         s != nil && s.BypassTrust,
			McpToolHooksEnabled: s.McpToolHooksEnabled,
		})
	}
}

func (s *HookDiscoveryService) projectHookFolders(cwd string) []string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return nil
	}
	if s != nil && s.Config != nil {
		read, err := s.Config.Read(&config.ConfigReadParams{CWD: &cwd, IncludeLayers: true})
		if err == nil && read != nil {
			return projectHookFoldersFromLayers(read.Layers)
		}
	}
	return []string{filepath.Join(cwd, ".gcode")}
}

func projectHookFoldersFromLayers(layers []config.Layer) []string {
	seen := map[string]bool{}
	var folders []string
	for i := range layers {
		if layers[i].Name.Type != config.LayerSourceProject {
			continue
		}
		folder := strings.TrimSpace(layers[i].Name.HooksDotCodexFolder)
		if folder == "" {
			folder = strings.TrimSpace(layers[i].Name.DotCodexFolder)
		}
		if folder == "" {
			if strings.TrimSpace(layers[i].Name.File) == "" {
				continue
			}
			folder = filepath.Dir(layers[i].Name.File)
		}
		if seen[folder] {
			continue
		}
		seen[folder] = true
		folders = append(folders, folder)
	}
	return folders
}

type hookDiscoverySource struct {
	Path        string
	KeySource   string
	Source      HookSource
	States      map[string]*HookState
	BypassTrust bool
	PluginID    *string
	Env         map[string]string
	// McpToolHooksEnabled lists mcp_tool handlers instead of warning that MCP
	// invocation is not available yet.
	McpToolHooksEnabled bool
}

func (s *hookDiscoverySource) State(key string) *HookState {
	if s == nil || s.States == nil {
		return nil
	}
	return s.States[key]
}

func (s *HookDiscoveryService) appendPluginHooks(entry *HookListEntry) {
	if s == nil {
		return
	}
	for _, source := range s.PluginHookSources {
		pluginID := strings.TrimSpace(source.PluginID)
		sourcePath := strings.TrimSpace(source.SourcePath)
		relativePath := strings.Trim(strings.TrimSpace(source.SourceRelativePath), `/\`)
		if pluginID == "" || sourcePath == "" {
			continue
		}
		if relativePath == "" {
			relativePath = filepath.ToSlash(filepath.Join("hooks", "hooks.json"))
		}
		pluginRoot := strings.TrimSpace(source.PluginRoot)
		pluginDataRoot := strings.TrimSpace(source.PluginDataRoot)
		if pluginDataRoot == "" && pluginRoot != "" {
			pluginDataRoot = filepath.Join(pluginRoot, "data")
		}
		appendHooksJSONWithMessagePrefix(entry, &hookDiscoverySource{
			Path:                sourcePath,
			KeySource:           pluginID + ":" + filepath.ToSlash(relativePath),
			Source:              HookSourcePlugin,
			States:              s.States,
			BypassTrust:         s.BypassTrust,
			PluginID:            &pluginID,
			McpToolHooksEnabled: s.McpToolHooksEnabled,
			Env: map[string]string{
				"PLUGIN_ROOT":        pluginRoot,
				"CLAUDE_PLUGIN_ROOT": pluginRoot,
				"PLUGIN_DATA":        pluginDataRoot,
				"CLAUDE_PLUGIN_DATA": pluginDataRoot,
			},
		}, "plugin hooks config")
	}
}

type hooksJSONFileWire struct {
	Hooks map[string][]hookJSONMatcherGroupWire `json:"hooks"`
}

type hookJSONMatcherGroupWire struct {
	Matcher *string                     `json:"matcher"`
	Hooks   []hookJSONHandlerConfigWire `json:"hooks"`
}

type hookJSONHandlerConfigWire struct {
	Type                string         `json:"type"`
	Command             string         `json:"command"`
	CommandWindows      *string        `json:"commandWindows"`
	CommandWindowsAlias *string        `json:"command_windows"`
	Server              string         `json:"server"`
	Tool                string         `json:"tool"`
	Input               map[string]any `json:"input"`
	Timeout             *uint64        `json:"timeout"`
	TimeoutSec          *uint64        `json:"timeoutSec"`
	TimeoutSecAlias     *uint64        `json:"timeout_sec"`
	Async               bool           `json:"async"`
	StatusMessage       *string        `json:"statusMessage"`
	StatusMessageAlias  *string        `json:"status_message"`
	// Mirrors Rust HookHandlerConfig::Command additional_context_limit
	// (config/src/hook_config.rs): approximate token threshold for spilling
	// this hook's additionalContext to disk. Unset uses 2,500 tokens; 0
	// disables spilling.
	AdditionalContextLimit *uint64 `json:"additionalContextLimit"`
}

func appendHooksTOML(entry *HookListEntry, source *hookDiscoverySource) {
	if entry == nil || source == nil {
		return
	}
	info, err := os.Stat(source.Path)
	if err != nil {
		if !os.IsNotExist(err) {
			entry.Warnings = append(entry.Warnings, fmt.Sprintf("failed to read hooks config %s: %v", source.Path, err))
		}
		return
	}
	if info.IsDir() {
		entry.Warnings = append(entry.Warnings, fmt.Sprintf("failed to read hooks config %s: path is a directory", source.Path))
		return
	}
	data, err := os.ReadFile(source.Path)
	if err != nil {
		entry.Warnings = append(entry.Warnings, fmt.Sprintf("failed to read hooks config %s: %v", source.Path, err))
		return
	}
	file, warnings := parseHooksTOML(string(data), source.Path)
	for _, warning := range warnings {
		entry.Warnings = append(entry.Warnings, warning)
	}
	if len(file.Hooks) == 0 {
		return
	}
	sourcePath, err := filepath.Abs(source.Path)
	if err != nil {
		entry.Warnings = append(entry.Warnings, fmt.Sprintf("failed to normalize hooks config path %s: %v", source.Path, err))
		return
	}
	normalized := &hookDiscoverySource{
		Path:                sourcePath,
		KeySource:           sourcePath,
		Source:              source.Source,
		States:              source.States,
		BypassTrust:         source.BypassTrust,
		PluginID:            cloneString(source.PluginID),
		Env:                 cloneHookEnv(source.Env),
		McpToolHooksEnabled: source.McpToolHooksEnabled,
	}
	appendHookConfig(entry, normalized, file)
}

func appendHooksJSON(entry *HookListEntry, source *hookDiscoverySource) {
	appendHooksJSONWithMessagePrefix(entry, source, "hooks config")
}

func appendHooksJSONWithMessagePrefix(entry *HookListEntry, source *hookDiscoverySource, label string) {
	if entry == nil || source == nil {
		return
	}
	info, err := os.Stat(source.Path)
	if err != nil {
		if !os.IsNotExist(err) {
			entry.Warnings = append(entry.Warnings, fmt.Sprintf("failed to read %s %s: %v", label, source.Path, err))
		}
		return
	}
	if info.IsDir() {
		entry.Warnings = append(entry.Warnings, fmt.Sprintf("failed to read %s %s: path is a directory", label, source.Path))
		return
	}
	data, err := os.ReadFile(source.Path)
	if err != nil {
		entry.Warnings = append(entry.Warnings, fmt.Sprintf("failed to read %s %s: %v", label, source.Path, err))
		return
	}
	var file hooksJSONFileWire
	if err := json.Unmarshal(data, &file); err != nil {
		entry.Warnings = append(entry.Warnings, fmt.Sprintf("failed to parse %s %s: %v", label, source.Path, err))
		return
	}
	sourcePath, err := filepath.Abs(source.Path)
	if err != nil {
		entry.Warnings = append(entry.Warnings, fmt.Sprintf("failed to normalize hooks config path %s: %v", source.Path, err))
		return
	}
	normalized := &hookDiscoverySource{
		Path:                sourcePath,
		KeySource:           firstNonEmptyHookString(source.KeySource, "file:"+sourcePath),
		Source:              source.Source,
		States:              source.States,
		BypassTrust:         source.BypassTrust,
		PluginID:            cloneString(source.PluginID),
		Env:                 cloneHookEnv(source.Env),
		McpToolHooksEnabled: source.McpToolHooksEnabled,
	}
	appendHookConfig(entry, normalized, file)
}

func appendHookConfig(entry *HookListEntry, source *hookDiscoverySource, file hooksJSONFileWire) {
	displayOrder := int64(len(entry.Hooks))
	for _, eventName := range orderedHookJSONEventNames() {
		groups := file.Hooks[eventName]
		if len(groups) == 0 {
			continue
		}
		event := hookEventFromJSONName(eventName)
		for groupIndex := range groups {
			displayOrder = appendDiscoveredHookGroup(entry, source, event, groups[groupIndex], groupIndex, displayOrder)
		}
	}
	for eventName := range file.Hooks {
		if hookEventFromJSONName(eventName) == "" {
			entry.Warnings = append(entry.Warnings, fmt.Sprintf("skipping unsupported hook event %q in %s", eventName, source.Path))
		}
	}
}

func firstNonEmptyHookString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func cloneHookEnv(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

type hooksTOMLSectionKind int

const (
	hooksTOMLSectionOther hooksTOMLSectionKind = iota
	hooksTOMLSectionHooks
	hooksTOMLSectionGroup
	hooksTOMLSectionHandler
)

type hooksTOMLSection struct {
	Kind  hooksTOMLSectionKind
	Event string
}

func parseHooksTOML(input string, path string) (hooksJSONFileWire, []string) {
	file := hooksJSONFileWire{Hooks: map[string][]hookJSONMatcherGroupWire{}}
	var warnings []string
	section := hooksTOMLSection{}
	var currentGroup *hookJSONMatcherGroupWire
	var currentHandler *hookJSONHandlerConfigWire
	for _, line := range strings.Split(strings.ReplaceAll(input, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(stripTOMLComment(line))
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "[[") && strings.HasSuffix(trimmed, "]]") {
			name := strings.TrimSpace(trimmed[2 : len(trimmed)-2])
			section = parseHooksTOMLArraySection(name)
			currentGroup = nil
			currentHandler = nil
			switch section.Kind {
			case hooksTOMLSectionGroup:
				group := hookJSONMatcherGroupWire{}
				file.Hooks[section.Event] = append(file.Hooks[section.Event], group)
				currentGroup = &file.Hooks[section.Event][len(file.Hooks[section.Event])-1]
			case hooksTOMLSectionHandler:
				groups := file.Hooks[section.Event]
				if len(groups) == 0 {
					file.Hooks[section.Event] = append(file.Hooks[section.Event], hookJSONMatcherGroupWire{})
					groups = file.Hooks[section.Event]
				}
				group := &file.Hooks[section.Event][len(groups)-1]
				handler := hookJSONHandlerConfigWire{}
				group.Hooks = append(group.Hooks, handler)
				currentGroup = group
				currentHandler = &group.Hooks[len(group.Hooks)-1]
			}
			continue
		}
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			name := strings.TrimSpace(trimmed[1 : len(trimmed)-1])
			section = parseHooksTOMLTableSection(name)
			currentGroup = nil
			currentHandler = nil
			continue
		}
		key, rawValue, ok := strings.Cut(trimmed, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		rawValue = strings.TrimSpace(rawValue)
		switch section.Kind {
		case hooksTOMLSectionGroup:
			if currentGroup == nil {
				continue
			}
			applyHooksTOMLGroupValue(currentGroup, key, rawValue)
		case hooksTOMLSectionHandler:
			if currentHandler == nil {
				continue
			}
			if warning := applyHooksTOMLHandlerValue(currentHandler, key, rawValue, path); warning != "" {
				warnings = append(warnings, warning)
			}
		}
	}
	return file, warnings
}

func parseHooksTOMLTableSection(name string) hooksTOMLSection {
	if strings.TrimSpace(name) == "hooks" {
		return hooksTOMLSection{Kind: hooksTOMLSectionHooks}
	}
	return hooksTOMLSection{}
}

func parseHooksTOMLArraySection(name string) hooksTOMLSection {
	parts := splitHooksTOMLDottedPath(name)
	if len(parts) == 2 && parts[0] == "hooks" {
		return hooksTOMLSection{Kind: hooksTOMLSectionGroup, Event: parts[1]}
	}
	if len(parts) == 3 && parts[0] == "hooks" && parts[2] == "hooks" {
		return hooksTOMLSection{Kind: hooksTOMLSectionHandler, Event: parts[1]}
	}
	return hooksTOMLSection{}
}

func applyHooksTOMLGroupValue(group *hookJSONMatcherGroupWire, key string, rawValue string) {
	switch key {
	case "matcher":
		if value, ok := parseHooksTOMLString(rawValue); ok {
			group.Matcher = &value
		}
	}
}

func applyHooksTOMLHandlerValue(handler *hookJSONHandlerConfigWire, key string, rawValue string, path string) string {
	switch key {
	case "type":
		if value, ok := parseHooksTOMLString(rawValue); ok {
			handler.Type = value
		}
	case "command":
		if value, ok := parseHooksTOMLString(rawValue); ok {
			handler.Command = value
		}
	case "server":
		if value, ok := parseHooksTOMLString(rawValue); ok {
			handler.Server = value
		}
	case "tool":
		if value, ok := parseHooksTOMLString(rawValue); ok {
			handler.Tool = value
		}
	case "commandWindows", "command_windows":
		if value, ok := parseHooksTOMLString(rawValue); ok {
			if key == "commandWindows" {
				handler.CommandWindows = &value
			} else {
				handler.CommandWindowsAlias = &value
			}
		}
	case "timeout", "timeoutSec", "timeout_sec":
		value, ok := parseHooksTOMLUint(rawValue)
		if !ok {
			return fmt.Sprintf("skipping invalid hook timeout %q in %s", rawValue, path)
		}
		switch key {
		case "timeout":
			handler.Timeout = &value
		case "timeoutSec":
			handler.TimeoutSec = &value
		default:
			handler.TimeoutSecAlias = &value
		}
	case "async":
		if value, ok := parseHooksTOMLBool(rawValue); ok {
			handler.Async = value
		}
	case "statusMessage", "status_message":
		if value, ok := parseHooksTOMLString(rawValue); ok {
			if key == "statusMessage" {
				handler.StatusMessage = &value
			} else {
				handler.StatusMessageAlias = &value
			}
		}
	case "additionalContextLimit", "additional_context_limit":
		value, ok := parseHooksTOMLUint(rawValue)
		if !ok {
			return fmt.Sprintf("skipping invalid hook additionalContextLimit %q in %s", rawValue, path)
		}
		handler.AdditionalContextLimit = &value
	}
	return ""
}

func stripTOMLComment(line string) string {
	var quote rune
	escaped := false
	for i, r := range line {
		switch {
		case escaped:
			escaped = false
		case quote != 0:
			if quote == '"' && r == '\\' {
				escaped = true
				continue
			}
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			quote = r
		case r == '#':
			return line[:i]
		}
	}
	return line
}

func splitHooksTOMLDottedPath(path string) []string {
	var parts []string
	var current strings.Builder
	var quote rune
	escaped := false
	for _, ch := range path {
		switch {
		case escaped:
			current.WriteRune(ch)
			escaped = false
		case quote != 0:
			if quote == '"' && ch == '\\' {
				current.WriteRune(ch)
				escaped = true
				continue
			}
			if ch == quote {
				quote = 0
			}
			current.WriteRune(ch)
		case ch == '\'' || ch == '"':
			quote = ch
			current.WriteRune(ch)
		case ch == '.':
			parts = append(parts, unquoteHooksTOMLPathPart(strings.TrimSpace(current.String())))
			current.Reset()
		default:
			current.WriteRune(ch)
		}
	}
	parts = append(parts, unquoteHooksTOMLPathPart(strings.TrimSpace(current.String())))
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func unquoteHooksTOMLPathPart(part string) string {
	value, ok := parseHooksTOMLString(part)
	if ok {
		return value
	}
	return strings.Trim(part, "\"'")
}

func parseHooksTOMLString(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		value, err := strconv.Unquote(raw)
		if err == nil {
			return value, true
		}
		return strings.Trim(raw, "\""), true
	}
	if len(raw) >= 2 && raw[0] == '\'' && raw[len(raw)-1] == '\'' {
		return raw[1 : len(raw)-1], true
	}
	if raw == "" {
		return "", false
	}
	return strings.Trim(raw, "\"'"), true
}

func parseHooksTOMLUint(raw string) (uint64, bool) {
	raw = strings.ReplaceAll(strings.TrimSpace(raw), "_", "")
	if raw == "" || strings.HasPrefix(raw, "-") {
		return 0, false
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	return value, err == nil
}

func parseHooksTOMLBool(raw string) (bool, bool) {
	switch strings.TrimSpace(raw) {
	case "true":
		return true, true
	case "false":
		return false, true
	default:
		return false, false
	}
}

// hookDiscoveryExecutionMode mirrors Rust discovery.rs: async command hooks
// run in the background except SessionEnd, which stays synchronous.
func hookDiscoveryExecutionMode(async bool, event HookEventName) HookExecutionMode {
	if async && event != HookEventSessionEnd && event != HookEventInterrupt {
		return HookExecutionAsync
	}
	return HookExecutionSync
}

func appendDiscoveredHookGroup(entry *HookListEntry, source *hookDiscoverySource, event HookEventName, group hookJSONMatcherGroupWire, groupIndex int, displayOrder int64) int64 {
	matcher := normalizedHookMatcher(event, group.Matcher)
	if matcher != nil {
		if err := validateHookMatcherPattern(*matcher); err != nil {
			entry.Warnings = append(entry.Warnings, fmt.Sprintf("invalid matcher %q in %s: %v", *matcher, source.Path, err))
			return displayOrder
		}
	}
	// Rust #49379: compile the matcher once during discovery so dispatch never
	// recompiles it for each input.
	compiledMatcher := compiledHookMatcher(matcher)
	for handlerIndex := range group.Hooks {
		handler := group.Hooks[handlerIndex]
		handlerType := handler.hookHandlerType()
		// Rust bundled_hooks.rs: a known cleanup handler from an unsigned
		// bundled plugin is treated as builtin (trusted and enabled without a
		// recorded hash). The local discovery path has no enabled-tool catalog,
		// so an Apps target cannot match here.
		builtin := false
		if source.PluginID != nil && strings.TrimSpace(*source.PluginID) != "" {
			builtin = isAllowlistedBundledCleanupHook(strings.TrimSpace(*source.PluginID), event, matcher, handler, nil)
		}
		switch handlerType {
		case HookHandlerCommand:
			command := handler.commandForPlatform()
			if strings.TrimSpace(command) == "" {
				entry.Warnings = append(entry.Warnings, fmt.Sprintf("skipping empty hook command in %s", source.Path))
				continue
			}
			timeoutSec := handler.timeoutSecForEvent(event)
			key := hookDiscoveryKey(source.KeySource, event, groupIndex, handlerIndex)
			additionalContextLimit := handler.additionalContextLimit()
			currentHash := hookDiscoveryHash(event, group.Matcher, command, handler.Async, timeoutSec, handler.statusMessage(), additionalContextLimit)
			state := source.State(key)
			displayCommand := expandHookEnvPlaceholders(command, source.Env)
			metadata := HookMetadata{
				Key:                    key,
				EventName:              event,
				HandlerType:            HookHandlerCommand,
				ExecutionMode:          hookDiscoveryExecutionMode(handler.Async, event),
				Matcher:                cloneString(matcher),
				Command:                &displayCommand,
				TimeoutSec:             timeoutSec,
				StatusMessage:          handler.statusMessage(),
				AdditionalContextLimit: additionalContextLimit,
				SourcePath:             source.Path,
				Source:                 source.Source,
				PluginID:               cloneString(source.PluginID),
				DisplayOrder:           displayOrder,
				Builtin:                builtin,
				compiledMatcher:        compiledMatcher,
				Enabled:                hookEnabled(false, builtin, state),
				IsManaged:              false,
				CurrentHash:            currentHash,
				TrustStatus:            hookTrustStatus(false, builtin, currentHash, hookTrustedHash(false, state)),
				BypassTrust:            source.BypassTrust,
				Env:                    cloneHookEnv(source.Env),
			}
			entry.Hooks = append(entry.Hooks, metadata)
			displayOrder++
		case HookHandlerPrompt:
			entry.Warnings = append(entry.Warnings, fmt.Sprintf("skipping prompt hook in %s: prompt hooks are not supported yet", source.Path))
		case HookHandlerAgent:
			entry.Warnings = append(entry.Warnings, fmt.Sprintf("skipping agent hook in %s: agent hooks are not supported yet", source.Path))
		case HookHandlerMCPTool:
			server := strings.TrimSpace(handler.Server)
			toolName := strings.TrimSpace(handler.Tool)
			if source == nil || !source.McpToolHooksEnabled {
				entry.Warnings = append(entry.Warnings, fmt.Sprintf("skipping MCP tool hook in %s: MCP invocation is not available yet", source.Path))
				continue
			}
			if server == "" || toolName == "" {
				entry.Warnings = append(entry.Warnings, fmt.Sprintf("skipping MCP tool hook in %s: server and tool are required", source.Path))
				continue
			}
			if event == HookEventSessionEnd {
				entry.Warnings = append(entry.Warnings, fmt.Sprintf("skipping MCP tool hook in %s: MCP tool hooks are not supported for SessionEnd", source.Path))
				continue
			}
			timeoutSec := handler.timeoutSecForEvent(event)
			key := hookDiscoveryKey(source.KeySource, event, groupIndex, handlerIndex)
			inputTemplate := cloneHookInput(handler.Input)
			currentHash := hookDiscoveryHashHandler(event, group.Matcher, HookHandlerMCPTool, "", false, handler.Server, handler.Tool, inputTemplate, timeoutSec, handler.statusMessage(), nil)
			state := source.State(key)
			metadata := HookMetadata{
				Key:             key,
				EventName:       event,
				HandlerType:     HookHandlerMCPTool,
				ExecutionMode:   HookExecutionSync,
				Matcher:         cloneString(matcher),
				Server:          &server,
				Tool:            &toolName,
				Input:           inputTemplate,
				TimeoutSec:      timeoutSec,
				StatusMessage:   handler.statusMessage(),
				SourcePath:      source.Path,
				compiledMatcher: compiledMatcher,
				Source:          source.Source,
				PluginID:        cloneString(source.PluginID),
				DisplayOrder:    displayOrder,
				Builtin:         builtin,
				Enabled:         hookEnabled(false, builtin, state),
				IsManaged:       false,
				CurrentHash:     currentHash,
				TrustStatus:     hookTrustStatus(false, builtin, currentHash, hookTrustedHash(false, state)),
				BypassTrust:     source.BypassTrust,
				Env:             cloneHookEnv(source.Env),
			}
			entry.Hooks = append(entry.Hooks, metadata)
			displayOrder++
		default:
			entry.Warnings = append(entry.Warnings, fmt.Sprintf("skipping unsupported hook handler %q in %s", handler.Type, source.Path))
		}
	}
	return displayOrder
}

func expandHookEnvPlaceholders(command string, env map[string]string) string {
	if len(env) == 0 {
		return command
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		if strings.TrimSpace(key) != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		command = strings.ReplaceAll(command, "${"+key+"}", env[key])
	}
	return command
}

func (h *hookJSONHandlerConfigWire) hookHandlerType() HookHandlerType {
	if h == nil {
		return ""
	}
	switch strings.TrimSpace(h.Type) {
	case "":
		if strings.TrimSpace(h.Command) != "" || h.CommandWindows != nil || h.CommandWindowsAlias != nil {
			return HookHandlerCommand
		}
		return ""
	case string(HookHandlerCommand):
		return HookHandlerCommand
	case string(HookHandlerPrompt):
		return HookHandlerPrompt
	case string(HookHandlerAgent):
		return HookHandlerAgent
	case string(HookHandlerMCPTool):
		return HookHandlerMCPTool
	default:
		return ""
	}
}

func (h *hookJSONHandlerConfigWire) commandForPlatform() string {
	if h == nil {
		return ""
	}
	if runtime.GOOS == "windows" {
		if h.CommandWindows != nil {
			return *h.CommandWindows
		}
		if h.CommandWindowsAlias != nil {
			return *h.CommandWindowsAlias
		}
	}
	return h.Command
}

// timeoutSecForEvent resolves this handler's configured timeout and normalizes it the
// way Rust's normalize_command_hook does (hooks/src/engine/discovery.rs): SessionEnd and
// Interrupt default to one second and clamp to 1..=3s
// (hooks/src/events/session_end.rs SESSION_END_DEFAULT_TIMEOUT_SEC/MAX_TIMEOUT_SEC);
// every other hook defaults to ten minutes and is floored at one second. The normalized
// value is what both the reported timeout and the hook trust hash use.
func (h *hookJSONHandlerConfigWire) timeoutSecForEvent(event HookEventName) int64 {
	if h == nil {
		return hookNormalizedCommandTimeout(event, nil)
	}
	value := h.Timeout
	if value == nil {
		value = h.TimeoutSec
	}
	if value == nil {
		value = h.TimeoutSecAlias
	}
	return hookNormalizedCommandTimeout(event, value)
}

// hookNormalizedCommandTimeout mirrors Rust normalize_command_hook
// (hooks/src/engine/discovery.rs): SessionEnd and Interrupt use 1s by default and clamp
// to SESSION_END_MAX_TIMEOUT_SEC (3s); every other event uses 600s by default and never
// goes below one second.
func hookNormalizedCommandTimeout(event HookEventName, raw *uint64) int64 {
	value := int64(0)
	if raw != nil {
		value = hookTimeoutToInt64(*raw)
	}
	switch event {
	case HookEventSessionEnd, HookEventInterrupt:
		if raw == nil {
			return sessionEndHookDefaultTimeoutSec
		}
		if value < 1 {
			return 1
		}
		if value > sessionEndHookMaxTimeoutSec {
			return sessionEndHookMaxTimeoutSec
		}
		return value
	default:
		if raw == nil {
			return defaultDiscoveredHookTimeoutSec
		}
		if value < 1 {
			return 1
		}
		return value
	}
}

func hookTimeoutToInt64(value uint64) int64 {
	if value > uint64(^uint64(0)>>1) {
		return int64(^uint64(0) >> 1)
	}
	return int64(value)
}

func (h *hookJSONHandlerConfigWire) statusMessage() *string {
	if h == nil {
		return nil
	}
	if h.StatusMessage != nil {
		return cloneString(h.StatusMessage)
	}
	return cloneString(h.StatusMessageAlias)
}

func (h *hookJSONHandlerConfigWire) additionalContextLimit() *int64 {
	if h == nil || h.AdditionalContextLimit == nil {
		return nil
	}
	value := *h.AdditionalContextLimit
	if value > uint64(^uint64(0)>>1) {
		return nil
	}
	converted := int64(value)
	return &converted
}

func hookDiscoveryCWDs(params *HookListParams, defaultCWD string) []string {
	seen := map[string]bool{}
	var cwds []string
	if params != nil {
		for _, cwd := range params.CWDs {
			cwd = strings.TrimSpace(cwd)
			if cwd == "" || seen[cwd] {
				continue
			}
			seen[cwd] = true
			cwds = append(cwds, cwd)
		}
	}
	if len(cwds) == 0 {
		defaultCWD = strings.TrimSpace(defaultCWD)
		if defaultCWD == "" {
			defaultCWD = "."
		}
		cwds = append(cwds, defaultCWD)
	}
	return cwds
}

func orderedHookJSONEventNames() []string {
	return []string{
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
		"Interrupt",
	}
}

func hookEventFromJSONName(value string) HookEventName {
	switch value {
	case "PreToolUse":
		return HookEventPreToolUse
	case "PermissionRequest":
		return HookEventPermissionRequest
	case "PostToolUse":
		return HookEventPostToolUse
	case "PreCompact":
		return HookEventPreCompact
	case "PostCompact":
		return HookEventPostCompact
	case "SessionStart":
		return HookEventSessionStart
	case "SessionEnd":
		return HookEventSessionEnd
	case "UserPromptSubmit":
		return HookEventUserPromptSubmit
	case "SubagentStart":
		return HookEventSubagentStart
	case "SubagentStop":
		return HookEventSubagentStop
	case "Stop":
		return HookEventStop
	case "Interrupt":
		return HookEventInterrupt
	default:
		return ""
	}
}

func hookEventKeyLabel(event HookEventName) string {
	switch event {
	case HookEventPreToolUse:
		return "pre_tool_use"
	case HookEventPermissionRequest:
		return "permission_request"
	case HookEventPostToolUse:
		return "post_tool_use"
	case HookEventPreCompact:
		return "pre_compact"
	case HookEventPostCompact:
		return "post_compact"
	case HookEventSessionStart:
		return "session_start"
	case HookEventSessionEnd:
		return "session_end"
	case HookEventUserPromptSubmit:
		return "user_prompt_submit"
	case HookEventSubagentStart:
		return "subagent_start"
	case HookEventSubagentStop:
		return "subagent_stop"
	case HookEventStop:
		return "stop"
	default:
		return strings.TrimSpace(string(event))
	}
}

func hookDiscoveryKey(source string, event HookEventName, groupIndex int, handlerIndex int) string {
	return fmt.Sprintf("%s:%s:%d:%d", source, hookEventKeyLabel(event), groupIndex, handlerIndex)
}

func normalizedHookMatcher(event HookEventName, matcher *string) *string {
	if matcher == nil {
		return nil
	}
	if event == HookEventStop || event == HookEventSessionEnd || event == HookEventUserPromptSubmit {
		return nil
	}
	value := strings.TrimSpace(*matcher)
	if value == "" {
		return nil
	}
	return &value
}

// hookHashMatcher mirrors Rust matcher_pattern_for_event
// (hooks/src/events/common.rs): UserPromptSubmit, Stop and Interrupt carry no matcher in
// the hashed identity; every other event keeps the configured matcher verbatim.
func hookHashMatcher(event HookEventName, matcher *string) *string {
	switch event {
	case HookEventUserPromptSubmit, HookEventStop, HookEventInterrupt:
		return nil
	}
	return matcher
}

// hookHashAdditionalContextLimit mirrors the additionalContextLimit normalization in
// Rust discovery.rs::append_matcher_groups: the limit survives only for events that can
// emit additionalContext, and an explicit default (2,500 tokens) is normalized away
// before hashing.
func hookHashAdditionalContextLimit(event HookEventName, limit *int64) *int64 {
	if limit == nil {
		return nil
	}
	switch event {
	case HookEventPreToolUse, HookEventPostToolUse, HookEventSessionStart, HookEventUserPromptSubmit, HookEventSubagentStart:
	default:
		return nil
	}
	if *limit == defaultHookOutputTokenLimit {
		return nil
	}
	return limit
}

func hookDiscoveryHash(event HookEventName, matcher *string, command string, async bool, timeoutSec int64, statusMessage *string, additionalContextLimit *int64) string {
	return hookDiscoveryHashHandler(event, matcher, HookHandlerCommand, command, async, "", "", nil, timeoutSec, statusMessage, additionalContextLimit)
}

// hookDiscoveryHashHandler fingerprints one normalized hook identity, mirroring Rust
// hooks/src/engine/discovery.rs::hook_hash -> codex_config::version_for_toml
// (codex-rs/config/src/fingerprint.rs, #49295 "Simplify configuration fingerprint
// canonicalization"). The identity is
//
//	{ event_name, matcher?, hooks: [handler] }
//
// hashed as the canonical JSON "sha256:<hex>" through config.VersionForTOML. The handler
// keys follow Rust's HookHandlerConfig serialization: command hooks carry
// command/async/timeout, mcp_tool hooks carry server/tool/input/timeout and no async,
// `input` is always present (an omitted input serializes as an empty table), None
// options are dropped, and additionalContextLimit is skipped when unset. All inputs are
// the raw config-derived values (untrimmed), so a Rust-written trusted_hash validates in
// Go for the same hook definition.
func hookDiscoveryHashHandler(event HookEventName, matcher *string, handlerType HookHandlerType, command string, async bool, server string, tool string, input map[string]any, timeoutSec int64, statusMessage *string, additionalContextLimit *int64) string {
	handler := map[string]any{
		"type":    string(handlerType),
		"timeout": timeoutSec,
	}
	switch handlerType {
	case HookHandlerCommand:
		handler["command"] = command
		handler["async"] = async
	case HookHandlerMCPTool:
		handler["server"] = server
		handler["tool"] = tool
		mcpInput := input
		if mcpInput == nil {
			mcpInput = map[string]any{}
		}
		handler["input"] = mcpInput
	}
	if statusMessage != nil {
		handler["statusMessage"] = *statusMessage
	}
	if limit := hookHashAdditionalContextLimit(event, additionalContextLimit); limit != nil {
		handler["additionalContextLimit"] = *limit
	}
	identity := map[string]any{
		"event_name": hookEventKeyLabel(event),
		"hooks":      []any{handler},
	}
	if hashedMatcher := hookHashMatcher(event, matcher); hashedMatcher != nil {
		identity["matcher"] = *hashedMatcher
	}
	return config.VersionForTOML(identity)
}

func cloneHookInput(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = cloneHookInputValue(value)
	}
	return out
}

func cloneHookInputValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneHookInput(typed)
	case []any:
		out := make([]any, len(typed))
		for i := range typed {
			out[i] = cloneHookInputValue(typed[i])
		}
		return out
	default:
		return value
	}
}

// hookEnabled mirrors Rust discovery.rs: builtin and managed hooks are always
// enabled; otherwise the recorded state decides (absent means enabled).
func hookEnabled(isManaged bool, isBuiltin bool, state *HookState) bool {
	if isBuiltin || isManaged {
		return true
	}
	return state == nil || state.Enabled == nil || *state.Enabled
}

func hookTrustedHash(isManaged bool, state *HookState) *string {
	if isManaged || state == nil {
		return nil
	}
	return cloneString(state.TrustedHash)
}

// hookTrustStatus mirrors Rust discovery.rs: builtin hooks are trusted without
// a recorded hash, managed hooks report managed, and everything else compares
// the current hash to the trusted one.
func hookTrustStatus(isManaged bool, isBuiltin bool, currentHash string, trustedHash *string) HookTrustStatus {
	if isBuiltin {
		return HookTrustTrusted
	}
	if isManaged {
		return HookTrustManaged
	}
	if trustedHash == nil {
		return HookTrustUntrusted
	}
	if *trustedHash == currentHash {
		return HookTrustTrusted
	}
	return HookTrustModified
}
