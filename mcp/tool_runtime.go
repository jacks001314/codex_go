package mcp

import (
	"crypto/sha1"
	"encoding/json"
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
	ServerName           string      `json:"serverName"`
	CallableName         string      `json:"toolName,omitempty"`
	CallableNamespace    string      `json:"toolNamespace,omitempty"`
	NamespaceDescription string      `json:"namespaceDescription,omitempty"`
	ConnectorID          string      `json:"connectorId,omitempty"`
	ConnectorName        string      `json:"connectorName,omitempty"`
	PluginDisplayNames   []string    `json:"pluginDisplayNames,omitempty"`
	Tool                 RuntimeTool `json:"tool"`
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
	allTools = NormalizeRuntimeToolsForModel(allTools)
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

func RuntimeToolsFromStatuses(statuses []MCPServerStatus) []RuntimeToolInfo {
	out := make([]RuntimeToolInfo, 0)
	for i := range statuses {
		status := statuses[i]
		if status.State != "" && status.State != MCPServerReady {
			continue
		}
		serverName := RuntimeServerNameFromStatus(&status)
		if serverName == "" {
			continue
		}
		runtimeServerName := serverName
		if IsCodexAppsMCPServerName(serverName) {
			runtimeServerName = RuntimeCodexAppsMCPServerName
		}
		for j := range status.Tools {
			toolInfo := status.Tools[j]
			toolName := strings.TrimSpace(toolInfo.Name)
			if toolName == "" || ToolSyntheticLink(toolInfo.Meta) {
				continue
			}
			runtimeTool := RuntimeToolInfo{
				ServerName: runtimeServerName,
				Tool: RuntimeTool{
					Name:         toolName,
					Title:        strings.TrimSpace(toolInfo.Title),
					Description:  strings.TrimSpace(toolInfo.Description),
					InputSchema:  cloneAnyMap(toolInfo.InputSchema),
					Annotations:  RuntimeToolAnnotationsFromMCP(toolInfo.Annotations),
					ModelVisible: ToolModelVisible(&toolInfo),
				},
			}
			if connector := ConnectorToolInfoFromMCPTool(serverName, &toolInfo); connector != nil {
				runtimeTool.ConnectorID = strings.TrimSpace(connector.ConnectorID)
				runtimeTool.ConnectorName = strings.TrimSpace(connector.ConnectorName)
				runtimeTool.NamespaceDescription = firstNonEmpty(
					strings.TrimSpace(connector.NamespaceDescription),
					strings.TrimSpace(connector.ConnectorDescription),
				)
				runtimeTool.PluginDisplayNames = append([]string(nil), connector.PluginDisplayNames...)
				if runtimeTool.Tool.Description == "" {
					runtimeTool.Tool.Description = runtimeTool.NamespaceDescription
				}
			}
			out = append(out, runtimeTool)
		}
	}
	return NormalizeRuntimeToolsForModel(out)
}

const (
	runtimeMCPToolNameMaxLength = 64
	runtimeMCPHashLength        = 12
)

type runtimeToolNameCandidate struct {
	tool                 RuntimeToolInfo
	namespace            string
	name                 string
	rawNamespaceIdentity string
	rawToolIdentity      string
}

// NormalizeRuntimeToolsForModel mirrors Rust Codex's split between raw MCP
// identities and model-visible callable names. Raw names remain available for
// tools/call while callable names are sanitized, prefixed, and deduplicated.
func NormalizeRuntimeToolsForModel(tools []RuntimeToolInfo) []RuntimeToolInfo {
	candidates := make([]runtimeToolNameCandidate, 0, len(tools))
	seenRawTools := map[string]bool{}
	for i := range tools {
		info := cloneRuntimeToolInfo(&tools[i])
		info.ServerName = strings.TrimSpace(info.ServerName)
		info.ConnectorID = strings.TrimSpace(info.ConnectorID)
		info.ConnectorName = strings.TrimSpace(info.ConnectorName)

		rawNamespace := firstNonEmpty(strings.TrimSpace(info.CallableNamespace), info.ServerName)
		rawName := firstNonEmpty(strings.TrimSpace(info.CallableName), strings.TrimSpace(info.Tool.Name))
		if IsCodexAppsMCPServerName(info.ServerName) {
			rawNamespace = runtimeCodexAppsCallableNamespace(info, rawNamespace)
			rawName = runtimeCodexAppsCallableName(info, rawName)
		}
		rawNamespaceIdentity := strings.Join([]string{info.ServerName, info.ConnectorID, info.ConnectorName}, "\x00")
		rawToolIdentity := strings.Join([]string{rawNamespaceIdentity, info.Tool.Name}, "\x00")
		if seenRawTools[rawToolIdentity] {
			continue
		}
		seenRawTools[rawToolIdentity] = true

		namespace := sanitizeRuntimeMCPToolName(rawNamespace)
		if !strings.HasPrefix(namespace, LegacyMCPToolNamePrefix) {
			namespace = LegacyMCPToolNamePrefix + namespace
		}
		candidates = append(candidates, runtimeToolNameCandidate{
			tool:                 info,
			namespace:            namespace,
			name:                 sanitizeRuntimeMCPToolName(rawName),
			rawNamespaceIdentity: rawNamespaceIdentity,
			rawToolIdentity:      rawToolIdentity,
		})
	}

	namespaceIdentities := map[string]map[string]bool{}
	for i := range candidates {
		identities := namespaceIdentities[candidates[i].namespace]
		if identities == nil {
			identities = map[string]bool{}
			namespaceIdentities[candidates[i].namespace] = identities
		}
		identities[candidates[i].rawNamespaceIdentity] = true
	}
	for i := range candidates {
		if len(namespaceIdentities[candidates[i].namespace]) > 1 {
			candidates[i].namespace = appendRuntimeMCPHash(candidates[i].namespace, candidates[i].rawNamespaceIdentity)
		}
	}

	toolIdentities := map[string]map[string]bool{}
	for i := range candidates {
		key := candidates[i].namespace + "\x00" + candidates[i].name
		identities := toolIdentities[key]
		if identities == nil {
			identities = map[string]bool{}
			toolIdentities[key] = identities
		}
		identities[candidates[i].rawToolIdentity] = true
	}
	for i := range candidates {
		key := candidates[i].namespace + "\x00" + candidates[i].name
		if len(toolIdentities[key]) > 1 {
			candidates[i].name = appendRuntimeMCPHash(candidates[i].name, candidates[i].rawToolIdentity)
		}
	}

	used := map[string]bool{}
	out := make([]RuntimeToolInfo, 0, len(candidates))
	for i := range candidates {
		namespace, name := uniqueRuntimeMCPCallableParts(
			candidates[i].namespace,
			candidates[i].name,
			candidates[i].rawToolIdentity,
			used,
		)
		info := candidates[i].tool
		info.CallableNamespace = namespace
		info.CallableName = name
		out = append(out, info)
	}
	return out
}

func runtimeCodexAppsCallableNamespace(info RuntimeToolInfo, fallback string) string {
	base := firstNonEmpty(strings.TrimSpace(fallback), RuntimeCodexAppsMCPServerName)
	if strings.HasPrefix(base, LegacyMCPToolNamePrefix) || strings.Contains(base, MCPToolNameDelimiter) {
		return base
	}
	connector := firstNonEmpty(info.ConnectorName, info.ConnectorID)
	if connector == "" {
		return base
	}
	return base + MCPToolNameDelimiter + sanitizeRuntimeConnectorName(connector)
}

func runtimeCodexAppsCallableName(info RuntimeToolInfo, fallback string) string {
	name := sanitizeRuntimeConnectorName(fallback)
	for _, connector := range []string{info.ConnectorName, info.ConnectorID} {
		prefix := sanitizeRuntimeConnectorName(connector)
		if prefix == "" {
			continue
		}
		if stripped, ok := strings.CutPrefix(name, prefix); ok && stripped != "" {
			return strings.TrimLeft(stripped, "_")
		}
	}
	return name
}

func sanitizeRuntimeConnectorName(value string) string {
	return strings.ToLower(sanitizeRuntimeMCPToolName(strings.TrimSpace(value)))
}

func sanitizeRuntimeMCPToolName(value string) string {
	var out strings.Builder
	out.Grow(len(value))
	for _, ch := range strings.TrimSpace(value) {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' {
			out.WriteRune(ch)
			continue
		}
		out.WriteByte('_')
	}
	return out.String()
}

func runtimeMCPHashSuffix(identity string) string {
	digest := sha1.Sum([]byte(identity))
	return fmt.Sprintf("_%x", digest)[:runtimeMCPHashLength+1]
}

func appendRuntimeMCPHash(value string, identity string) string {
	return value + runtimeMCPHashSuffix(identity)
}

func fitRuntimeMCPCallableParts(namespace string, name string, identity string) (string, string) {
	suffix := runtimeMCPHashSuffix(identity)
	maxNameLength := runtimeMCPToolNameMaxLength - len(namespace) - len(MCPToolNameDelimiter)
	if maxNameLength >= len(suffix) {
		return namespace, truncateRuntimeMCPName(name, maxNameLength-len(suffix)) + suffix
	}
	maxNamespaceLength := runtimeMCPToolNameMaxLength - len(suffix) - len(MCPToolNameDelimiter)
	return truncateRuntimeMCPName(namespace, maxNamespaceLength), suffix
}

func uniqueRuntimeMCPCallableParts(namespace string, name string, identity string, used map[string]bool) (string, string) {
	key := namespace + MCPToolNameDelimiter + name
	if len(key) <= runtimeMCPToolNameMaxLength && !used[key] {
		used[key] = true
		return namespace, name
	}
	for attempt := 0; ; attempt++ {
		hashInput := identity
		if attempt > 0 {
			hashInput = fmt.Sprintf("%s\x00%d", identity, attempt)
		}
		candidateNamespace, candidateName := fitRuntimeMCPCallableParts(namespace, name, hashInput)
		key = candidateNamespace + MCPToolNameDelimiter + candidateName
		if !used[key] {
			used[key] = true
			return candidateNamespace, candidateName
		}
	}
}

func truncateRuntimeMCPName(value string, maxLength int) string {
	if maxLength <= 0 {
		return ""
	}
	if len(value) <= maxLength {
		return value
	}
	return value[:maxLength]
}

func RuntimeServerNameFromStatus(status *MCPServerStatus) string {
	if status == nil {
		return ""
	}
	if name := strings.TrimSpace(status.Name); name != "" {
		return name
	}
	if name := strings.TrimSpace(status.Server.Name); name != "" {
		return name
	}
	if status.ServerInfo != nil {
		return strings.TrimSpace(status.ServerInfo.Name)
	}
	return ""
}

func RuntimeToolAnnotationsFromMCP(value any) *RuntimeToolAnnotations {
	if value == nil {
		return nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var annotations RuntimeToolAnnotations
	if err := json.Unmarshal(encoded, &annotations); err != nil {
		return nil
	}
	if annotations.ReadOnlyHint == nil && annotations.DestructiveHint == nil && annotations.OpenWorldHint == nil {
		return nil
	}
	return &annotations
}

func ToolModelVisible(toolInfo *MCPToolInfo) *bool {
	if toolInfo == nil {
		return nil
	}
	if value, ok := boolMetadataValue(toolInfo.Meta, "modelVisible", "model_visible"); ok {
		return value
	}
	if value, ok := boolMetadataValue(toolInfo.Annotations, "modelVisible", "model_visible"); ok {
		return value
	}
	return nil
}

func ToolSyntheticLink(meta any) bool {
	value, ok := boolMetadataValue(meta, "synthetic_link", "syntheticLink")
	return ok && value != nil && *value
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

func boolMetadataValue(value any, keys ...string) (*bool, bool) {
	for _, values := range metadataMaps(value) {
		for _, key := range keys {
			raw, ok := values[key]
			if !ok {
				continue
			}
			switch typed := raw.(type) {
			case bool:
				return &typed, true
			case string:
				switch strings.ToLower(strings.TrimSpace(typed)) {
				case "true":
					result := true
					return &result, true
				case "false":
					result := false
					return &result, true
				}
			}
		}
	}
	return nil, false
}

func metadataMaps(value any) []map[string]any {
	base := metadataMap(value)
	if base == nil {
		return nil
	}
	out := []map[string]any{base}
	for _, key := range []string{"_codex_apps", "codex_apps", "codexApps"} {
		if nested := metadataMap(base[key]); nested != nil {
			out = append(out, nested)
		}
	}
	return out
}

func metadataMap(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		return typed
	case map[string]string:
		out := make(map[string]any, len(typed))
		for key, value := range typed {
			out[key] = value
		}
		return out
	default:
		if value == nil {
			return nil
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil
		}
		var decoded map[string]any
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			return nil
		}
		if len(decoded) == 0 {
			return nil
		}
		return decoded
	}
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
		return RuntimeServerConfig{Name: name, Transport: "streamable_http", URL: url, Enabled: true}, nil
	}
	if transport == "stdio" {
		if command == "" {
			return RuntimeServerConfig{}, fmt.Errorf("missing command for stdio dependency")
		}
		return RuntimeServerConfig{Name: name, Transport: "stdio", Command: command, Enabled: true}, nil
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
