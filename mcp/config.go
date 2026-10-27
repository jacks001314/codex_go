package mcp

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"codex_go/apps"
	managedconfig "codex_go/config"
	"codex_go/jsonschema"
	"codex_go/sandbox"
)

const (
	CodexAppsServerName           = "codex_apps"
	DefaultMCPServerEnvironmentID = "local"
	ServerAuthOAuth               = "oauth"
	ServerAuthChatGPT             = "chatgpt"
	// ServerAuthEMAAuth exchanges an enterprise IdP refresh token for
	// resource-specific authorization (Rust #44832). EMA connections are not
	// enabled in this version; the mode only drives config provenance.
	ServerAuthEMAAuth                  = "ema_auth"
	codexConnectorsTokenEnvVar         = "CODEX_CONNECTORS_TOKEN"
	legacyCodexAppsRegistrationID      = "legacy_codex_apps"
	consequentialTemplateSchemaVersion = 4
	connectorNameTemplateVar           = "{connector_name}"
)

// McpOAuthRefreshMode selects the owner of MCP OAuth refresh and credential
// persistence, pinned for the lifetime of a connection (Rust #42128).
type McpOAuthRefreshMode string

const (
	McpOAuthRefreshLegacy      McpOAuthRefreshMode = "legacy"
	McpOAuthRefreshCoordinated McpOAuthRefreshMode = "coordinated"
)

// MCPStartupReadiness controls what startup may use before exposing a server's
// tools to the model (Rust McpStartupReadiness).
type MCPStartupReadiness string

const (
	// MCPStartupReadinessConnection requires the live connection to complete.
	MCPStartupReadinessConnection MCPStartupReadiness = "connection"
	// MCPStartupReadinessCatalog accepts an eligible cached tool catalog while
	// the live connection starts. Tool execution still requires that connection.
	MCPStartupReadinessCatalog MCPStartupReadiness = "catalog"
)

// Effective returns the configured readiness, defaulting to connection.
func (m MCPStartupReadiness) Effective() MCPStartupReadiness {
	if m == MCPStartupReadinessCatalog {
		return MCPStartupReadinessCatalog
	}
	return MCPStartupReadinessConnection
}

// String returns the effective readiness value.
func (m MCPStartupReadiness) String() string {
	return string(m.Effective())
}

// Effective returns the configured refresh mode, defaulting to legacy when the
// field is unset (Rust McpOAuthRefreshMode::default is Legacy).
func (m McpOAuthRefreshMode) Effective() McpOAuthRefreshMode {
	if m == "" {
		return McpOAuthRefreshLegacy
	}
	return m
}

func (m McpOAuthRefreshMode) String() string {
	return string(m.Effective())
}

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
	HTTPHeadersHelper string            `json:"http_headers_helper,omitempty"`
	OAuthClientID     string            `json:"oauth_client_id,omitempty"`
	// OAuthClientSecret is the configured secret for a pre-registered
	// confidential OAuth client (Rust #47891). It is only valid with a client
	// ID and is used for token exchange/refresh instead of persisting it in the
	// token record.
	OAuthClientSecret string `json:"oauth_client_secret,omitempty"`
	// OAuthTokenEndpointAuthMethod is the registered EMA client authentication
	// method for a confidential MCP registration (Rust #56001). Empty means the
	// public-client `none` method.
	OAuthTokenEndpointAuthMethod string `json:"oauth_token_endpoint_auth_method,omitempty"`
	// ToolInputSchemaMaxBytes is the best-effort compaction threshold for this
	// server's tool input schemas (Rust #47936). Nil uses 5,000 bytes.
	ToolInputSchemaMaxBytes *int   `json:"tool_input_schema_max_bytes,omitempty"`
	OAuthCallbackPort       uint16 `json:"oauth_callback_port,omitempty"`
	OAuthCallbackURL        string `json:"oauth_callback_url,omitempty"`
	OAuthResource           string `json:"oauth_resource,omitempty"`
	// OAuthAuthorizationServerIssuer is the expected resource authorization
	// server issuer for EMA token exchange (Rust #44832); it is only valid
	// together with auth = "ema_auth".
	OAuthAuthorizationServerIssuer string              `json:"authorization_server_issuer,omitempty"`
	Scopes                         []string            `json:"scopes,omitempty"`
	ScopesConfigured               bool                `json:"-"`
	OAuthServerName                string              `json:"-"`
	OAuthRefreshMode               McpOAuthRefreshMode `json:"oauth_refresh_mode,omitempty"`
	Auth                           string              `json:"auth,omitempty"`
	CodexHome                      string              `json:"-"`
	Enabled                        bool                `json:"enabled"`
	DisabledReason                 string              `json:"disabled_reason,omitempty"`
	Required                       bool                `json:"required,omitempty"`
	// StartupReadiness selects whether required-server startup may accept an
	// eligible cached catalog before the live connection is available.
	StartupReadiness MCPStartupReadiness `json:"startup_readiness,omitempty"`
	// SupportsParallelToolCalls advertises every tool from this server as safe
	// for parallel execution (Rust McpServerConfig::supports_parallel_tool_calls).
	SupportsParallelToolCalls bool                              `json:"supports_parallel_tool_calls,omitempty"`
	EnabledTools              []string                          `json:"enabled_tools,omitempty"`
	DisabledTools             []string                          `json:"disabled_tools,omitempty"`
	OmitToolsFrom             []string                          `json:"omit_tools_from,omitempty"`
	DefaultToolsApprovalMode  *apps.AppToolApproval             `json:"default_tools_approval_mode,omitempty"`
	Tools                     map[string]ToolConfig             `json:"tools,omitempty"`
	EnvironmentID             string                            `json:"environment_id,omitempty"`
	StartupTimeout            time.Duration                     `json:"-"`
	ToolTimeout               time.Duration                     `json:"-"`
	CatalogItemLimit          int                               `json:"-"`
	ApplyHTTPRequest          func(*http.Request, []byte) error `json:"-"`
	// HTTPClient overrides the runtime's shared HTTP transport for this server.
	// Executor-discovered HTTP MCP servers use a route-bound client so their
	// requests originate from the selected executor.
	HTTPClient   HTTPDoer        `json:"-"`
	ProtocolMode MCPProtocolMode `json:"-"`
	// ProtocolModeOverride is set by extension/overlay contributions that
	// select an HTTP protocol mode for their own server (Rust #44571). When
	// non-nil it wins over the default and host-owned-Apps modes; nil preserves
	// the existing default.
	ProtocolModeOverride *MCPProtocolMode `json:"-"`
	// OAuthCredentialsStoreMode carries the process-wide
	// `mcp_oauth_credentials_store` value down to this server so OAuth
	// refreshes use the configured credential store (Rust
	// OAuthCredentialsStoreMode).
	OAuthCredentialsStoreMode OAuthCredentialsStoreMode `json:"-"`
	// RequiresReadOnlyTools mirrors Rust's `EffectiveMcpServer::
	// with_read_only_mcp_tools`: the runtime's read-only policy is applied to
	// each server, so its tool discovery and invocation request
	// `openai/readOnly` and never share an unrestricted connection or catalog
	// (Rust #46042).
	RequiresReadOnlyTools bool `json:"-"`
	// CredentialPolicy mirrors Rust #48143's effective-server credential policy:
	// whether this server may resolve environment-variable credentials from the
	// host. Host configuration leaves it unset (host fallback allowed);
	// executor-discovered declarations set ExecutorOnly so a reconnect cannot
	// promote them to host credentials. It is excluded from serialization and
	// from the connection identity's JSON, and added explicitly to every cache
	// key instead.
	CredentialPolicy CredentialPolicy `json:"-"`
	// EMAConfigured reports that a trusted enterprise profile admitted this
	// ema_auth server; EMACredentialsAvailable reports a stored account-scoped
	// IdP grant. EMAAuthExtension advertises the enterprise-managed
	// authorization MCP extension on the 2026-07-28 protocol.
	EMAConfigured           bool `json:"-"`
	EMACredentialsAvailable bool `json:"-"`
	EMAAuthExtension        bool `json:"-"`
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

// ToolInputSchemaCompactionBytes returns the server's configured best-effort
// input-schema compaction threshold, or Rust's 5,000-byte default.
func (c *ServerConfig) ToolInputSchemaCompactionBytes() int {
	if c != nil && c.ToolInputSchemaMaxBytes != nil && *c.ToolInputSchemaMaxBytes > 0 {
		return *c.ToolInputSchemaMaxBytes
	}
	return jsonschema.DefaultCompactToolSchemaBytes
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
	if config.EffectiveAuth() == ServerAuthEMAAuth {
		// Rust #55334: EMA is admitted only from a trusted enterprise profile
		// and always uses a host-owned streamable HTTP transport.
		if !config.EMAConfigured {
			return errors.New("enterprise MCP authorization requires an enabled, directly configured server and a trusted enterprise profile")
		}
		if strings.TrimSpace(config.OAuthClientSecret) != "" && strings.TrimSpace(config.OAuthClientID) == "" {
			return errors.New("oauth.client_secret requires oauth.client_id")
		}
		return config.ValidateEMAAuthTransport()
	}
	if strings.TrimSpace(config.OAuthClientSecret) != "" && strings.TrimSpace(config.OAuthClientID) == "" {
		return errors.New("oauth.client_secret requires oauth.client_id")
	}
	// Rust #56001: the registered EMA authentication method only applies to
	// enterprise-managed authorization.
	if strings.TrimSpace(config.OAuthTokenEndpointAuthMethod) != "" {
		return errors.New("oauth.token_endpoint_auth_method requires auth = \"ema_auth\"")
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

// ValidateEMAAuthTransport mirrors Rust McpServerConfig::validate_ema_auth_transport:
// EMA requires a host-owned streamable HTTP connection and never falls back to
// an unrelated bearer token or executor-owned credential (Rust #44832).
func (c *ServerConfig) ValidateEMAAuthTransport() error {
	if c == nil || !c.IsLocalEnvironment() {
		return errors.New("ema_auth requires a host-owned MCP connection")
	}
	if strings.TrimSpace(c.URL) == "" || strings.TrimSpace(c.Command) != "" {
		return errors.New("ema_auth requires streamable HTTP")
	}
	if strings.TrimSpace(c.BearerTokenEnvVar) != "" ||
		len(c.HTTPHeaders) != 0 ||
		len(c.EnvHTTPHeaders) != 0 ||
		strings.TrimSpace(c.HTTPHeadersHelper) != "" {
		return errors.New("ema_auth cannot be combined with alternate HTTP credentials")
	}
	// Rust #56001: a confidential MCP registration needs a non-empty secret and
	// a pinned authorization server issuer, and its authentication method
	// requires that secret.
	method := managedconfig.MCPEMATokenEndpointAuthMethod(strings.TrimSpace(c.OAuthTokenEndpointAuthMethod))
	if method != "" && !method.IsValid() {
		return fmt.Errorf("unknown value %q for oauth.token_endpoint_auth_method", c.OAuthTokenEndpointAuthMethod)
	}
	if c.OAuthClientSecret == "" {
		if method != "" {
			return errors.New("oauth.token_endpoint_auth_method requires oauth.client_secret")
		}
		return nil
	}
	if strings.TrimSpace(c.OAuthClientSecret) == "" {
		return errors.New("ema_auth oauth.client_secret must not be empty")
	}
	if strings.TrimSpace(c.OAuthAuthorizationServerIssuer) == "" {
		return errors.New("confidential ema_auth requires oauth.authorization_server_issuer")
	}
	return nil
}

type ToolConfig struct {
	ApprovalMode *apps.AppToolApproval `json:"approval_mode,omitempty"`

	// OutputTokenLimit is the token budget for this tool's output, applied
	// before the standard serialization allowance (Rust #41421). When multiple
	// sources configure a limit, the most restrictive (smallest) wins; it is
	// independent of the approval mode.
	OutputTokenLimit *int `json:"output_token_limit,omitempty"`
}

// RestrictOutputTokenLimit applies the stricter explicit output budget without
// changing the approval policy (Rust McpServerToolConfig::restrict_output_token_limit).
func (t *ToolConfig) RestrictOutputTokenLimit(limit *int) {
	if limit == nil || *limit <= 0 {
		return
	}
	if t.OutputTokenLimit == nil || *limit < *t.OutputTokenLimit {
		t.OutputTokenLimit = limit
	}
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
	PluginHostRoot    string
	SelectionOrder    int
	// CredentialPolicy is the registration's credential authority (Rust #48143):
	// host configuration leaves it unset, executor-discovered declarations set
	// CredentialPolicyExecutorOnly.
	CredentialPolicy CredentialPolicy
}

type RuntimeConfig struct {
	Servers              map[string]ServerRegistration
	AppsEnabled          bool
	ChatGPTBaseURL       string
	AppsMCPProductSKU    string
	ConnectorIDs         []string
	AvailableEnvironment []string
	// EnvironmentAuthority is the captured turn-environment authority for MCP
	// availability (Rust McpEnvironmentScope::Selected, #39335/#46335). It takes
	// precedence over AvailableEnvironment when set.
	EnvironmentAuthority *EnvironmentAuthority
	// SelectedEnvironments is the ordered executor selection captured for this
	// projection, preserving priority order and pending or failed entries (Rust
	// McpServerContributionContext::selected_environments, #51503). A nil value
	// means no selection was captured (threadless discovery); a non-nil empty
	// value means the thread explicitly selected no environments. Authority is
	// derived from this same snapshot via SelectedEnvironments.Authority().
	SelectedEnvironments *SelectedEnvironments
	CodexHome            string
	Auth                 *RuntimeAuth
	Requirements         *managedconfig.ConfigRequirements
	ProtocolMode         MCPProtocolMode
	// DiscoverLocalStdio mirrors Rust McpConfig::discover_local_stdio (feature
	// mcp_connection_sharing, disabled by default): eligible local stdio servers
	// are probed with `server/discover` before the legacy handshake (#57210).
	DiscoverLocalStdio bool
	// NetworkPolicyMetadata, when set, supplies call-scoped network policy
	// metadata to trusted ChatGPT MCP servers (Rust NetworkPolicyMetadataProvider,
	// #56278). Existing runtimes leave it unset.
	NetworkPolicyMetadata NetworkPolicyMetadataProvider
	// RefreshToolListOnNotifications mirrors Rust McpConfig::mcp_tool_list_changed
	// (feature mcp_tool_list_changed, disabled by default): an ordinary server
	// that reports a tools/list_changed notification is re-listed before its
	// catalog is used again (Rust #54656).
	RefreshToolListOnNotifications bool
	// HostOwnedAppsProtocolMode is the independent protocol mode for the
	// trusted, HTTP Codex Apps registration (Rust #44318/#44571).
	HostOwnedAppsProtocolMode MCPProtocolMode
	HTTPClient                HTTPDoer
	// CodexSelfExe is the host-supplied re-exec binary. It is advertised to local
	// stdio servers as the sandbox launcher only after a capability probe
	// (Rust McpConfig::codex_self_exe, #52182). An empty value uses this
	// process's own executable, mirroring Rust's `std::env::current_exe()`.
	CodexSelfExe string
	// CodexLinuxSandboxExe and UseLegacyLandlock are forwarded in the sandbox
	// state (Rust McpConfig fields of the same names).
	CodexLinuxSandboxExe string
	UseLegacyLandlock    bool
	// EnvironmentCWDs maps an environment id to its captured working directory
	// (Rust McpConfig::environment_cwds). The sandbox state reports the entry for
	// the server's environment; the default (local) environment falls back to
	// the server's configured cwd.
	EnvironmentCWDs map[string]string
	// EnvironmentUseMXC maps an environment id to its effective Windows sandbox
	// backend (Rust McpConfig::environment_use_mxc, #49781). A missing entry
	// reports false, so one environment's MXC preference never leaks into
	// another.
	EnvironmentUseMXC map[string]bool
	// PermissionProfile is the thread execution authority published with this
	// MCP runtime (Rust McpConfig.permission_profile).
	PermissionProfile *sandbox.PermissionProfile
	// ServerPermissionProfiles maps enabled servers to the exact attachment
	// authority published for them (Rust McpConfig.server_permission_profiles).
	// A server absent from this map has no published authority, so its calls
	// and elicitations are rejected (#40728).
	ServerPermissionProfiles map[string]*sandbox.PermissionProfile
	// OAuthCredentialsStoreMode is Rust's `mcp_oauth_credentials_store`
	// (auto by default) applied to MCP OAuth credential storage.
	OAuthCredentialsStoreMode OAuthCredentialsStoreMode
	// XAAEnabled reports `[features].use_xaa` for enterprise-managed MCP auth.
	XAAEnabled bool
	// EMAPolicyBinding is the enterprise policy revision this runtime was
	// resolved from. New enterprise MCP requests are held while the binding is
	// stale or suspended (Rust #55691).
	EMAPolicyBinding *managedconfig.CloudConfigBundleBinding
	// EMAState remembers rejected account grants for each enterprise MCP
	// registration across threads (Rust McpConfig::ema_state, #55859). It
	// retains only revision digests, never credentials. A nil value makes
	// rejection markers local to one enterprise manager.
	EMAState *EMAState
	// EnterpriseManagedAuth is the trusted `[mcp_enterprise_managed_auth]`
	// profile used to derive enterprise IdP credentials.
	EnterpriseManagedAuth *managedconfig.MCPEnterpriseManagedAuthConfig
	// RequiresReadOnlyMCPTools mirrors Rust McpConfig: when set, every server's
	// tool discovery and invocation requests server-side read-only filtering and
	// invocation checks, and read-only connections never share a connection or
	// tool catalog with unrestricted ones (Rust #46042). It is disabled by
	// default, exactly like Rust's core configuration.
	RequiresReadOnlyMCPTools bool
}

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type RuntimeAuth struct {
	UsesCodexBackend bool
	HTTPHeaders      map[string]string
	ApplyHTTPRequest func(*http.Request, []byte) error
	// ChatGPTUserID and AccountID identify the account owner for
	// enterprise-managed MCP authorization (Rust #55334).
	ChatGPTUserID string
	AccountID     string
}

func RuntimeConfigFromValues(values map[string]any, codexHome string) *RuntimeConfig {
	return RuntimeConfigFromValuesWithAuth(values, codexHome, nil)
}

func RuntimeConfigFromValuesWithAuth(values map[string]any, codexHome string, runtimeAuth *RuntimeAuth) *RuntimeConfig {
	return RuntimeConfigFromValuesWithAuthAndRequirements(values, codexHome, runtimeAuth, nil)
}

func RuntimeConfigFromValuesWithAuthAndRequirements(values map[string]any, codexHome string, runtimeAuth *RuntimeAuth, requirements *managedconfig.ConfigRequirements) *RuntimeConfig {
	return runtimeConfigFromValues(values, codexHome, runtimeAuth, requirements, nil)
}

// RuntimeConfigFromValuesWithAuthRequirementsAndBinding additionally carries the
// enterprise policy revision the values were resolved from, so new enterprise
// MCP requests are held while that revision is suspended or stale (Rust #55691
// `McpConfig` policy binding). A nil binding keeps the previous behavior.
func RuntimeConfigFromValuesWithAuthRequirementsAndBinding(values map[string]any, codexHome string, runtimeAuth *RuntimeAuth, requirements *managedconfig.ConfigRequirements, binding *managedconfig.CloudConfigBundleBinding) *RuntimeConfig {
	return runtimeConfigFromValues(values, codexHome, runtimeAuth, requirements, binding)
}

func runtimeConfigFromValues(values map[string]any, codexHome string, runtimeAuth *RuntimeAuth, requirements *managedconfig.ConfigRequirements, binding *managedconfig.CloudConfigBundleBinding) *RuntimeConfig {
	out := &RuntimeConfig{
		Servers:                   map[string]ServerRegistration{},
		AppsEnabled:               appsEnabledFromRuntimeConfigValues(values),
		ChatGPTBaseURL:            runtimeConfigStringAny(values, "chatgpt_base_url", "chatgptBaseUrl", "chatgptBaseURL"),
		AppsMCPProductSKU:         runtimeConfigStringAny(values, "apps_mcp_product_sku", "appsMcpProductSku", "appsMCPProductSKU"),
		CodexHome:                 strings.TrimSpace(codexHome),
		AvailableEnvironment:      runtimeConfigStringSliceAny(values, "available_environment", "availableEnvironment"),
		ConnectorIDs:              runtimeConfigStringSliceAny(values, "connector_ids", "connectorIds"),
		Auth:                      cloneRuntimeAuth(runtimeAuth),
		Requirements:              managedconfig.CloneConfigRequirements(requirements),
		ProtocolMode:              mcpProtocolModeFromValues(values),
		DiscoverLocalStdio:        mcpFeatureEnabled(values, "mcp_connection_sharing"),
		HostOwnedAppsProtocolMode: hostOwnedAppsProtocolModeFromValues(values),
		// Rust #54656 reads the notification refresh from the same feature set
		// as the MCP protocol modes.
		RefreshToolListOnNotifications: mcpFeatureEnabled(values, "mcp_tool_list_changed"),
		OAuthCredentialsStoreMode:      runtimeOAuthCredentialsStoreMode(values),
		EMAPolicyBinding:               binding,
	}
	out.XAAEnabled = mcpFeatureEnabled(values, managedconfig.XAAFeatureKey())
	if table, ok := values["mcp_enterprise_managed_auth"].(map[string]any); ok {
		if idp, ok := table["idp"].(map[string]any); ok {
			issuer, _ := idp["issuer"].(string)
			clientID, _ := idp["client_id"].(string)
			if strings.TrimSpace(issuer) != "" && strings.TrimSpace(clientID) != "" {
				out.EnterpriseManagedAuth = &managedconfig.MCPEnterpriseManagedAuthConfig{
					IDP: managedconfig.MCPServerIdPOAuthConfig{Issuer: strings.TrimSpace(issuer), ClientID: strings.TrimSpace(clientID)},
				}
			}
		}
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
		// Rust mcp_oauth_callback_port / mcp_oauth_callback_url provide global
		// defaults applied to every MCP server; per-server values win.
		if config.OAuthCallbackPort == 0 {
			if port := runtimeConfigUint16Any(values, "mcp_oauth_callback_port", "mcpOauthCallbackPort"); port > 0 {
				config.OAuthCallbackPort = port
			}
		}
		if config.OAuthCallbackURL == "" {
			config.OAuthCallbackURL = runtimeConfigStringAny(values, "mcp_oauth_callback_url", "mcpOauthCallbackUrl")
		}
		if config.OAuthCredentialsStoreMode == "" {
			config.OAuthCredentialsStoreMode = out.OAuthCredentialsStoreMode
		}
		if config.EffectiveAuth() == ServerAuthEMAAuth {
			config.EMAConfigured = out.XAAEnabled && out.EnterpriseManagedAuth != nil
			config.EMAAuthExtension = config.EMAConfigured
			modern := MCPProtocol20260728
			config.ProtocolModeOverride = &modern
		}
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
	PluginHostRoot    string
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
	// Rust #56266: an explicit disable must survive runtime overlays, which only
	// replace a registration's transport and source. A requirements denial also
	// has to outlive the registration it was applied to, unless a trusted
	// host-owned Apps registration replaces it.
	explicitlyDisabled := map[string]string{}
	requirementsDisabled := map[string]bool{}
	for name, registration := range servers {
		if reason, ok := explicitMCPDisabledReason(&registration); ok {
			explicitlyDisabled[name] = reason
		}
		if _, ok := requirementsMCPDisabledReason(&registration); ok {
			requirementsDisabled[name] = true
		}
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
			PluginHostRoot:    overlay.PluginHostRoot,
			SelectionOrder:    overlay.SelectionOrder,
		}
	}
	for name, reason := range explicitlyDisabled {
		registration, ok := servers[name]
		if !ok {
			continue
		}
		registration.Config.Enabled = false
		registration.Config.DisabledReason = reason
		servers[name] = registration
	}
	for name := range requirementsDisabled {
		registration, ok := servers[name]
		if !ok || isHostOwnedCodexAppsRegistration(name, registration) {
			continue
		}
		disableMCPRegistrationByRequirements(&registration)
		servers[name] = registration
	}
	out := base
	out.Servers = servers
	out.Requirements = managedconfig.CloneConfigRequirements(base.Requirements)
	normalizeMCPServerChatGPTAuth(out.Servers, out.ChatGPTBaseURL)
	applyManagedMCPRequirements(out.Servers, out.Requirements)
	return &out
}

// normalizeMCPServerChatGPTAuth mirrors Rust's effective_mcp_servers auth
// filter: hosted ChatGPT auth is retained only for the ChatGPT/Gov origins and
// the configured staging origin. Any other streamable HTTP server falls back to
// OAuth instead of receiving the host's ChatGPT credential.
func normalizeMCPServerChatGPTAuth(servers map[string]ServerRegistration, chatGPTBaseURL string) {
	for name, registration := range servers {
		if registration.Config.EffectiveAuth() != ServerAuthChatGPT ||
			strings.TrimSpace(registration.Config.URL) == "" ||
			strings.TrimSpace(registration.Config.Command) != "" ||
			trustedChatGPTMCPOrigin(registration.Config.URL, chatGPTBaseURL) {
			continue
		}
		registration.Config.Auth = ServerAuthOAuth
		servers[name] = registration
	}
}

func trustedChatGPTMCPOrigin(serverURL string, chatGPTBaseURL string) bool {
	server, ok := normalizedMCPServerOrigin(serverURL)
	if !ok {
		return false
	}
	for _, fixed := range []string{"https://chatgpt.com", "https://gov.chatgpt.com"} {
		trusted, trustedOK := normalizedMCPServerOrigin(fixed)
		if trustedOK && server == trusted {
			return true
		}
	}
	base, ok := normalizedMCPServerOrigin(chatGPTBaseURL)
	if !ok || base.scheme != "https" ||
		(base.host != "chatgpt-staging.com" && !strings.HasSuffix(base.host, ".chatgpt-staging.com")) {
		return false
	}
	return server == base
}

type mcpURLOrigin struct {
	scheme string
	host   string
	port   string
}

func normalizedMCPServerOrigin(raw string) (mcpURLOrigin, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return mcpURLOrigin{}, false
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return mcpURLOrigin{}, false
	}
	port := parsed.Port()
	if port == "" {
		if parsed.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return mcpURLOrigin{scheme: parsed.Scheme, host: host, port: port}, true
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

// disableMCPRegistrationByRequirements mirrors McpServerConfig::disable_by_requirements
// (Rust #56266): a requirements denial keeps an existing explicit disable
// visible in the reason, and repeated filtering keeps the combined reason
// instead of downgrading it.
func disableMCPRegistrationByRequirements(registration *ServerRegistration) {
	if registration == nil {
		return
	}
	disableServerConfigByRequirements(&registration.Config)
}

func disableServerConfigByRequirements(config *ServerConfig) {
	if config == nil {
		return
	}
	reason := managedconfig.MCPDisabledByRequirements
	switch strings.TrimSpace(config.DisabledReason) {
	case managedconfig.MCPDisabledByRequirements:
		// Already denied only by requirements.
	case managedconfig.MCPDisabledByExplicitAndRequirements:
		reason = managedconfig.MCPDisabledByExplicitAndRequirements
	default:
		// An empty or custom reason with a disabled registration is an explicit
		// disable, matching Rust's None/Unknown arms.
		if !config.Enabled {
			reason = managedconfig.MCPDisabledByExplicitAndRequirements
		}
	}
	config.Enabled = false
	config.DisabledReason = reason
}

// requirementsMCPDisabledReason reports whether a disabled registration was
// denied by managed requirements and returns its reason. Selected plugin
// registrations do not persist a name-level veto (Rust #56266).
func requirementsMCPDisabledReason(registration *ServerRegistration) (string, bool) {
	if registration == nil || registration.Config.Enabled {
		return "", false
	}
	reason := strings.TrimSpace(registration.Config.DisabledReason)
	if !managedconfig.MCPDisabledByManagedRequirements(reason) {
		return "", false
	}
	if !DisabledRegistrationIsNameVeto(SourceFromRegistration(registration)) {
		return "", false
	}
	return reason, true
}

// explicitMCPDisabledReason reports whether a registration was disabled without
// managed requirements and returns the reason to keep across overlays. An
// explicit disable stays effective when an overlay only replaces the transport
// and source (Rust #56266).
func explicitMCPDisabledReason(registration *ServerRegistration) (string, bool) {
	if registration == nil || registration.Config.Enabled {
		return "", false
	}
	reason := strings.TrimSpace(registration.Config.DisabledReason)
	if managedconfig.MCPDisabledByManagedRequirements(reason) {
		return reason, reason == managedconfig.MCPDisabledByExplicitAndRequirements
	}
	return registration.Config.DisabledReason, true
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
	server.StartupReadiness = MCPStartupReadiness(runtimeConfigStringAny(values, "startup_readiness", "startupReadiness"))
	server.DisabledReason = runtimeConfigStringAny(values, "disabled_reason", "disabledReason")
	server.Auth = runtimeConfigString(values, "auth")
	server.EnvironmentID = runtimeConfigStringAny(values, "environment_id", "environmentId")
	server.SupportsParallelToolCalls = runtimeConfigBoolAny(values, "supports_parallel_tool_calls", "supportsParallelToolCalls")
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
		server.OAuthClientSecret = runtimeConfigOAuthClientSecret(values)
		server.OAuthTokenEndpointAuthMethod = runtimeConfigOAuthTokenEndpointAuthMethod(values)
		server.ToolInputSchemaMaxBytes = runtimeConfigPositiveIntAny(values, "tool_input_schema_max_bytes", "toolInputSchemaMaxBytes")
		server.OAuthCallbackPort = runtimeConfigOAuthCallbackPort(values)
		server.OAuthCallbackURL = runtimeConfigStringAny(values, "oauth_callback_url", "oauthCallbackUrl")
		server.OAuthResource = runtimeConfigStringAny(values, "oauth_resource", "oauthResource")
		server.OAuthAuthorizationServerIssuer = runtimeConfigOAuthAuthorizationServerIssuer(values)
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

// runtimeConfigBoolAny returns the first boolean value among the keys (Rust
// config values are typed, so a mistyped entry is ignored rather than coerced).
func runtimeConfigBoolAny(values map[string]any, keys ...string) bool {
	for _, key := range keys {
		if value, ok := values[key].(bool); ok {
			return value
		}
	}
	return false
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

// runtimeConfigOAuthClientSecret keeps the configured value untrimmed so a
// whitespace-only secret is still detectable and rejected (Rust #56001).
func runtimeConfigOAuthClientSecret(values map[string]any) string {
	if value := runtimeConfigRawStringAny(values, "oauth_client_secret", "oauthClientSecret"); value != "" {
		return value
	}
	oauth, ok := runtimeConfigMapAny(values, "oauth")
	if !ok {
		return ""
	}
	return runtimeConfigRawStringAny(oauth, "client_secret", "clientSecret")
}

// runtimeConfigRawStringAny returns the first non-empty string value without
// trimming it.
func runtimeConfigRawStringAny(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key].(string); ok && value != "" {
			return value
		}
	}
	return ""
}

// runtimeConfigOAuthTokenEndpointAuthMethod resolves the registered EMA client
// authentication method from either the flat or nested oauth form.
func runtimeConfigOAuthTokenEndpointAuthMethod(values map[string]any) string {
	if value := runtimeConfigStringAny(values, "oauth_token_endpoint_auth_method", "oauthTokenEndpointAuthMethod", "token_endpoint_auth_method", "tokenEndpointAuthMethod"); value != "" {
		return value
	}
	oauth, ok := runtimeConfigMapAny(values, "oauth")
	if !ok {
		return ""
	}
	return runtimeConfigStringAny(oauth, "token_endpoint_auth_method", "tokenEndpointAuthMethod")
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

// runtimeConfigOAuthAuthorizationServerIssuer resolves the EMA resource
// authorization server issuer from either the flat or nested oauth form.
func runtimeConfigOAuthAuthorizationServerIssuer(values map[string]any) string {
	if value := runtimeConfigStringAny(values, "oauth_authorization_server_issuer", "oauthAuthorizationServerIssuer"); value != "" {
		return value
	}
	oauth, ok := runtimeConfigMapAny(values, "oauth")
	if !ok {
		return ""
	}
	return runtimeConfigStringAny(oauth, "authorization_server_issuer", "authorizationServerIssuer")
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
		case uint16:
			if typed > 0 {
				return typed
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

// runtimeConfigPositiveIntAny returns the first positive integer value among the
// candidate keys (e.g. output_token_limit / outputTokenLimit), or nil when unset
// or non-positive.
func runtimeConfigPositiveIntAny(values map[string]any, keys ...string) *int {
	if values == nil {
		return nil
	}
	for _, key := range keys {
		value, ok := values[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case int:
			if typed > 0 {
				out := typed
				return &out
			}
		case int64:
			if typed > 0 && typed <= int64(maxInt()) {
				out := int(typed)
				return &out
			}
		case float64:
			if typed > 0 && typed <= float64(maxInt()) {
				out := int(typed)
				return &out
			}
		case json.Number:
			if n, err := typed.Int64(); err == nil && n > 0 && n <= int64(maxInt()) {
				out := int(n)
				return &out
			}
		}
	}
	return nil
}

func maxInt() int {
	return int(^uint(0) >> 1)
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
			ApprovalMode:     runtimeConfigAppToolApproval(toolTable, "approval_mode", "approvalMode"),
			OutputTokenLimit: runtimeConfigPositiveIntAny(toolTable, "output_token_limit", "outputTokenLimit"),
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

// codexAppsMCPURL mirrors Rust's `codex_apps_mcp_url_for_base_url`
// (codex-mcp/src/mcp/mod.rs:609-628 at pin 4eb4a65407, #27198): the base URL
// loses its trailing slashes, a ChatGPT host without a backend path gains
// `/backend-api`, and the endpoint is plugin-service's `/ps/mcp` — appended
// directly when the base already carries `/backend-api` or `/api/codex`, and
// after `/api/codex` otherwise; the retired Apps path it replaced is gone.
func codexAppsMCPURL(baseURL string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		// Rust substitutes its default one layer up, before the helper runs
		// (`codex_apps_mcp_server_config` receives an already-defaulted
		// `mcp_config.chatgpt_base_url`, core/src/config/mod.rs:4578-4580), so an
		// empty base yields the same effective URL here.
		baseURL = strings.TrimRight(managedconfig.DefaultChatGPTBaseURL, "/")
	}
	if (strings.HasPrefix(baseURL, "https://chatgpt.com") || strings.HasPrefix(baseURL, "https://chat.openai.com")) && !strings.Contains(baseURL, "/backend-api") {
		baseURL += "/backend-api"
	}
	if !strings.Contains(baseURL, "/backend-api") && !strings.Contains(baseURL, "/api/codex") {
		baseURL += "/api/codex"
	}
	return baseURL + "/ps/mcp"
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

// cloneBoolMap copies an environment-indexed boolean map. A nil input stays nil
// so an absent environment selection is distinguishable from an explicit false.
func cloneBoolMap(values map[string]bool) map[string]bool {
	if values == nil {
		return nil
	}
	out := make(map[string]bool, len(values))
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
	if config.ProtocolModeOverride != nil {
		override := *config.ProtocolModeOverride
		cloned.ProtocolModeOverride = &override
	}
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
