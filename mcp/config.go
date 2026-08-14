package mcp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"codex_go/apps"
	managedconfig "codex_go/config"
)

const (
	CodexAppsServerName                = "codex_apps"
	DefaultMCPServerEnvironmentID      = "local"
	ServerAuthOAuth                    = "oauth"
	ServerAuthChatGPT                  = "chatgpt"
	codexConnectorsTokenEnvVar         = "CODEX_CONNECTORS_TOKEN"
	legacyCodexAppsRegistrationID      = "legacy_codex_apps"
	consequentialTemplateSchemaVersion = 4
	connectorNameTemplateVar           = "{connector_name}"
)

type ServerConfig struct {
	Command                  string                            `json:"command,omitempty"`
	Args                     []string                          `json:"args,omitempty"`
	Env                      map[string]string                 `json:"env,omitempty"`
	EnvVars                  []EnvVar                          `json:"env_vars,omitempty"`
	CWD                      string                            `json:"cwd,omitempty"`
	URL                      string                            `json:"url,omitempty"`
	BearerTokenEnvVar        string                            `json:"bearer_token_env_var,omitempty"`
	HTTPHeaders              map[string]string                 `json:"http_headers,omitempty"`
	EnvHTTPHeaders           map[string]string                 `json:"env_http_headers,omitempty"`
	HTTPHeadersHelper        string                            `json:"http_headers_helper,omitempty"`
	OAuthClientID            string                            `json:"oauth_client_id,omitempty"`
	OAuthCallbackPort        uint16                            `json:"oauth_callback_port,omitempty"`
	OAuthResource            string                            `json:"oauth_resource,omitempty"`
	Scopes                   []string                          `json:"scopes,omitempty"`
	ScopesConfigured         bool                              `json:"-"`
	OAuthServerName          string                            `json:"-"`
	Auth                     string                            `json:"auth,omitempty"`
	CodexHome                string                            `json:"-"`
	Enabled                  bool                              `json:"enabled"`
	DisabledReason           string                            `json:"disabled_reason,omitempty"`
	Required                 bool                              `json:"required,omitempty"`
	EnabledTools             []string                          `json:"enabled_tools,omitempty"`
	DisabledTools            []string                          `json:"disabled_tools,omitempty"`
	OmitToolsFrom            []string                          `json:"omit_tools_from,omitempty"`
	DefaultToolsApprovalMode *apps.AppToolApproval             `json:"default_tools_approval_mode,omitempty"`
	Tools                    map[string]ToolConfig             `json:"tools,omitempty"`
	EnvironmentID            string                            `json:"environment_id,omitempty"`
	StartupTimeout           time.Duration                     `json:"-"`
	ToolTimeout              time.Duration                     `json:"-"`
	CatalogItemLimit         int                               `json:"-"`
	ApplyHTTPRequest         func(*http.Request, []byte) error `json:"-"`
	ProtocolMode             MCPProtocolMode                   `json:"-"`
}

func (c *ServerConfig) EffectiveEnvironmentID() string {
	if c == nil || strings.TrimSpace(c.EnvironmentID) == "" {
		return DefaultMCPServerEnvironmentID
	}
	return strings.TrimSpace(c.EnvironmentID)
}

func (c *ServerConfig) IsLocalEnvironment() bool {
	return c != nil && c.EffectiveEnvironmentID() == DefaultMCPServerEnvironmentID
}

func (c *ServerConfig) EffectiveAuth() string {
	if c == nil || strings.TrimSpace(c.Auth) == "" {
		return ServerAuthOAuth
	}
	return strings.ToLower(strings.TrimSpace(c.Auth))
}

// OAuthCredentialName keeps legacy local names stable while isolating
// executor-owned credentials from both the host and other environments.
func (c *ServerConfig) OAuthCredentialName(serverName string) string {
	serverName = strings.TrimSpace(serverName)
	if c == nil || c.IsLocalEnvironment() {
		if strings.HasPrefix(serverName, "executor:") || strings.HasPrefix(serverName, "local:") {
			return "local:" + serverName
		}
		return serverName
	}
	environment := base64.RawURLEncoding.EncodeToString([]byte(c.EffectiveEnvironmentID()))
	server := base64.RawURLEncoding.EncodeToString([]byte(serverName))
	return "executor:" + environment + ":" + server
}

func (c *ServerConfig) SafeRemoteChatGPTAuthorization() bool {
	if c == nil || c.EffectiveAuth() != ServerAuthChatGPT || c.IsLocalEnvironment() || c.ApplyHTTPRequest != nil || strings.TrimSpace(c.BearerTokenEnvVar) != "" {
		return false
	}
	if len(c.EnvHTTPHeaders) != 0 {
		return false
	}
	for name, value := range c.HTTPHeaders {
		if !strings.EqualFold(strings.TrimSpace(name), "Authorization") || strings.TrimSpace(value) == "" {
			continue
		}
		for _, character := range []byte(value) {
			if character != '\t' && (character < ' ' || character == 0x7f) {
				return false
			}
		}
		return true
	}
	return false
}

func ValidateServerAuth(serverName string, config *ServerConfig) error {
	if config == nil {
		return nil
	}
	if strings.TrimSpace(config.HTTPHeadersHelper) != "" {
		if strings.TrimSpace(config.URL) == "" || strings.TrimSpace(config.Command) != "" {
			return fmt.Errorf("http_headers_helper is only supported for streamable HTTP MCP servers")
		}
		if !config.IsLocalEnvironment() {
			return fmt.Errorf("http_headers_helper is only supported for local MCP servers")
		}
	}
	if config.EffectiveAuth() != ServerAuthChatGPT || config.IsLocalEnvironment() {
		return nil
	}
	if config.SafeRemoteChatGPTAuthorization() {
		return nil
	}
	return fmt.Errorf("executor-owned MCP server `%s` cannot use hosted ChatGPT authentication; configure executor-owned credentials instead", strings.TrimSpace(serverName))
}

type ToolConfig struct {
	ApprovalMode *apps.AppToolApproval `json:"approval_mode,omitempty"`
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
	Auth                 *RuntimeAuth
	Requirements         *managedconfig.ConfigRequirements
	ProtocolMode         MCPProtocolMode
	HTTPClient           HTTPDoer
}

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type RuntimeAuth struct {
	UsesCodexBackend bool
	HTTPHeaders      map[string]string
	ApplyHTTPRequest func(*http.Request, []byte) error
}

func RuntimeConfigFromValues(values map[string]any, codexHome string) *RuntimeConfig {
	return RuntimeConfigFromValuesWithAuth(values, codexHome, nil)
}

func RuntimeConfigFromValuesWithAuth(values map[string]any, codexHome string, runtimeAuth *RuntimeAuth) *RuntimeConfig {
	return RuntimeConfigFromValuesWithAuthAndRequirements(values, codexHome, runtimeAuth, nil)
}

func RuntimeConfigFromValuesWithAuthAndRequirements(values map[string]any, codexHome string, runtimeAuth *RuntimeAuth, requirements *managedconfig.ConfigRequirements) *RuntimeConfig {
	out := &RuntimeConfig{
		Servers:              map[string]ServerRegistration{},
		AppsEnabled:          appsEnabledFromRuntimeConfigValues(values),
		ChatGPTBaseURL:       runtimeConfigStringAny(values, "chatgpt_base_url", "chatgptBaseUrl", "chatgptBaseURL"),
		AppsMCPProductSKU:    runtimeConfigStringAny(values, "apps_mcp_product_sku", "appsMcpProductSku", "appsMCPProductSKU"),
		CodexHome:            strings.TrimSpace(codexHome),
		AvailableEnvironment: runtimeConfigStringSliceAny(values, "available_environment", "availableEnvironment"),
		ConnectorIDs:         runtimeConfigStringSliceAny(values, "connector_ids", "connectorIds"),
		Auth:                 cloneRuntimeAuth(runtimeAuth),
		Requirements:         managedconfig.CloneConfigRequirements(requirements),
		ProtocolMode:         mcpProtocolModeFromValues(values),
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
	Source            CatalogSource
	PluginID          string
	PluginDisplayName string
	SelectionOrder    int
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
	if base.AppsEnabled && base.Auth != nil && base.Auth.UsesCodexBackend {
		servers[CodexAppsServerName] = ServerRegistration{
			Name:          CodexAppsServerName,
			Config:        CodexAppsServerConfig(base.ChatGPTBaseURL, base.AppsMCPProductSKU, base.Auth),
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
		source := overlay.Source
		if source == "" {
			source = CatalogSourceExtension
		}
		if current, ok := servers[overlay.Name]; ok && CatalogSourcePriority(SourceFromRegistration(&current)) > CatalogSourcePriority(source) {
			continue
		}
		if overlay.Remove {
			delete(servers, overlay.Name)
			continue
		}
		servers[overlay.Name] = ServerRegistration{
			Name:              overlay.Name,
			Config:            cloneServerConfig(&overlay.Config),
			Source:            string(source),
			ContributorID:     overlay.ContributorID,
			ContributionOrder: overlay.ContributionOrder,
			PluginID:          overlay.PluginID,
			PluginDisplayName: overlay.PluginDisplayName,
			SelectionOrder:    overlay.SelectionOrder,
		}
	}
	out := base
	out.Servers = servers
	out.Requirements = managedconfig.CloneConfigRequirements(base.Requirements)
	applyManagedMCPRequirements(out.Servers, out.Requirements)
	return &out
}

func applyManagedMCPRequirements(servers map[string]ServerRegistration, requirements *managedconfig.ConfigRequirements) {
	if len(servers) == 0 || requirements == nil {
		return
	}
	pluginFilteringEnabled := false
	for _, requirement := range requirements.Plugins {
		if requirement.MCPServers != nil {
			pluginFilteringEnabled = true
			break
		}
	}
	emptyGlobalAllowlist := requirements.MCPServers != nil && len(requirements.MCPServers) == 0
	for name, registration := range servers {
		source := SourceFromRegistration(&registration)
		switch source {
		case CatalogSourcePlugin, CatalogSourceSelectedPlugin:
			if pluginFilteringEnabled {
				pluginRequirement, pluginAllowed := requirements.Plugins[strings.TrimSpace(registration.PluginID)]
				var allowlist map[string]managedconfig.MCPServerRequirement
				if pluginAllowed && pluginRequirement.MCPServers != nil {
					allowlist = *pluginRequirement.MCPServers
				}
				requirement, allowed := allowlist[name]
				if !allowed || !managedMCPRequirementMatches(requirement, &registration.Config) {
					disableMCPRegistrationByRequirements(&registration)
				}
			}
			if emptyGlobalAllowlist {
				disableMCPRegistrationByRequirements(&registration)
			}
		case CatalogSourceConfig:
			if requirements.MCPServers != nil {
				requirement, allowed := requirements.MCPServers[name]
				if !allowed || !managedMCPRequirementMatches(requirement, &registration.Config) {
					disableMCPRegistrationByRequirements(&registration)
				}
			}
		}
		servers[name] = registration
	}
}

func managedMCPRequirementMatches(requirement managedconfig.MCPServerRequirement, server *ServerConfig) bool {
	if server == nil {
		return false
	}
	return requirement.Matches(strings.TrimSpace(server.Command), server.Args, strings.TrimSpace(server.URL))
}

func disableMCPRegistrationByRequirements(registration *ServerRegistration) {
	if registration == nil {
		return
	}
	registration.Config.Enabled = false
	registration.Config.DisabledReason = managedconfig.MCPDisabledByRequirements
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
	server.Auth = runtimeConfigString(values, "auth")
	server.EnvironmentID = runtimeConfigStringAny(values, "environment_id", "environmentId")
	server.StartupTimeout = runtimeConfigDurationAny(values, "startup_timeout_sec", "startupTimeoutSec", "startup_timeout_ms", "startupTimeoutMs")
	server.ToolTimeout = runtimeConfigDurationAny(values, "tool_timeout_sec", "toolTimeoutSec", "tool_timeout_ms", "toolTimeoutMs")
	server.EnabledTools = runtimeConfigStringSliceAny(values, "enabled_tools", "enabledTools")
	server.DisabledTools = runtimeConfigStringSliceAny(values, "disabled_tools", "disabledTools")
	server.OmitToolsFrom = runtimeConfigStringSliceAny(values, "omit_tools_from", "omitToolsFrom")
	server.DefaultToolsApprovalMode = runtimeConfigAppToolApproval(values, "default_tools_approval_mode", "defaultToolsApprovalMode")
	server.Tools = runtimeConfigToolsMap(values, "tools")
	if url := runtimeConfigString(values, "url"); url != "" {
		server.URL = url
		server.BearerTokenEnvVar = runtimeConfigStringAny(values, "bearer_token_env_var", "bearerTokenEnvVar")
		server.HTTPHeaders = runtimeConfigStringMap(values, "http_headers")
		server.EnvHTTPHeaders = runtimeConfigStringMap(values, "env_http_headers")
		server.HTTPHeadersHelper = runtimeConfigString(values, "http_headers_helper")
		server.OAuthClientID = runtimeConfigOAuthClientID(values)
		server.OAuthCallbackPort = runtimeConfigOAuthCallbackPort(values)
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

func ServerConfigFromValues(values map[string]any) *ServerConfig {
	return runtimeServerConfigFromValues(values)
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

func runtimeConfigOAuthCallbackPort(values map[string]any) uint16 {
	if port := runtimeConfigUint16Any(values, "oauth_callback_port", "oauthCallbackPort"); port > 0 {
		return port
	}
	oauth, ok := runtimeConfigMapAny(values, "oauth")
	if !ok {
		return 0
	}
	return runtimeConfigUint16Any(oauth, "callback_port", "callbackPort", "port")
}

func runtimeConfigUint16Any(values map[string]any, keys ...string) uint16 {
	if values == nil {
		return 0
	}
	for _, key := range keys {
		value, ok := values[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case float64:
			if typed > 0 && typed <= 65535 {
				return uint16(typed)
			}
		case float32:
			if typed > 0 && typed <= 65535 {
				return uint16(typed)
			}
		case int:
			if typed > 0 && typed <= 65535 {
				return uint16(typed)
			}
		case int64:
			if typed > 0 && typed <= 65535 {
				return uint16(typed)
			}
		case uint64:
			if typed > 0 && typed <= 65535 {
				return uint16(typed)
			}
		case json.Number:
			parsed, err := typed.Float64()
			if err == nil && parsed > 0 && parsed <= 65535 {
				return uint16(parsed)
			}
		}
	}
	return 0
}

func runtimeConfigStringSliceAny(values map[string]any, keys ...string) []string {
	for _, key := range keys {
		if values := runtimeConfigStringSlice(values, key); len(values) > 0 {
			return values
		}
	}
	return nil
}

func runtimeConfigDurationAny(values map[string]any, keys ...string) time.Duration {
	for _, key := range keys {
		duration := runtimeConfigDuration(values, key)
		if duration > 0 {
			return duration
		}
	}
	return 0
}

func runtimeConfigDuration(values map[string]any, key string) time.Duration {
	if values == nil {
		return 0
	}
	value, ok := values[key]
	if !ok || value == nil {
		return 0
	}
	var seconds float64
	switch typed := value.(type) {
	case float64:
		seconds = typed
	case float32:
		seconds = float64(typed)
	case int:
		seconds = float64(typed)
	case int64:
		seconds = float64(typed)
	case uint64:
		seconds = float64(typed)
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0
		}
		seconds = parsed
	default:
		return 0
	}
	if seconds <= 0 {
		return 0
	}
	if strings.HasSuffix(strings.ToLower(strings.TrimSpace(key)), "_ms") || strings.HasSuffix(strings.ToLower(strings.TrimSpace(key)), "ms") {
		return time.Duration(seconds * float64(time.Millisecond))
	}
	return time.Duration(seconds * float64(time.Second))
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

func runtimeConfigAppToolApproval(values map[string]any, keys ...string) *apps.AppToolApproval {
	for _, key := range keys {
		if values == nil {
			continue
		}
		value, ok := values[key]
		if !ok {
			continue
		}
		text, ok := value.(string)
		if !ok {
			continue
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		var approval apps.AppToolApproval
		switch text {
		case "auto":
			approval = apps.AppToolApprovalAuto
		case "prompt":
			approval = apps.AppToolApprovalPrompt
		case "writes":
			approval = apps.AppToolApprovalWrites
		case "approve":
			approval = apps.AppToolApprovalApprove
		default:
			continue
		}
		return &approval
	}
	return nil
}

func runtimeConfigToolsMap(values map[string]any, key string) map[string]ToolConfig {
	if values == nil {
		return nil
	}
	raw, ok := values[key]
	if !ok {
		return nil
	}
	table, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]ToolConfig, len(table))
	for toolName, toolRaw := range table {
		toolName = strings.TrimSpace(toolName)
		if toolName == "" {
			continue
		}
		toolTable, ok := toolRaw.(map[string]any)
		if !ok {
			continue
		}
		toolConfig := ToolConfig{
			ApprovalMode: runtimeConfigAppToolApproval(toolTable, "approval_mode", "approvalMode"),
		}
		out[toolName] = toolConfig
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

func CodexAppsServerConfig(baseURL string, productSKU string, runtimeAuth *RuntimeAuth) ServerConfig {
	headers := cloneStringMap(runtimeAuthHeaders(runtimeAuth))
	if strings.TrimSpace(productSKU) != "" {
		if headers == nil {
			headers = map[string]string{}
		}
		headers["X-OpenAI-Product-Sku"] = strings.TrimSpace(productSKU)
	}
	config := ServerConfig{
		URL:              codexAppsMCPURL(baseURL),
		Auth:             ServerAuthChatGPT,
		EnvironmentID:    DefaultMCPServerEnvironmentID,
		HTTPHeaders:      headers,
		Enabled:          true,
		StartupTimeout:   30 * time.Second,
		CatalogItemLimit: maxCodexAppsCatalogItems,
		ApplyHTTPRequest: runtimeAuthRequestApplier(runtimeAuth),
	}
	if strings.TrimSpace(os.Getenv(codexConnectorsTokenEnvVar)) != "" {
		config.BearerTokenEnvVar = codexConnectorsTokenEnvVar
		config.ApplyHTTPRequest = nil
	}
	return config
}

func codexAppsMCPURL(baseURL string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = "https://chatgpt.com/backend-api"
	}
	if (strings.HasPrefix(baseURL, "https://chatgpt.com") || strings.HasPrefix(baseURL, "https://chat.openai.com")) && !strings.Contains(baseURL, "/backend-api") {
		baseURL += "/backend-api"
	}
	switch {
	case strings.Contains(baseURL, "/backend-api"):
		return baseURL + "/wham/apps"
	case strings.Contains(baseURL, "/api/codex"):
		return baseURL + "/apps"
	default:
		return baseURL + "/api/codex/apps"
	}
}

func cloneRuntimeAuth(runtimeAuth *RuntimeAuth) *RuntimeAuth {
	if runtimeAuth == nil {
		return nil
	}
	cloned := *runtimeAuth
	cloned.HTTPHeaders = cloneStringMap(runtimeAuth.HTTPHeaders)
	return &cloned
}

func runtimeAuthHeaders(runtimeAuth *RuntimeAuth) map[string]string {
	if runtimeAuth == nil {
		return nil
	}
	return runtimeAuth.HTTPHeaders
}

func runtimeAuthRequestApplier(runtimeAuth *RuntimeAuth) func(*http.Request, []byte) error {
	if runtimeAuth == nil {
		return nil
	}
	return runtimeAuth.ApplyHTTPRequest
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func RuntimeAuthUsesCodexBackend(mode string) bool {
	switch strings.TrimSpace(mode) {
	case "chatgpt", "chatgptAuthTokens", "agent-identity", "personal-access-token":
		return true
	default:
		return false
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
	cloned.OmitToolsFrom = append([]string(nil), config.OmitToolsFrom...)
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
	if config.Tools != nil {
		cloned.Tools = make(map[string]ToolConfig, len(config.Tools))
		for key, value := range config.Tools {
			cloned.Tools[key] = value
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
