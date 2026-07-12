package mcp

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	CodexAppsServerName                = "codex_apps"
	legacyCodexAppsRegistrationID      = "legacy_codex_apps"
	consequentialTemplateSchemaVersion = 4
	connectorNameTemplateVar           = "{connector_name}"
)

type ServerConfig struct {
	Command           string            `json:"command,omitempty"`
	Args              []string          `json:"args,omitempty"`
	Env               map[string]string `json:"env,omitempty"`
	EnvVars           []EnvVar          `json:"env_vars,omitempty"`
	CWD               string            `json:"cwd,omitempty"`
	URL               string            `json:"url,omitempty"`
	BearerTokenEnvVar string            `json:"bearer_token_env_var,omitempty"`
	HTTPHeaders       map[string]string `json:"http_headers,omitempty"`
	EnvHTTPHeaders    map[string]string `json:"env_http_headers,omitempty"`
	OAuthClientID     string            `json:"oauth_client_id,omitempty"`
	OAuthResource     string            `json:"oauth_resource,omitempty"`
	Scopes            []string          `json:"scopes,omitempty"`
	ScopesConfigured  bool              `json:"-"`
	OAuthServerName   string            `json:"-"`
	CodexHome         string            `json:"-"`
	Enabled           bool              `json:"enabled"`
	DisabledReason    string            `json:"disabled_reason,omitempty"`
	Required          bool              `json:"required,omitempty"`
	EnvironmentID     string            `json:"environment_id,omitempty"`
}

type EnvVar struct {
	Name   string `json:"name"`
	Source string `json:"source,omitempty"`
}

type ServerRegistration struct {
	Name              string
	Config            ServerConfig
	Source            string
	ContributorID     string
	ContributionOrder int
	PluginID          string
	PluginDisplayName string
	SelectionOrder    int
}

type RuntimeConfig struct {
	Servers              map[string]ServerRegistration
	AppsEnabled          bool
	ChatGPTBaseURL       string
	AppsMCPProductSKU    string
	ConnectorIDs         []string
	AvailableEnvironment []string
	CodexHome            string
}

func RuntimeConfigFromValues(values map[string]any, codexHome string) *RuntimeConfig {
	out := &RuntimeConfig{
		Servers:              map[string]ServerRegistration{},
		AppsEnabled:          appsEnabledFromRuntimeConfigValues(values),
		ChatGPTBaseURL:       runtimeConfigStringAny(values, "chatgpt_base_url", "chatgptBaseUrl", "chatgptBaseURL"),
		AppsMCPProductSKU:    runtimeConfigStringAny(values, "apps_mcp_product_sku", "appsMcpProductSku", "appsMCPProductSKU"),
		CodexHome:            strings.TrimSpace(codexHome),
		AvailableEnvironment: runtimeConfigStringSliceAny(values, "available_environment", "availableEnvironment"),
		ConnectorIDs:         runtimeConfigStringSliceAny(values, "connector_ids", "connectorIds"),
	}
	rawServers, ok := runtimeConfigMapAny(values, "mcp_servers", "mcpServers")
	if !ok {
		return NewManager(nil).RuntimeConfig(*out, nil)
	}
	for name, raw := range rawServers {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		table, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		config := runtimeServerConfigFromValues(table)
		out.Servers[name] = ServerRegistration{
			Name:   name,
			Config: *config,
			Source: "config",
		}
	}
	return NewManager(nil).RuntimeConfig(*out, nil)
}

type ConfigOverlay struct {
	Name              string
	Config            ServerConfig
	Remove            bool
	ContributorID     string
	ContributionOrder int
}

type Manager struct {
	baseServers map[string]ServerRegistration
}

func NewManager(baseServers map[string]ServerRegistration) *Manager {
	return &Manager{baseServers: cloneRegistrations(baseServers)}
}

func (m *Manager) RuntimeConfig(base RuntimeConfig, overlays []ConfigOverlay) *RuntimeConfig {
	servers := cloneRegistrations(m.baseServers)
	for name, registration := range base.Servers {
		registration.Name = name
		servers[name] = cloneRegistration(&registration)
	}
	if base.AppsEnabled {
		servers[CodexAppsServerName] = ServerRegistration{
			Name:          CodexAppsServerName,
			Config:        CodexAppsServerConfig(base.ChatGPTBaseURL, base.AppsMCPProductSKU),
			Source:        "compatibility",
			ContributorID: legacyCodexAppsRegistrationID,
		}
	} else {
		if registration, ok := servers[CodexAppsServerName]; ok && registration.ContributorID == legacyCodexAppsRegistrationID {
			delete(servers, CodexAppsServerName)
		}
	}
	sort.SliceStable(overlays, func(i int, j int) bool {
		if overlays[i].ContributionOrder == overlays[j].ContributionOrder {
			return overlays[i].Name < overlays[j].Name
		}
		return overlays[i].ContributionOrder < overlays[j].ContributionOrder
	})
	for _, overlay := range overlays {
		if overlay.Remove {
			delete(servers, overlay.Name)
			continue
		}
		servers[overlay.Name] = ServerRegistration{
			Name:              overlay.Name,
			Config:            cloneServerConfig(&overlay.Config),
			Source:            "extension",
			ContributorID:     overlay.ContributorID,
			ContributionOrder: overlay.ContributionOrder,
		}
	}
	out := base
	out.Servers = servers
	return &out
}

func appsEnabledFromRuntimeConfigValues(values map[string]any) bool {
	enabled := true
	if features, ok := values["features"].(map[string]any); ok {
		if value, ok := features["apps"].(bool); ok {
			enabled = value
		}
	}
	if apps, ok := values["apps"].(map[string]any); ok {
		if value, ok := apps["enabled"].(bool); ok {
			enabled = value
		}
	}
	return enabled
}

func runtimeServerConfigFromValues(values map[string]any) *ServerConfig {
	server := &ServerConfig{Enabled: true}
	if value, ok := values["enabled"].(bool); ok {
		server.Enabled = value
	}
	if value, ok := values["required"].(bool); ok {
		server.Required = value
	}
	server.DisabledReason = runtimeConfigStringAny(values, "disabled_reason", "disabledReason")
	server.EnvironmentID = runtimeConfigStringAny(values, "environment_id", "environmentId")
	if url := runtimeConfigString(values, "url"); url != "" {
		server.URL = url
		server.BearerTokenEnvVar = runtimeConfigStringAny(values, "bearer_token_env_var", "bearerTokenEnvVar")
		server.HTTPHeaders = runtimeConfigStringMap(values, "http_headers")
		server.EnvHTTPHeaders = runtimeConfigStringMap(values, "env_http_headers")
		server.OAuthClientID = runtimeConfigOAuthClientID(values)
		server.OAuthResource = runtimeConfigStringAny(values, "oauth_resource", "oauthResource")
		server.Scopes, server.ScopesConfigured = runtimeConfigOptionalStringSlice(values, "scopes")
		return server
	}
	server.Command = runtimeConfigString(values, "command")
	server.Args = runtimeConfigStringSlice(values, "args")
	server.Env = runtimeConfigStringMap(values, "env")
	server.EnvVars = runtimeConfigEnvVars(values["env_vars"])
	server.CWD = runtimeConfigString(values, "cwd")
	return server
}

func runtimeConfigMapAny(values map[string]any, keys ...string) (map[string]any, bool) {
	if values == nil {
		return nil, false
	}
	for _, key := range keys {
		table, ok := values[key].(map[string]any)
		if ok {
			return table, true
		}
	}
	return nil, false
}

func runtimeConfigStringAny(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := runtimeConfigString(values, key); value != "" {
			return value
		}
	}
	return ""
}

func runtimeConfigOAuthClientID(values map[string]any) string {
	if value := runtimeConfigStringAny(values, "oauth_client_id", "oauthClientId"); value != "" {
		return value
	}
	oauth, ok := runtimeConfigMapAny(values, "oauth")
	if !ok {
		return ""
	}
	return runtimeConfigStringAny(oauth, "client_id", "clientId")
}

func runtimeConfigStringSliceAny(values map[string]any, keys ...string) []string {
	for _, key := range keys {
		if values := runtimeConfigStringSlice(values, key); len(values) > 0 {
			return values
		}
	}
	return nil
}

func runtimeConfigOptionalStringSlice(values map[string]any, key string) ([]string, bool) {
	if values == nil {
		return nil, false
	}
	if _, ok := values[key]; !ok {
		return nil, false
	}
	return runtimeConfigStringSlice(values, key), true
}

func runtimeConfigString(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, ok := values[key]
	if !ok {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

func runtimeConfigStringSlice(values map[string]any, key string) []string {
	if values == nil {
		return nil
	}
	switch typed := values[key].(type) {
	case []string:
		out := make([]string, 0, len(typed))
		for _, value := range typed {
			value = strings.TrimSpace(value)
			if value != "" {
				out = append(out, value)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(typed))
		for _, value := range typed {
			text, ok := value.(string)
			if !ok {
				continue
			}
			text = strings.TrimSpace(text)
			if text != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func runtimeConfigStringMap(values map[string]any, key string) map[string]string {
	if values == nil {
		return nil
	}
	out := map[string]string{}
	switch table := values[key].(type) {
	case map[string]string:
		for key, value := range table {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			out[key] = strings.TrimSpace(value)
		}
	case map[string]any:
		for key, raw := range table {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			text, ok := raw.(string)
			if !ok {
				continue
			}
			out[key] = strings.TrimSpace(text)
		}
	default:
		return nil
	}
	return out
}

func runtimeConfigEnvVars(value any) []EnvVar {
	switch typed := value.(type) {
	case []EnvVar:
		return append([]EnvVar(nil), typed...)
	case []string:
		out := make([]EnvVar, 0, len(typed))
		for _, name := range typed {
			if name = strings.TrimSpace(name); name != "" {
				out = append(out, EnvVar{Name: name})
			}
		}
		return out
	case []any:
		out := make([]EnvVar, 0, len(typed))
		for _, item := range typed {
			switch entry := item.(type) {
			case string:
				if name := strings.TrimSpace(entry); name != "" {
					out = append(out, EnvVar{Name: name})
				}
			case map[string]any:
				name := runtimeConfigString(entry, "name")
				if name == "" {
					continue
				}
				out = append(out, EnvVar{Name: name, Source: runtimeConfigString(entry, "source")})
			case map[string]string:
				name := strings.TrimSpace(entry["name"])
				if name == "" {
					continue
				}
				out = append(out, EnvVar{Name: name, Source: strings.TrimSpace(entry["source"])})
			}
		}
		return out
	default:
		return nil
	}
}

func (m *Manager) ConfiguredServers(config *RuntimeConfig) map[string]ServerConfig {
	if config == nil {
		return nil
	}
	out := make(map[string]ServerConfig, len(config.Servers))
	for name, registration := range config.Servers {
		if registration.Config.Enabled {
			out[name] = cloneServerConfig(&registration.Config)
		}
	}
	return out
}

func (m *Manager) EffectiveServers(config *RuntimeConfig, authenticated bool) map[string]ServerConfig {
	servers := m.ConfiguredServers(config)
	if authenticated {
		return servers
	}
	for name, server := range servers {
		if strings.Contains(strings.ToLower(server.Command), "requires-auth") {
			delete(servers, name)
		}
	}
	return servers
}

func CodexAppsServerConfig(baseURL string, productSKU string) ServerConfig {
	env := map[string]string{}
	if baseURL != "" {
		env["CHATGPT_BASE_URL"] = baseURL
	}
	if productSKU != "" {
		env["APPS_MCP_PRODUCT_SKU"] = productSKU
	}
	return ServerConfig{
		Command: "codex-apps-mcp",
		Env:     env,
		Enabled: true,
	}
}

type Tool struct {
	ServerName         string
	Name               string
	Title              string
	ConnectorID        string
	ModelVisible       bool
	DestructiveHint    bool
	OpenWorldHint      bool
	PluginDisplayNames []string
}

type AppConnector struct {
	ID      string
	Enabled bool
}

type AppToolPolicy struct {
	DisabledTools map[string]bool
}

type ToolExposure struct {
	DirectTools   []Tool
	DeferredTools []Tool
}

func BuildToolExposure(all []Tool, connectors []AppConnector, policy *AppToolPolicy, searchToolEnabled bool) *ToolExposure {
	deferred := make([]Tool, 0, len(all))
	for _, tool := range all {
		if tool.ServerName != CodexAppsServerName && tool.ModelVisible {
			deferred = append(deferred, cloneTool(&tool))
		}
	}
	allowedConnectors := make(map[string]bool, len(connectors))
	for _, connector := range connectors {
		id := strings.TrimSpace(connector.ID)
		if connector.Enabled && id != "" {
			allowedConnectors[id] = true
		}
	}
	for _, tool := range all {
		if tool.ServerName != CodexAppsServerName || !tool.ModelVisible {
			continue
		}
		tool.ConnectorID = strings.TrimSpace(tool.ConnectorID)
		if !allowedConnectors[tool.ConnectorID] {
			continue
		}
		if policy != nil && policy.DisabledTools[tool.ConnectorID+"/"+tool.Name] {
			continue
		}
		deferred = append(deferred, cloneTool(&tool))
	}
	if !searchToolEnabled {
		return &ToolExposure{DirectTools: deferred}
	}
	if len(deferred) == 0 {
		return &ToolExposure{}
	}
	return &ToolExposure{DeferredTools: deferred}
}

type ApprovalTemplate struct {
	ConnectorID    string                  `json:"connector_id"`
	ServerName     string                  `json:"server_name"`
	ToolTitle      string                  `json:"tool_title"`
	Template       string                  `json:"template"`
	TemplateParams []ApprovalTemplateParam `json:"template_params"`
}

type ApprovalTemplateParam struct {
	Name  string `json:"name"`
	Label string `json:"label"`
}

type RenderedApprovalTemplate struct {
	Question           string
	ElicitationMessage string
	ToolParams         map[string]any
	ToolParamsDisplay  []RenderedApprovalParam
}

type RenderedApprovalParam struct {
	Name        string
	Value       any
	DisplayName string
}

func RenderApprovalTemplate(templates []ApprovalTemplate, serverName string, connectorID string, connectorName string, toolTitle string, toolParams map[string]any) (*RenderedApprovalTemplate, bool) {
	connectorID = strings.TrimSpace(connectorID)
	toolTitle = strings.TrimSpace(toolTitle)
	if connectorID == "" || toolTitle == "" {
		return nil, false
	}
	var matched *ApprovalTemplate
	for i := range templates {
		template := &templates[i]
		if template.ServerName == serverName && template.ConnectorID == connectorID && template.ToolTitle == toolTitle {
			matched = template
			break
		}
	}
	if matched == nil {
		return nil, false
	}
	message, ok := renderQuestionTemplate(matched.Template, connectorName)
	if !ok {
		return nil, false
	}
	display, ok := renderToolParams(toolParams, matched.TemplateParams)
	if !ok {
		return nil, false
	}
	return &RenderedApprovalTemplate{
		Question:           message,
		ElicitationMessage: message,
		ToolParams:         cloneAnyMap(toolParams),
		ToolParamsDisplay:  display,
	}, true
}

func LoadApprovalTemplatesJSON(data []byte) ([]ApprovalTemplate, error) {
	var file struct {
		SchemaVersion int                `json:"schema_version"`
		Templates     []ApprovalTemplate `json:"templates"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	if file.SchemaVersion != consequentialTemplateSchemaVersion {
		return nil, fmt.Errorf("unexpected approval template schema version %d", file.SchemaVersion)
	}
	return file.Templates, nil
}

func renderQuestionTemplate(template string, connectorName string) (string, bool) {
	template = strings.TrimSpace(template)
	if template == "" {
		return "", false
	}
	if strings.Contains(template, connectorNameTemplateVar) {
		connectorName = strings.TrimSpace(connectorName)
		if connectorName == "" {
			return "", false
		}
		return strings.ReplaceAll(template, connectorNameTemplateVar, connectorName), true
	}
	return template, true
}

func renderToolParams(toolParams map[string]any, templateParams []ApprovalTemplateParam) ([]RenderedApprovalParam, bool) {
	display := make([]RenderedApprovalParam, 0, len(toolParams))
	displayNames := make(map[string]bool, len(toolParams))
	handled := make(map[string]bool, len(templateParams))
	for _, templateParam := range templateParams {
		label := strings.TrimSpace(templateParam.Label)
		if label == "" {
			return nil, false
		}
		value, ok := toolParams[templateParam.Name]
		if !ok {
			continue
		}
		if displayNames[label] {
			return nil, false
		}
		displayNames[label] = true
		handled[templateParam.Name] = true
		display = append(display, RenderedApprovalParam{
			Name:        templateParam.Name,
			Value:       value,
			DisplayName: label,
		})
	}
	remaining := make([]string, 0, len(toolParams))
	for name := range toolParams {
		if !handled[name] {
			remaining = append(remaining, name)
		}
	}
	sort.Strings(remaining)
	for _, name := range remaining {
		if displayNames[name] {
			return nil, false
		}
		displayNames[name] = true
		display = append(display, RenderedApprovalParam{
			Name:        name,
			Value:       toolParams[name],
			DisplayName: name,
		})
	}
	return display, true
}

func cloneRegistrations(registrations map[string]ServerRegistration) map[string]ServerRegistration {
	out := make(map[string]ServerRegistration, len(registrations))
	for name, registration := range registrations {
		out[name] = cloneRegistration(&registration)
	}
	return out
}

func cloneRegistration(registration *ServerRegistration) ServerRegistration {
	if registration == nil {
		return ServerRegistration{}
	}
	cloned := *registration
	cloned.Config = cloneServerConfig(&registration.Config)
	return cloned
}

func cloneServerConfig(config *ServerConfig) ServerConfig {
	if config == nil {
		return ServerConfig{}
	}
	cloned := *config
	cloned.Args = append([]string(nil), config.Args...)
	cloned.EnvVars = append([]EnvVar(nil), config.EnvVars...)
	cloned.Scopes = append([]string(nil), config.Scopes...)
	if config.Env != nil {
		cloned.Env = make(map[string]string, len(config.Env))
		for key, value := range config.Env {
			cloned.Env[key] = value
		}
	}
	if config.HTTPHeaders != nil {
		cloned.HTTPHeaders = make(map[string]string, len(config.HTTPHeaders))
		for key, value := range config.HTTPHeaders {
			cloned.HTTPHeaders[key] = value
		}
	}
	if config.EnvHTTPHeaders != nil {
		cloned.EnvHTTPHeaders = make(map[string]string, len(config.EnvHTTPHeaders))
		for key, value := range config.EnvHTTPHeaders {
			cloned.EnvHTTPHeaders[key] = value
		}
	}
	return cloned
}

func cloneTool(tool *Tool) Tool {
	if tool == nil {
		return Tool{}
	}
	cloned := *tool
	cloned.PluginDisplayNames = append([]string(nil), tool.PluginDisplayNames...)
	return cloned
}
