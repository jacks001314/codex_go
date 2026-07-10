package appserver

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"codex_go/internal/config"
	"codex_go/internal/features"
	"codex_go/internal/plugin"
)

const defaultDiscoveredHookTimeoutSec int64 = 600

type HookDiscoveryService struct {
	CodexHome         string
	Config            *config.ConfigService
	States            map[string]*HookState
	BypassTrust       bool
	PluginHookSources []plugin.HookSource
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
		sortHooks(entry.Hooks)
		response.Data = append(response.Data, entry)
	}
	return response
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
		Path:      configPath,
		KeySource: configPath,
		Source:    HookSourceUser,
		States:    s.States,
	})
	sourcePath := filepath.Join(s.CodexHome, "hooks.json")
	appendHooksJSON(entry, &hookDiscoverySource{
		Path:      sourcePath,
		KeySource: "file:" + sourcePath,
		Source:    HookSourceUser,
		States:    s.States,
	})
}

func (s *HookDiscoveryService) appendProjectHooks(entry *HookListEntry, cwd string) {
	for _, folder := range s.projectHookFolders(cwd) {
		configPath := filepath.Join(folder, "config.toml")
		appendHooksTOML(entry, &hookDiscoverySource{
			Path:        configPath,
			KeySource:   configPath,
			Source:      HookSourceProject,
			States:      s.States,
			BypassTrust: s != nil && s.BypassTrust,
		})
		sourcePath := filepath.Join(folder, "hooks.json")
		appendHooksJSON(entry, &hookDiscoverySource{
			Path:        sourcePath,
			KeySource:   "file:" + sourcePath,
			Source:      HookSourceProject,
			States:      s.States,
			BypassTrust: s != nil && s.BypassTrust,
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
	return []string{filepath.Join(cwd, ".codex")}
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
			Path:        sourcePath,
			KeySource:   pluginID + ":" + filepath.ToSlash(relativePath),
			Source:      HookSourcePlugin,
			States:      s.States,
			BypassTrust: s.BypassTrust,
			PluginID:    &pluginID,
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
	Type                string  `json:"type"`
	Command             string  `json:"command"`
	CommandWindows      *string `json:"commandWindows"`
	CommandWindowsAlias *string `json:"command_windows"`
	Timeout             *uint64 `json:"timeout"`
	TimeoutSec          *uint64 `json:"timeoutSec"`
	TimeoutSecAlias     *uint64 `json:"timeout_sec"`
	Async               bool    `json:"async"`
	StatusMessage       *string `json:"statusMessage"`
	StatusMessageAlias  *string `json:"status_message"`
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
		Path:        sourcePath,
		KeySource:   sourcePath,
		Source:      source.Source,
		States:      source.States,
		BypassTrust: source.BypassTrust,
		PluginID:    cloneString(source.PluginID),
		Env:         cloneHookEnv(source.Env),
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
		Path:        sourcePath,
		KeySource:   firstNonEmptyHookString(source.KeySource, "file:"+sourcePath),
		Source:      source.Source,
		States:      source.States,
		BypassTrust: source.BypassTrust,
		PluginID:    cloneString(source.PluginID),
		Env:         cloneHookEnv(source.Env),
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

func appendDiscoveredHookGroup(entry *HookListEntry, source *hookDiscoverySource, event HookEventName, group hookJSONMatcherGroupWire, groupIndex int, displayOrder int64) int64 {
	matcher := normalizedHookMatcher(event, group.Matcher)
	if matcher != nil {
		if err := validateHookMatcherPattern(*matcher); err != nil {
			entry.Warnings = append(entry.Warnings, fmt.Sprintf("invalid matcher %q in %s: %v", *matcher, source.Path, err))
			return displayOrder
		}
	}
	for handlerIndex := range group.Hooks {
		handler := group.Hooks[handlerIndex]
		handlerType := handler.hookHandlerType()
		switch handlerType {
		case HookHandlerCommand:
			command := handler.commandForPlatform()
			if handler.Async {
				entry.Warnings = append(entry.Warnings, fmt.Sprintf("skipping async hook in %s: async hooks are not supported yet", source.Path))
				continue
			}
			if strings.TrimSpace(command) == "" {
				entry.Warnings = append(entry.Warnings, fmt.Sprintf("skipping empty hook command in %s", source.Path))
				continue
			}
			timeoutSec := handler.timeoutSec()
			key := hookDiscoveryKey(source.KeySource, event, groupIndex, handlerIndex)
			currentHash := hookDiscoveryHash(event, matcher, command, timeoutSec, handler.statusMessage())
			state := source.State(key)
			displayCommand := expandHookEnvPlaceholders(command, source.Env)
			metadata := HookMetadata{
				Key:           key,
				EventName:     event,
				HandlerType:   HookHandlerCommand,
				Matcher:       cloneString(matcher),
				Command:       &displayCommand,
				TimeoutSec:    timeoutSec,
				StatusMessage: handler.statusMessage(),
				SourcePath:    source.Path,
				Source:        source.Source,
				PluginID:      cloneString(source.PluginID),
				DisplayOrder:  displayOrder,
				Enabled:       hookEnabled(false, state),
				IsManaged:     false,
				CurrentHash:   currentHash,
				TrustStatus:   hookTrustStatus(false, currentHash, hookTrustedHash(false, state)),
				BypassTrust:   source.BypassTrust,
				Env:           cloneHookEnv(source.Env),
			}
			entry.Hooks = append(entry.Hooks, metadata)
			displayOrder++
		case HookHandlerPrompt:
			entry.Warnings = append(entry.Warnings, fmt.Sprintf("skipping prompt hook in %s: prompt hooks are not supported yet", source.Path))
		case HookHandlerAgent:
			entry.Warnings = append(entry.Warnings, fmt.Sprintf("skipping agent hook in %s: agent hooks are not supported yet", source.Path))
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

func (h *hookJSONHandlerConfigWire) timeoutSec() int64 {
	if h == nil {
		return defaultDiscoveredHookTimeoutSec
	}
	value := h.Timeout
	if value == nil {
		value = h.TimeoutSec
	}
	if value == nil {
		value = h.TimeoutSecAlias
	}
	if value == nil {
		return defaultDiscoveredHookTimeoutSec
	}
	if *value == 0 {
		return 1
	}
	if *value > uint64(^uint64(0)>>1) {
		return int64(^uint64(0) >> 1)
	}
	return int64(*value)
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
		"UserPromptSubmit",
		"SubagentStart",
		"SubagentStop",
		"Stop",
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
	case "UserPromptSubmit":
		return HookEventUserPromptSubmit
	case "SubagentStart":
		return HookEventSubagentStart
	case "SubagentStop":
		return HookEventSubagentStop
	case "Stop":
		return HookEventStop
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
	if event == HookEventStop || event == HookEventUserPromptSubmit {
		return nil
	}
	value := strings.TrimSpace(*matcher)
	if value == "" {
		return nil
	}
	return &value
}

func hookDiscoveryHash(event HookEventName, matcher *string, command string, timeoutSec int64, statusMessage *string) string {
	handler := map[string]any{
		"type":    string(HookHandlerCommand),
		"command": command,
		"timeout": timeoutSec,
		"async":   false,
	}
	if statusMessage != nil {
		handler["statusMessage"] = *statusMessage
	}
	identity := map[string]any{
		"event_name": hookEventKeyLabel(event),
		"hooks":      []any{handler},
	}
	if matcher != nil {
		identity["matcher"] = *matcher
	}
	data, err := json.Marshal(identity)
	if err != nil {
		data = []byte(fmt.Sprintf("%s\x00%s\x00%s\x00%d", hookEventKeyLabel(event), ptrStringValue(matcher), command, timeoutSec))
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func hookEnabled(isManaged bool, state *HookState) bool {
	if isManaged {
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

func hookTrustStatus(isManaged bool, currentHash string, trustedHash *string) HookTrustStatus {
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
