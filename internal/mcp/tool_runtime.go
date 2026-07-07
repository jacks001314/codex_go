package mcp

import (
	"fmt"
	"sort"
	"strings"
)

const RuntimeCodexAppsMCPServerName = "codex_apps"

type RuntimeToolAnnotations struct {
	ReadOnlyHint    *bool `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool `json:"destructiveHint,omitempty"`
	OpenWorldHint   *bool `json:"openWorldHint,omitempty"`
}

type RuntimeTool struct {
	Name         string                  `json:"name"`
	Title        string                  `json:"title,omitempty"`
	Description  string                  `json:"description,omitempty"`
	InputSchema  map[string]any          `json:"inputSchema,omitempty"`
	Annotations  *RuntimeToolAnnotations `json:"annotations,omitempty"`
	ModelVisible *bool                   `json:"modelVisible,omitempty"`
}

type RuntimeToolInfo struct {
	ServerName         string      `json:"serverName"`
	ConnectorID        string      `json:"connectorId,omitempty"`
	PluginDisplayNames []string    `json:"pluginDisplayNames,omitempty"`
	Tool               RuntimeTool `json:"tool"`
}

func (t *RuntimeToolInfo) IsModelVisible() bool {
	if t == nil {
		return false
	}
	if t.Tool.ModelVisible == nil {
		return true
	}
	return *t.Tool.ModelVisible
}

type RuntimeConnector struct {
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	Enabled bool   `json:"enabled"`
}

type RuntimeExposure struct {
	DirectTools   []RuntimeToolInfo `json:"directTools"`
	DeferredTools []RuntimeToolInfo `json:"deferredTools,omitempty"`
}

func BuildRuntimeExposure(allTools []RuntimeToolInfo, connectors []RuntimeConnector, searchToolEnabled bool) *RuntimeExposure {
	deferred := filterRuntimeNonCodexApps(allTools)
	if len(connectors) > 0 {
		deferred = append(deferred, filterRuntimeCodexApps(allTools, connectors)...)
	}
	sortRuntimeTools(deferred)
	if !searchToolEnabled {
		return &RuntimeExposure{DirectTools: deferred}
	}
	return &RuntimeExposure{DeferredTools: deferred}
}

func AnnotateRuntimeToolsWithConnectorPluginProvenance(tools []RuntimeToolInfo, provenance *ConnectorPluginProvenance) []RuntimeToolInfo {
	out := make([]RuntimeToolInfo, len(tools))
	for i := range tools {
		out[i] = cloneRuntimeToolInfo(&tools[i])
		if out[i].ServerName != RuntimeCodexAppsMCPServerName || strings.TrimSpace(out[i].ConnectorID) == "" {
			continue
		}
		AnnotateRuntimeToolWithPluginProvenance(&out[i], provenance.Names(out[i].ConnectorID))
	}
	return out
}

func AnnotateRuntimeToolWithPluginProvenance(tool *RuntimeToolInfo, pluginNames []string) {
	if tool == nil {
		return
	}
	pluginNames = sortedNonEmptyUnique(pluginNames)
	tool.PluginDisplayNames = append([]string(nil), pluginNames...)
	if len(pluginNames) == 0 {
		return
	}
	note := ""
	if len(pluginNames) == 1 {
		note = fmt.Sprintf("This tool is part of plugin `%s`.", pluginNames[0])
	} else {
		quoted := make([]string, len(pluginNames))
		for i := range pluginNames {
			quoted[i] = fmt.Sprintf("`%s`", pluginNames[i])
		}
		note = fmt.Sprintf("This tool is part of plugins %s.", strings.Join(quoted, ", "))
	}
	description := strings.TrimSpace(tool.Tool.Description)
	switch {
	case description == "":
		tool.Tool.Description = note
	case strings.HasSuffix(description, ".") || strings.HasSuffix(description, "!") || strings.HasSuffix(description, "?"):
		tool.Tool.Description = description + " " + note
	default:
		tool.Tool.Description = description + ". " + note
	}
}

func ConnectorToolInfoFromRuntimeTools(tools []RuntimeToolInfo) []ConnectorToolInfo {
	out := make([]ConnectorToolInfo, 0, len(tools))
	for i := range tools {
		tool := tools[i]
		tool.ConnectorID = strings.TrimSpace(tool.ConnectorID)
		if tool.ServerName != RuntimeCodexAppsMCPServerName || tool.ConnectorID == "" {
			continue
		}
		out = append(out, ConnectorToolInfo{
			ServerName:         ConnectorCodexAppsMCPServerName,
			ConnectorID:        tool.ConnectorID,
			PluginDisplayNames: append([]string(nil), tool.PluginDisplayNames...),
		})
	}
	return out
}

type RuntimeDependency struct {
	Type      string `json:"type"`
	Value     string `json:"value"`
	Transport string `json:"transport,omitempty"`
	URL       string `json:"url,omitempty"`
	Command   string `json:"command,omitempty"`
}

type RuntimeSkillMetadata struct {
	Name         string              `json:"name"`
	Dependencies []RuntimeDependency `json:"dependencies,omitempty"`
}

type RuntimeServerConfig struct {
	Name      string   `json:"name"`
	Transport string   `json:"transport"`
	URL       string   `json:"url,omitempty"`
	Command   string   `json:"command,omitempty"`
	Args      []string `json:"args,omitempty"`
	Enabled   bool     `json:"enabled"`
	Required  bool     `json:"required,omitempty"`
}

func CollectMissingRuntimeDependencies(skills []RuntimeSkillMetadata, installed map[string]RuntimeServerConfig) map[string]RuntimeServerConfig {
	installedKeys := map[string]bool{}
	for name, config := range installed {
		if !config.Enabled {
			continue
		}
		installedKeys[CanonicalRuntimeServerKey(name, config)] = true
	}
	missing := map[string]RuntimeServerConfig{}
	seen := map[string]bool{}
	for _, skill := range skills {
		for _, dependency := range skill.Dependencies {
			if !strings.EqualFold(dependency.Type, "mcp") {
				continue
			}
			config, err := RuntimeDependencyToServerConfig(dependency)
			if err != nil {
				continue
			}
			dependencyName := strings.TrimSpace(config.Name)
			if dependencyName == "" {
				dependencyName = strings.TrimSpace(dependency.Value)
			}
			key := CanonicalRuntimeServerKey(dependencyName, config)
			if installedKeys[key] || seen[key] {
				continue
			}
			missing[dependencyName] = config
			seen[key] = true
		}
	}
	return missing
}

func RuntimeDependencyToServerConfig(dependency RuntimeDependency) (RuntimeServerConfig, error) {
	name := strings.TrimSpace(dependency.Value)
	if name == "" {
		return RuntimeServerConfig{}, fmt.Errorf("missing value for mcp dependency")
	}
	transport := runtimeDependencyTransport(dependency)
	if transport == "" {
		transport = "streamable_http"
	}
	url := strings.TrimSpace(dependency.URL)
	command := strings.TrimSpace(dependency.Command)
	if transport == "streamable_http" {
		if url == "" {
			return RuntimeServerConfig{}, fmt.Errorf("missing url for streamable_http dependency")
		}
		return RuntimeServerConfig{Name: name, Transport: "streamable_http", URL: url, Enabled: true, Required: true}, nil
	}
	if transport == "stdio" {
		if command == "" {
			return RuntimeServerConfig{}, fmt.Errorf("missing command for stdio dependency")
		}
		return RuntimeServerConfig{Name: name, Transport: "stdio", Command: command, Enabled: true, Required: true}, nil
	}
	return RuntimeServerConfig{}, fmt.Errorf("unsupported transport %s", transport)
}

func runtimeDependencyTransport(dependency RuntimeDependency) string {
	transport := normalizeRuntimeTransport(dependency.Transport)
	if transport != "" {
		return transport
	}
	if strings.TrimSpace(dependency.Command) != "" && strings.TrimSpace(dependency.URL) == "" {
		return "stdio"
	}
	return "streamable_http"
}

func runtimeServerConfigTransport(config RuntimeServerConfig) string {
	transport := normalizeRuntimeTransport(config.Transport)
	if transport != "" {
		return transport
	}
	if strings.TrimSpace(config.Command) != "" && strings.TrimSpace(config.URL) == "" {
		return "stdio"
	}
	if strings.TrimSpace(config.URL) != "" {
		return "streamable_http"
	}
	return ""
}

func normalizeRuntimeTransport(transport string) string {
	normalized := strings.ToLower(strings.TrimSpace(transport))
	normalized = strings.NewReplacer("-", "_", " ", "_").Replace(normalized)
	switch normalized {
	case "", "stdio":
		return normalized
	case "http", "sse", "server_sent_events", "streamablehttp", "streamable_http":
		return "streamable_http"
	default:
		return normalized
	}
}

func CanonicalRuntimeServerKey(name string, config RuntimeServerConfig) string {
	transport := runtimeServerConfigTransport(config)
	identifier := config.URL
	if transport == "stdio" {
		identifier = config.Command
	}
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return name
	}
	return "mcp__" + transport + "__" + identifier
}

func FormatMissingRuntimeDependencies(missing map[string]RuntimeServerConfig) string {
	names := make([]string, 0, len(missing))
	for name := range missing {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

type RuntimeApprovalTemplate struct {
	ConnectorID    string                         `json:"connectorId"`
	ServerName     string                         `json:"serverName"`
	ToolTitle      string                         `json:"toolTitle"`
	Template       string                         `json:"template"`
	TemplateParams []RuntimeApprovalTemplateParam `json:"templateParams,omitempty"`
}

type RuntimeApprovalTemplateParam struct {
	Name  string `json:"name"`
	Label string `json:"label"`
}

type RenderedRuntimeApprovalParam struct {
	Name        string `json:"name"`
	Value       any    `json:"value"`
	DisplayName string `json:"displayName"`
}

type RenderedRuntimeApprovalTemplate struct {
	Question           string                         `json:"question"`
	ElicitationMessage string                         `json:"elicitationMessage"`
	ToolParams         map[string]any                 `json:"toolParams,omitempty"`
	ToolParamsDisplay  []RenderedRuntimeApprovalParam `json:"toolParamsDisplay"`
}

func RenderRuntimeApprovalTemplate(templates []RuntimeApprovalTemplate, serverName string, connectorID string, connectorName string, toolTitle string, toolParams map[string]any) (*RenderedRuntimeApprovalTemplate, bool) {
	connectorID = strings.TrimSpace(connectorID)
	toolTitle = strings.TrimSpace(toolTitle)
	if connectorID == "" || toolTitle == "" {
		return nil, false
	}
	for _, template := range templates {
		if template.ServerName != serverName || template.ConnectorID != connectorID || template.ToolTitle != toolTitle {
			continue
		}
		question, ok := renderRuntimeQuestion(template.Template, connectorName)
		if !ok {
			return nil, false
		}
		params, ok := renderRuntimeParams(toolParams, template.TemplateParams)
		if !ok {
			return nil, false
		}
		return &RenderedRuntimeApprovalTemplate{
			Question:           question,
			ElicitationMessage: question,
			ToolParams:         cloneRuntimeAnyMap(toolParams),
			ToolParamsDisplay:  params,
		}, true
	}
	return nil, false
}

func filterRuntimeNonCodexApps(tools []RuntimeToolInfo) []RuntimeToolInfo {
	out := []RuntimeToolInfo{}
	for _, tool := range tools {
		copy := cloneRuntimeToolInfo(&tool)
		if copy.ServerName != RuntimeCodexAppsMCPServerName && (&copy).IsModelVisible() {
			out = append(out, copy)
		}
	}
	return out
}

func filterRuntimeCodexApps(tools []RuntimeToolInfo, connectors []RuntimeConnector) []RuntimeToolInfo {
	allowed := map[string]bool{}
	for _, connector := range connectors {
		id := strings.TrimSpace(connector.ID)
		if connector.Enabled && id != "" {
			allowed[id] = true
		}
	}
	out := []RuntimeToolInfo{}
	for _, tool := range tools {
		copy := cloneRuntimeToolInfo(&tool)
		copy.ConnectorID = strings.TrimSpace(copy.ConnectorID)
		if copy.ServerName == RuntimeCodexAppsMCPServerName && (&copy).IsModelVisible() && allowed[copy.ConnectorID] {
			out = append(out, copy)
		}
	}
	return out
}

func sortRuntimeTools(tools []RuntimeToolInfo) {
	sort.SliceStable(tools, func(i int, j int) bool {
		if tools[i].ServerName != tools[j].ServerName {
			return tools[i].ServerName < tools[j].ServerName
		}
		return tools[i].Tool.Name < tools[j].Tool.Name
	})
}

func renderRuntimeQuestion(template string, connectorName string) (string, bool) {
	template = strings.TrimSpace(template)
	if template == "" {
		return "", false
	}
	if strings.Contains(template, "{connector_name}") {
		connectorName = strings.TrimSpace(connectorName)
		if connectorName == "" {
			return "", false
		}
		template = strings.ReplaceAll(template, "{connector_name}", connectorName)
	}
	return template, true
}

func renderRuntimeParams(toolParams map[string]any, templateParams []RuntimeApprovalTemplateParam) ([]RenderedRuntimeApprovalParam, bool) {
	display := []RenderedRuntimeApprovalParam{}
	displayNames := map[string]bool{}
	handled := map[string]bool{}
	for _, templateParam := range templateParams {
		label := strings.TrimSpace(templateParam.Label)
		if label == "" || displayNames[label] {
			return nil, false
		}
		value, ok := toolParams[templateParam.Name]
		if !ok {
			continue
		}
		display = append(display, RenderedRuntimeApprovalParam{Name: templateParam.Name, Value: value, DisplayName: label})
		displayNames[label] = true
		handled[templateParam.Name] = true
	}
	names := make([]string, 0, len(toolParams))
	for name := range toolParams {
		if !handled[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if displayNames[name] {
			return nil, false
		}
		display = append(display, RenderedRuntimeApprovalParam{Name: name, Value: toolParams[name], DisplayName: name})
		displayNames[name] = true
	}
	return display, true
}

func cloneRuntimeAnyMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneRuntimeToolInfo(info *RuntimeToolInfo) RuntimeToolInfo {
	if info == nil {
		return RuntimeToolInfo{}
	}
	out := *info
	out.PluginDisplayNames = append([]string(nil), info.PluginDisplayNames...)
	out.Tool.InputSchema = cloneRuntimeAnyMap(info.Tool.InputSchema)
	return out
}

func sortedNonEmptyUnique(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
