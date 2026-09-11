package config

// Rust parity: codex-rs/config/src/mcp_ema.rs (#44832).
//
// Enterprise MCP authorization (EMA) must remain controlled by host, user, or
// managed configuration. Project settings and plugin declarations may not
// redirect the enterprise credential source or downgrade the selected
// authentication mode, and the disabled-by-default use_xaa feature must be
// opted in from a non-project layer (or a managed requirement).

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	featureflags "codex_go/features"
)

// MCPServerAuthEMAAuth is the MCP server auth mode that exchanges an enterprise
// IdP refresh token for resource-specific authorization.
const MCPServerAuthEMAAuth = "ema_auth"

// MCPServerIdPOAuthConfig is the trusted enterprise OAuth identity provider.
type MCPServerIdPOAuthConfig struct {
	// Issuer used for enterprise OAuth discovery and identity validation.
	Issuer string `json:"issuer"`
	// ClientID is the public OAuth client registered with that enterprise IdP.
	ClientID string `json:"client_id"`
}

// MCPEnterpriseManagedAuthConfig is the shared enterprise authorization,
// independent of Codex account credentials.
type MCPEnterpriseManagedAuthConfig struct {
	IDP MCPServerIdPOAuthConfig `json:"idp"`
}

// PluginMCPServerEMAAuthConfig is a resource registration applied through an
// existing per-plugin policy overlay. The enterprise IdP is selected separately
// by trusted host configuration.
type PluginMCPServerEMAAuthConfig struct {
	// URL is the exact plugin endpoint approved by the host; it never overrides
	// the plugin's own declaration.
	URL                       string   `json:"url"`
	ClientID                  string   `json:"client_id"`
	AuthorizationServerIssuer string   `json:"authorization_server_issuer"`
	Scopes                    []string `json:"scopes"`
	Resource                  string   `json:"resource"`
}

func cloneMCPEnterpriseManagedAuth(config *MCPEnterpriseManagedAuthConfig) *MCPEnterpriseManagedAuthConfig {
	if config == nil {
		return nil
	}
	cloned := *config
	return &cloned
}

func mcpEMAString(value any) string {
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

func mcpEMABool(value any) (bool, bool) {
	boolean, ok := value.(bool)
	return boolean, ok
}

func mcpEMATable(value any) (map[string]any, bool) {
	table, ok := value.(map[string]any)
	return table, ok
}

func mcpEMAStringSlice(value any) []string {
	switch typed := value.(type) {
	case []any:
		out := make([]string, 0, len(typed))
		for _, entry := range typed {
			text, ok := entry.(string)
			if !ok {
				continue
			}
			out = append(out, strings.TrimSpace(text))
		}
		return out
	case []string:
		out := make([]string, 0, len(typed))
		for _, entry := range typed {
			out = append(out, strings.TrimSpace(entry))
		}
		return out
	default:
		return nil
	}
}

func mcpEMASortedKeys(table map[string]any) []string {
	keys := make([]string, 0, len(table))
	for key := range table {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// parseMCPEnterpriseManagedAuth parses a strict `mcp_enterprise_managed_auth`
// table (serde deny_unknown_fields, required nested idp issuer/client_id).
func parseMCPEnterpriseManagedAuth(section any) (*MCPEnterpriseManagedAuthConfig, error) {
	table, ok := mcpEMATable(section)
	if !ok {
		return nil, errors.New("mcp_enterprise_managed_auth must be a table")
	}
	for _, key := range mcpEMASortedKeys(table) {
		if key != "idp" {
			return nil, fmt.Errorf("unknown field `mcp_enterprise_managed_auth.%s`", key)
		}
	}
	rawIDP, ok := table["idp"]
	if !ok {
		return nil, errors.New("missing field `idp`")
	}
	idp, ok := mcpEMATable(rawIDP)
	if !ok {
		return nil, errors.New("field `idp` must be a table")
	}
	for _, key := range mcpEMASortedKeys(idp) {
		if key != "issuer" && key != "client_id" {
			return nil, fmt.Errorf("unknown field `mcp_enterprise_managed_auth.idp.%s`", key)
		}
	}
	issuer := mcpEMAString(idp["issuer"])
	clientID := mcpEMAString(idp["client_id"])
	if issuer == "" {
		return nil, errors.New("missing field `issuer`")
	}
	if clientID == "" {
		return nil, errors.New("missing field `client_id`")
	}
	return &MCPEnterpriseManagedAuthConfig{
		IDP: MCPServerIdPOAuthConfig{Issuer: issuer, ClientID: clientID},
	}, nil
}

// parsePluginMCPServerEMAAuth parses a strict plugin EMA registration.
func parsePluginMCPServerEMAAuth(section any) (*PluginMCPServerEMAAuthConfig, error) {
	table, ok := mcpEMATable(section)
	if !ok {
		return nil, errors.New("plugins.mcp_servers.ema_auth must be a table")
	}
	known := map[string]bool{
		"url": true, "client_id": true, "authorization_server_issuer": true,
		"scopes": true, "resource": true,
	}
	for _, key := range mcpEMASortedKeys(table) {
		if !known[key] {
			return nil, fmt.Errorf("unknown field `ema_auth.%s`", key)
		}
	}
	for _, required := range []string{"url", "client_id", "authorization_server_issuer", "resource"} {
		if _, ok := table[required]; !ok {
			return nil, fmt.Errorf("missing field `%s`", required)
		}
	}
	return &PluginMCPServerEMAAuthConfig{
		URL:                       mcpEMAString(table["url"]),
		ClientID:                  mcpEMAString(table["client_id"]),
		AuthorizationServerIssuer: mcpEMAString(table["authorization_server_issuer"]),
		Scopes:                    mcpEMAStringSlice(table["scopes"]),
		Resource:                  mcpEMAString(table["resource"]),
	}, nil
}

func equalMCPEMARegistration(left *PluginMCPServerEMAAuthConfig, right *PluginMCPServerEMAAuthConfig) bool {
	if left == nil || right == nil {
		return left == right
	}
	if left.URL != right.URL || left.ClientID != right.ClientID ||
		left.AuthorizationServerIssuer != right.AuthorizationServerIssuer ||
		left.Resource != right.Resource {
		return false
	}
	if len(left.Scopes) != len(right.Scopes) {
		return false
	}
	for i := range left.Scopes {
		if left.Scopes[i] != right.Scopes[i] {
			return false
		}
	}
	return true
}

func layerConfigMap(layer Layer) map[string]any {
	values, ok := layer.Config.(map[string]any)
	if !ok {
		return nil
	}
	return values
}

func layersHighToLow(layers []Layer) []Layer {
	out := append([]Layer(nil), layers...)
	sort.SliceStable(out, func(i int, j int) bool {
		return out[i].Name.Precedence() > out[j].Name.Precedence()
	})
	return out
}

func layersLowToHigh(layers []Layer) []Layer {
	out := append([]Layer(nil), layers...)
	sort.SliceStable(out, func(i int, j int) bool {
		return out[i].Name.Precedence() < out[j].Name.Precedence()
	})
	return out
}

// isManagedMCPEMALayerSource reports whether a layer is a host/managed source
// that outranks ordinary user configuration for enterprise IdP selection.
func isManagedMCPEMALayerSource(source LayerSourceType) bool {
	switch source {
	case LayerSourceMDM,
		LayerSourceSystem,
		LayerSourceEnterpriseManaged,
		LayerSourceLegacyManagedConfigFromFile,
		LayerSourceLegacyManagedConfigFromMDM:
		return true
	default:
		return false
	}
}

// mcpEnterpriseManagedAuthFromLayers selects one complete trusted registration
// rather than merging issuer/client pairs, mirroring Rust
// McpEnterpriseManagedAuthConfig::from_config_layers.
func mcpEnterpriseManagedAuthFromLayers(layers []Layer, fallback *MCPEnterpriseManagedAuthConfig) (*MCPEnterpriseManagedAuthConfig, error) {
	high := layersHighToLow(layers)
	if len(high) == 0 {
		return cloneMCPEnterpriseManagedAuth(fallback), nil
	}
	type candidate struct {
		source LayerSource
		raw    any
	}
	candidates := make([]candidate, 0, len(high))
	for _, layer := range high {
		values := layerConfigMap(layer)
		if values == nil {
			continue
		}
		raw, ok := values["mcp_enterprise_managed_auth"]
		if !ok {
			continue
		}
		candidates = append(candidates, candidate{source: layer.Name, raw: raw})
	}
	selected := -1
	for i := range candidates {
		if isManagedMCPEMALayerSource(candidates[i].source.Type) {
			selected = i
			break
		}
	}
	if selected < 0 {
		for i := range candidates {
			if candidates[i].source.Type != LayerSourceProject {
				selected = i
				break
			}
		}
	}
	if selected < 0 {
		return nil, nil
	}
	parsed, err := parseMCPEnterpriseManagedAuth(candidates[selected].raw)
	if err != nil {
		return nil, fmt.Errorf("enterprise IdP must be complete in one trusted config layer: %w", err)
	}
	return parsed, nil
}

// mcpEMAServerView is the authorization-relevant projection of a raw MCP
// server config used for provenance comparison.
type mcpEMAServerView struct {
	enabled                        *bool
	auth                           string
	transport                      string
	scopes                         []string
	oauthClientID                  string
	oauthCallbackURL               string
	oauthCallbackPort              uint16
	oauthAuthorizationServerIssuer string
	oauthResource                  string
}

func mcpEMAServerViewFromRaw(raw any) *mcpEMAServerView {
	table, ok := mcpEMATable(raw)
	if !ok {
		return nil
	}
	view := &mcpEMAServerView{}
	if enabled, ok := mcpEMABool(table["enabled"]); ok {
		value := enabled
		view.enabled = &value
	}
	view.auth = strings.ToLower(mcpEMAString(table["auth"]))
	view.scopes = mcpEMAStringSlice(table["scopes"])
	view.oauthResource = firstNonEmptyString(
		mcpEMAString(table["oauth_resource"]),
		mcpEMAString(table["oauthResource"]),
	)
	if oauth, ok := mcpEMATable(table["oauth"]); ok {
		view.oauthClientID = firstNonEmptyString(mcpEMAString(oauth["client_id"]), mcpEMAString(oauth["clientId"]))
		view.oauthCallbackURL = firstNonEmptyString(mcpEMAString(oauth["callback_url"]), mcpEMAString(oauth["callbackUrl"]))
		view.oauthCallbackPort = mcpEMAUint16(firstNonNilValue(oauth["callback_port"], oauth["callbackPort"], oauth["port"]))
		view.oauthAuthorizationServerIssuer = firstNonEmptyString(
			mcpEMAString(oauth["authorization_server_issuer"]),
			mcpEMAString(oauth["authorizationServerIssuer"]),
		)
		view.oauthClientID = firstNonEmptyString(view.oauthClientID, mcpEMAString(table["oauth_client_id"]), mcpEMAString(table["oauthClientId"]))
	}
	if view.oauthResource == "" {
		view.oauthResource = mcpEMAString(table["oauth_resource"])
	}
	view.transport = mcpEMATransportFingerprint(table)
	return view
}

func firstNonNilValue(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func mcpEMAUint16(value any) uint16 {
	switch typed := value.(type) {
	case int:
		if typed > 0 && typed <= 65535 {
			return uint16(typed)
		}
	case int64:
		if typed > 0 && typed <= 65535 {
			return uint16(typed)
		}
	case float64:
		if typed > 0 && typed <= 65535 {
			return uint16(typed)
		}
	case json.Number:
		parsed, err := typed.Int64()
		if err == nil && parsed > 0 && parsed <= 65535 {
			return uint16(parsed)
		}
	}
	return 0
}

// mcpEMATransportFingerprint canonicalizes the transport-relevant fields of a
// raw MCP server table so transport equality can be compared directly.
func mcpEMATransportFingerprint(table map[string]any) string {
	transport := map[string]any{}
	for _, key := range []string{
		"command", "args", "env", "env_vars", "cwd", "url",
		"bearer_token_env_var", "http_headers", "env_http_headers",
		"http_headers_helper", "environment_id",
	} {
		if value, ok := table[key]; ok {
			transport[key] = normalizeJSONValue(value)
		}
	}
	data, err := json.Marshal(transport)
	if err != nil {
		return fmt.Sprintf("%#v", transport)
	}
	return string(data)
}

func mcpEMAServerFromConfig(values map[string]any, name string) *mcpEMAServerView {
	if values == nil {
		return nil
	}
	servers, ok := mcpEMATable(values["mcp_servers"])
	if !ok {
		return nil
	}
	return mcpEMAServerViewFromRaw(servers[name])
}

func (view *mcpEMAServerView) matchesEffectiveAuthorization(effective *mcpEMAServerView) bool {
	if view == nil || effective == nil {
		return false
	}
	if view.auth != MCPServerAuthEMAAuth || view.auth != effective.auth {
		return false
	}
	if view.transport != effective.transport ||
		view.oauthClientID != effective.oauthClientID ||
		view.oauthCallbackURL != effective.oauthCallbackURL ||
		view.oauthCallbackPort != effective.oauthCallbackPort ||
		view.oauthAuthorizationServerIssuer != effective.oauthAuthorizationServerIssuer ||
		view.oauthResource != effective.oauthResource {
		return false
	}
	if len(view.scopes) != len(effective.scopes) {
		return false
	}
	for i := range view.scopes {
		if view.scopes[i] != effective.scopes[i] {
			return false
		}
	}
	return true
}

func mcpEMAServerEnabled(view *mcpEMAServerView) bool {
	if view == nil || view.enabled == nil {
		return true
	}
	return *view.enabled
}

func mcpEMAReenabledOverNonProjectDenial(effectiveEnabled bool, nonProjectServer *mcpEMAServerView) bool {
	if !effectiveEnabled || nonProjectServer == nil || nonProjectServer.enabled == nil {
		return false
	}
	return !*nonProjectServer.enabled
}

func mcpEMAPluginRegistration(values map[string]any, pluginName string, serverName string) *PluginMCPServerEMAAuthConfig {
	if values == nil {
		return nil
	}
	plugins, ok := mcpEMATable(values["plugins"])
	if !ok {
		return nil
	}
	plugin, ok := mcpEMATable(plugins[pluginName])
	if !ok {
		return nil
	}
	servers, ok := mcpEMATable(plugin["mcp_servers"])
	if !ok {
		return nil
	}
	server, ok := mcpEMATable(servers[serverName])
	if !ok {
		return nil
	}
	raw, ok := server["ema_auth"]
	if !ok {
		return nil
	}
	registration, err := parsePluginMCPServerEMAAuth(raw)
	if err != nil {
		return nil
	}
	return registration
}

func mcpEMAMergedConfig(layers []Layer) map[string]any {
	merged := map[string]any{}
	for _, layer := range layersLowToHigh(layers) {
		values := layerConfigMap(layer)
		if values == nil {
			continue
		}
		cloudConfigMergeMap(merged, values)
	}
	return merged
}

func mcpEMAMergedNonProjectConfig(layers []Layer) map[string]any {
	merged := map[string]any{}
	for _, layer := range layersLowToHigh(layers) {
		if layer.Name.Type == LayerSourceProject {
			continue
		}
		values := layerConfigMap(layer)
		if values == nil {
			continue
		}
		cloudConfigMergeMap(merged, values)
	}
	return merged
}

func hasTrustedAtomicMCPEMARegistration(layers []Layer, nonProject map[string]any, matches func(map[string]any) bool) bool {
	if !matches(nonProject) {
		return false
	}
	for _, layer := range layersHighToLow(layers) {
		if layer.Name.Type == LayerSourceProject {
			continue
		}
		if matches(layerConfigMap(layer)) {
			return true
		}
	}
	return false
}

// validateMCPEMAAuthSources mirrors Rust validate_ema_auth_sources: projects
// cannot add, replace, or downgrade a non-project enterprise registration.
func validateMCPEMAAuthSources(layers []Layer, servers map[string]any) error {
	if len(layers) == 0 {
		return nil
	}
	nonProject := mcpEMAMergedNonProjectConfig(layers)
	for _, name := range mcpEMASortedKeys(servers) {
		effective := mcpEMAServerViewFromRaw(servers[name])
		nonProjectServer := mcpEMAServerFromConfig(nonProject, name)
		inheritedEMA := nonProjectServer != nil && nonProjectServer.auth == MCPServerAuthEMAAuth
		if (effective == nil || effective.auth != MCPServerAuthEMAAuth) && !inheritedEMA {
			continue
		}
		if mcpEMAReenabledOverNonProjectDenial(mcpEMAServerEnabled(effective), nonProjectServer) {
			return fmt.Errorf("project configuration cannot re-enable enterprise MCP server `%s`", name)
		}
		matches := func(config map[string]any) bool {
			view := mcpEMAServerFromConfig(config, name)
			return view.matchesEffectiveAuthorization(effective)
		}
		if !hasTrustedAtomicMCPEMARegistration(layers, nonProject, matches) {
			return fmt.Errorf("enterprise MCP server `%s` must define its transport, authorization, scopes, and resource in one non-project config layer", name)
		}
	}

	effective := mcpEMAMergedConfig(layers)
	plugins, ok := mcpEMATable(effective["plugins"])
	if !ok {
		return nil
	}
	for _, pluginName := range mcpEMASortedKeys(plugins) {
		plugin, ok := mcpEMATable(plugins[pluginName])
		if !ok {
			continue
		}
		pluginServers, ok := mcpEMATable(plugin["mcp_servers"])
		if !ok {
			continue
		}
		for _, serverName := range mcpEMASortedKeys(pluginServers) {
			server, ok := mcpEMATable(pluginServers[serverName])
			if !ok {
				continue
			}
			raw, ok := server["ema_auth"]
			if !ok {
				continue
			}
			nonProjectServer := mcpEMAPluginServerView(nonProject, pluginName, serverName)
			effectiveEnabled := true
			if enabled, ok := mcpEMABool(server["enabled"]); ok {
				effectiveEnabled = enabled
			}
			if mcpEMAReenabledOverNonProjectDenial(effectiveEnabled, nonProjectServer) {
				return fmt.Errorf("project configuration cannot re-enable enterprise MCP plugin `%s` server `%s`", pluginName, serverName)
			}
			registration, err := parsePluginMCPServerEMAAuth(raw)
			if err != nil {
				return err
			}
			if !hasTrustedAtomicMCPEMARegistration(layers, nonProject, func(config map[string]any) bool {
				candidate := mcpEMAPluginRegistration(config, pluginName, serverName)
				return equalMCPEMARegistration(candidate, registration)
			}) {
				return fmt.Errorf("enterprise MCP registration for plugin `%s` server `%s` must be defined in one non-project config layer", pluginName, serverName)
			}
		}
	}
	return nil
}

func mcpEMAPluginServerView(values map[string]any, pluginName string, serverName string) *mcpEMAServerView {
	if values == nil {
		return nil
	}
	plugins, ok := mcpEMATable(values["plugins"])
	if !ok {
		return nil
	}
	plugin, ok := mcpEMATable(plugins[pluginName])
	if !ok {
		return nil
	}
	servers, ok := mcpEMATable(plugin["mcp_servers"])
	if !ok {
		return nil
	}
	return mcpEMAServerViewFromRaw(servers[serverName])
}

// validateMCPXAAOptInSource mirrors Rust validate_xaa_opt_in_source: enabling
// use_xaa requires an explicit non-project setting or a managed requirement.
func validateMCPXAAOptInSource(layers []Layer, xaaEnabled bool, required *bool) error {
	if !xaaEnabled || len(layers) == 0 {
		return nil
	}
	if required != nil && *required {
		return nil
	}
	var configured *bool
	for _, layer := range layersHighToLow(layers) {
		if layer.Name.Type == LayerSourceProject {
			continue
		}
		values := layerConfigMap(layer)
		if values == nil {
			continue
		}
		featuresTable, ok := mcpEMATable(values["features"])
		if !ok {
			continue
		}
		if enabled, ok := mcpEMABool(featuresTable["use_xaa"]); ok {
			value := enabled
			configured = &value
			break
		}
	}
	if configured != nil && *configured {
		return nil
	}
	return errors.New("`[features].use_xaa = true` must be selected in a non-project config layer")
}

// validateMCPEMAServerDeclarations enforces per-server declaration rules that
// Rust applies while deserializing McpServerConfig.
func validateMCPEMAServerDeclarations(servers map[string]any) error {
	for _, name := range mcpEMASortedKeys(servers) {
		table, ok := mcpEMATable(servers[name])
		if !ok {
			continue
		}
		auth := strings.ToLower(mcpEMAString(table["auth"]))
		issuer := ""
		if oauth, ok := mcpEMATable(table["oauth"]); ok {
			issuer = firstNonEmptyString(
				mcpEMAString(oauth["authorization_server_issuer"]),
				mcpEMAString(oauth["authorizationServerIssuer"]),
			)
		}
		issuer = firstNonEmptyString(issuer,
			mcpEMAString(table["oauth_authorization_server_issuer"]),
			mcpEMAString(table["oauthAuthorizationServerIssuer"]),
		)
		if issuer != "" && auth != MCPServerAuthEMAAuth {
			return fmt.Errorf("oauth.authorization_server_issuer requires auth = \"ema_auth\"")
		}
	}
	return nil
}

// ResolveMCPEnterpriseManagedAuth resolves the trusted enterprise profile and
// checks provenance at a config load boundary (Rust #44832).
func ResolveMCPEnterpriseManagedAuth(
	layers []Layer,
	fallback *MCPEnterpriseManagedAuthConfig,
	servers map[string]any,
	xaaEnabled bool,
	featureRequirement *bool,
) (*MCPEnterpriseManagedAuthConfig, error) {
	if err := validateMCPEMAServerDeclarations(servers); err != nil {
		return nil, err
	}
	if err := validateMCPEMAAuthSources(layers, servers); err != nil {
		return nil, err
	}
	if err := validateMCPXAAOptInSource(layers, xaaEnabled, featureRequirement); err != nil {
		return nil, err
	}
	return mcpEnterpriseManagedAuthFromLayers(layers, fallback)
}

// XAAFeatureKey is the feature key that opts into enterprise refresh-token
// authorization for configured MCP resources.
func XAAFeatureKey() string {
	return featureflags.UseXAAKey
}

// validateKnownPluginEMAAuthFields strict-validates the per-plugin enterprise
// registration policy (`[plugins.<id>.mcp_servers.<server>.ema_auth]`).
func validateKnownPluginEMAAuthFields(value any) error {
	plugins, ok := mcpEMATable(value)
	if !ok {
		return nil
	}
	for _, pluginName := range mcpEMASortedKeys(plugins) {
		plugin, ok := mcpEMATable(plugins[pluginName])
		if !ok {
			continue
		}
		servers, ok := mcpEMATable(plugin["mcp_servers"])
		if !ok {
			continue
		}
		for _, serverName := range mcpEMASortedKeys(servers) {
			server, ok := mcpEMATable(servers[serverName])
			if !ok {
				continue
			}
			raw, ok := server["ema_auth"]
			if !ok {
				continue
			}
			if _, err := parsePluginMCPServerEMAAuth(raw); err != nil {
				return fmt.Errorf("plugins.%s.mcp_servers.%s.ema_auth: %w", pluginName, serverName, err)
			}
		}
	}
	return nil
}
