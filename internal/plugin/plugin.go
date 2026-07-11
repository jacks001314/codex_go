package plugin

import (
	"fmt"
	"sort"
	"strings"
)

const (
	ToolMentionSigil       = '$'
	PluginTextMentionSigil = '@'
	AppsMCPServerName      = "codex-apps"
)

type UserInput struct {
	Type string
	Text string
	Path string
}

type CapabilitySummary struct {
	Name           string
	ConfigName     string
	DisplayName    string
	RemotePluginID string
	Description    string
	HasSkills      bool
	MCPServers     []string
	AppConnectors  []string
	Apps           []AppSummary
	AppTemplates   []AppTemplateSummary
}

type DiscoverableInfo struct {
	ID                 string
	RemotePluginID     string
	Name               string
	Description        string
	ToolType           string
	InstallURL         string
	PluginDisplayNames []string
	HasSkills          bool
	MCPServerNames     []string
	AppConnectorIDs    []string
}

type DiscoverableConfig struct {
	ConfiguredPluginIDs   []string
	DisabledPluginIDs     []string
	LoadedAppConnectorIDs []string
}

type ToolMentions struct {
	PlainNames map[string]bool
	Paths      map[string]bool
}

type ToolInfo struct {
	ServerName         string
	PluginDisplayNames []string
}

type AppInfo struct {
	ID                 string
	DisplayName        string
	Enabled            bool
	PluginDisplayNames []string
}

func ListDiscoverablePlugins(available []DiscoverableInfo, config *DiscoverableConfig) []DiscoverableInfo {
	if config == nil {
		return cloneDiscoverables(available)
	}
	configured := setFromSlice(config.ConfiguredPluginIDs)
	disabled := setFromSlice(config.DisabledPluginIDs)
	loadedApps := setFromSlice(config.LoadedAppConnectorIDs)
	out := make([]DiscoverableInfo, 0, len(available))
	for _, plugin := range available {
		if disabled[plugin.ID] || disabled[plugin.RemotePluginID] {
			continue
		}
		if len(configured) > 0 && !configured[plugin.ID] && !configured[plugin.RemotePluginID] {
			continue
		}
		if hasAny(plugin.AppConnectorIDs, loadedApps) {
			continue
		}
		out = append(out, cloneDiscoverable(&plugin))
	}
	sort.SliceStable(out, func(i int, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func CollectToolMentionsFromMessages(messages []string) *ToolMentions {
	return collectToolMentionsFromMessagesWithSigil(messages, ToolMentionSigil)
}

func CollectExplicitAppIDs(input []UserInput) map[string]bool {
	messages := textMessages(input)
	paths := make(map[string]bool)
	for _, item := range input {
		if item.Type == "mention" && toolKindForPath(item.Path) == "app" {
			if id := appIDFromPath(item.Path); id != "" {
				paths[id] = true
			}
		}
	}
	mentions := CollectToolMentionsFromMessages(messages)
	for path := range mentions.Paths {
		if toolKindForPath(path) == "app" {
			if id := appIDFromPath(path); id != "" {
				paths[id] = true
			}
		}
	}
	return paths
}

func CollectExplicitPluginMentions(input []UserInput, plugins []CapabilitySummary) []CapabilitySummary {
	if len(plugins) == 0 {
		return nil
	}
	messages := textMessages(input)
	mentioned := make(map[string]bool)
	for _, item := range input {
		if item.Type == "mention" && toolKindForPath(item.Path) == "plugin" {
			if name := pluginConfigNameFromPath(item.Path); name != "" {
				mentioned[name] = true
			}
		}
	}
	mentions := collectToolMentionsFromMessagesWithSigil(messages, PluginTextMentionSigil)
	for path := range mentions.Paths {
		if toolKindForPath(path) == "plugin" {
			if name := pluginConfigNameFromPath(path); name != "" {
				mentioned[name] = true
			}
		}
	}
	if len(mentioned) == 0 {
		return nil
	}
	out := make([]CapabilitySummary, 0, len(plugins))
	for _, plugin := range plugins {
		if mentioned[plugin.ConfigName] {
			out = append(out, cloneCapability(&plugin))
		}
	}
	return out
}

func RenderExplicitPluginInstructions(plugin *CapabilitySummary, availableMCPServers []string, availableApps []string) (string, bool) {
	if plugin == nil {
		return "", false
	}
	lines := []string{fmt.Sprintf("Capabilities from the `%s` plugin:", plugin.DisplayName)}
	if plugin.HasSkills {
		lines = append(lines, fmt.Sprintf("- Skills from this plugin are prefixed with `%s:`.", plugin.DisplayName))
	}
	if len(availableMCPServers) > 0 {
		lines = append(lines, fmt.Sprintf(
			"- MCP servers from this plugin available in this session: %s.",
			formatBacktickList(availableMCPServers),
		))
	}
	if len(availableApps) > 0 {
		lines = append(lines, fmt.Sprintf(
			"- Apps from this plugin available in this session: %s.",
			formatBacktickList(availableApps),
		))
	}
	if len(lines) == 1 {
		return "", false
	}
	lines = append(lines, "Use these plugin-associated capabilities to help solve the task.")
	return strings.Join(lines, "\n"), true
}

func BuildPluginInjections(mentioned []CapabilitySummary, mcpTools []ToolInfo, apps []AppInfo) []string {
	if len(mentioned) == 0 {
		return nil
	}
	out := make([]string, 0, len(mentioned))
	for _, plugin := range mentioned {
		servers := make(map[string]bool)
		for _, tool := range mcpTools {
			if tool.ServerName == AppsMCPServerName {
				continue
			}
			if contains(tool.PluginDisplayNames, plugin.DisplayName) {
				servers[tool.ServerName] = true
			}
		}
		appNames := make(map[string]bool)
		for _, app := range apps {
			if app.Enabled && contains(app.PluginDisplayNames, plugin.DisplayName) {
				appNames[connectorDisplayLabel(&app)] = true
			}
		}
		rendered, ok := RenderExplicitPluginInstructions(&plugin, sortedKeys(servers), sortedKeys(appNames))
		if ok {
			out = append(out, rendered)
		}
	}
	return out
}

func collectToolMentionsFromMessagesWithSigil(messages []string, sigil rune) *ToolMentions {
	mentions := &ToolMentions{
		PlainNames: make(map[string]bool),
		Paths:      make(map[string]bool),
	}
	for _, message := range messages {
		collectMentionsFromText(message, sigil, mentions)
	}
	return mentions
}

func collectMentionsFromText(text string, sigil rune, mentions *ToolMentions) {
	if sigil > 127 {
		return
	}
	sigilByte := byte(sigil)
	textBytes := []byte(text)
	for i := 0; i < len(textBytes); {
		if textBytes[i] == '[' {
			if name, path, end, ok := parseLinkedToolMention(text, textBytes, i, sigilByte); ok {
				if !isCommonEnvVar(name) {
					mentions.Paths[path] = true
				}
				i = end
				continue
			}
		}
		if textBytes[i] != sigilByte {
			i++
			continue
		}
		start := i + 1
		if start >= len(textBytes) || !isMentionNameChar(textBytes[start]) {
			i++
			continue
		}
		end := start + 1
		for end < len(textBytes) && isMentionNameChar(textBytes[end]) {
			end++
		}
		token := text[start:end]
		if !isCommonEnvVar(token) {
			mentions.PlainNames[token] = true
		}
		i = end
	}
}

func parseLinkedToolMention(text string, textBytes []byte, start int, sigil byte) (string, string, int, bool) {
	sigilIndex := start + 1
	if sigilIndex >= len(textBytes) || textBytes[sigilIndex] != sigil {
		return "", "", 0, false
	}
	nameStart := sigilIndex + 1
	if nameStart >= len(textBytes) || !isMentionNameChar(textBytes[nameStart]) {
		return "", "", 0, false
	}
	nameEnd := nameStart + 1
	for nameEnd < len(textBytes) && isMentionNameChar(textBytes[nameEnd]) {
		nameEnd++
	}
	if nameEnd >= len(textBytes) || textBytes[nameEnd] != ']' {
		return "", "", 0, false
	}
	pathStart := nameEnd + 1
	for pathStart < len(textBytes) && isASCIIWhitespace(textBytes[pathStart]) {
		pathStart++
	}
	if pathStart >= len(textBytes) || textBytes[pathStart] != '(' {
		return "", "", 0, false
	}
	pathEnd := pathStart + 1
	for pathEnd < len(textBytes) && textBytes[pathEnd] != ')' {
		pathEnd++
	}
	if pathEnd >= len(textBytes) || textBytes[pathEnd] != ')' {
		return "", "", 0, false
	}
	path := strings.TrimSpace(text[pathStart+1 : pathEnd])
	if path == "" {
		return "", "", 0, false
	}
	return text[nameStart:nameEnd], path, pathEnd + 1, true
}

func isMentionNameChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_' || b == '-' || b == ':'
}

func isASCIIWhitespace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\v' || b == '\f'
}

func isCommonEnvVar(name string) bool {
	switch strings.ToUpper(name) {
	case "PATH", "HOME", "USER", "SHELL", "PWD", "TMPDIR", "TEMP", "TMP", "LANG", "TERM", "XDG_CONFIG_HOME":
		return true
	default:
		return false
	}
}

func textMessages(input []UserInput) []string {
	messages := make([]string, 0, len(input))
	for _, item := range input {
		if item.Type == "text" {
			messages = append(messages, item.Text)
		}
	}
	return messages
}

func toolKindForPath(path string) string {
	switch {
	case strings.HasPrefix(path, "plugin://"):
		return "plugin"
	case strings.HasPrefix(path, "app://"):
		return "app"
	default:
		return ""
	}
}

func pluginConfigNameFromPath(path string) string {
	return mentionIDFromPath(path, "plugin://")
}

func appIDFromPath(path string) string {
	return mentionIDFromPath(path, "app://")
}

func mentionIDFromPath(path string, prefix string) string {
	value := strings.TrimPrefix(path, prefix)
	if value == path {
		return ""
	}
	if value == "" {
		return ""
	}
	return value
}

func connectorDisplayLabel(app *AppInfo) string {
	if app == nil {
		return ""
	}
	if app.DisplayName != "" {
		return app.DisplayName
	}
	return app.ID
}

func formatBacktickList(values []string) string {
	sorted := append([]string(nil), values...)
	sort.Strings(sorted)
	for i, value := range sorted {
		sorted[i] = fmt.Sprintf("`%s`", value)
	}
	return strings.Join(sorted, ", ")
}

func cloneCapability(plugin *CapabilitySummary) CapabilitySummary {
	if plugin == nil {
		return CapabilitySummary{}
	}
	return CapabilitySummary{
		Name:           plugin.Name,
		ConfigName:     plugin.ConfigName,
		DisplayName:    plugin.DisplayName,
		RemotePluginID: plugin.RemotePluginID,
		Description:    plugin.Description,
		HasSkills:      plugin.HasSkills,
		MCPServers:     append([]string(nil), plugin.MCPServers...),
		AppConnectors:  append([]string(nil), plugin.AppConnectors...),
		Apps:           cloneAppSummaries(plugin.Apps),
		AppTemplates:   cloneAppTemplateSummaries(plugin.AppTemplates),
	}
}

func cloneDiscoverable(plugin *DiscoverableInfo) DiscoverableInfo {
	if plugin == nil {
		return DiscoverableInfo{}
	}
	return DiscoverableInfo{
		ID:                 plugin.ID,
		RemotePluginID:     plugin.RemotePluginID,
		Name:               plugin.Name,
		Description:        plugin.Description,
		ToolType:           plugin.ToolType,
		InstallURL:         plugin.InstallURL,
		PluginDisplayNames: append([]string(nil), plugin.PluginDisplayNames...),
		HasSkills:          plugin.HasSkills,
		MCPServerNames:     append([]string(nil), plugin.MCPServerNames...),
		AppConnectorIDs:    append([]string(nil), plugin.AppConnectorIDs...),
	}
}

func cloneDiscoverables(plugins []DiscoverableInfo) []DiscoverableInfo {
	out := make([]DiscoverableInfo, len(plugins))
	for i := range plugins {
		out[i] = cloneDiscoverable(&plugins[i])
	}
	return out
}

func setFromSlice(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		set[value] = true
	}
	return set
}

func hasAny(values []string, set map[string]bool) bool {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if set[value] {
			return true
		}
	}
	return false
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func sortedKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
