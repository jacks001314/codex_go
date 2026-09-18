package model

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func ProvidersFromConfig(values map[string]any, openAIBaseURL string) (map[string]ProviderInfo, error) {
	configured, err := ConfiguredProviderMap(configValue(values, "model_providers"))
	if err != nil {
		return nil, err
	}
	return MergeConfiguredProviders(BuiltInProviders(openAIBaseURL), configured)
}

// RequiredModelProviderDefinition resolves one managed `model_providers`
// requirement entry into the complete provider definition the policy requires
// for providerID (Rust #44944 ConfigManager::check_thread_model_provider).
// Bedrock overrides are merged onto the bundled provider; every other provider
// id is used verbatim. A malformed or incomplete definition is an error, so a
// policy that cannot describe a usable provider rejects the request instead of
// silently passing the comparison.
func RequiredModelProviderDefinition(providerID string, definition any) (*ProviderInfo, error) {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return nil, nil
	}
	raw, ok := definition.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("model_providers.%s must be a table", providerID)
	}
	configured, err := ConfiguredProviderMap(map[string]any{providerID: raw})
	if err != nil {
		return nil, err
	}
	provider, ok := configured[providerID]
	if !ok {
		return nil, nil
	}
	if isBedrockProviderID(providerID) {
		merged, err := MergeConfiguredProviders(BuiltInProviders(""), map[string]ProviderInfo{providerID: provider})
		if err != nil {
			return nil, err
		}
		builtIn := merged[providerID]
		return &builtIn, nil
	}
	return &provider, nil
}

func providerCredentialExportTimeoutMSConfig(values map[string]any) (uint64, error) {
	raw := configValue(values, "timeout_ms")
	if raw == nil {
		return DefaultAWSCredentialExportTimeoutMS, nil
	}
	value, ok := uint64FromAny(raw)
	if !ok {
		return 0, fmt.Errorf("provider aws.credential_export.timeout_ms must be a positive integer")
	}
	if value == 0 {
		return 0, fmt.Errorf("provider aws.credential_export.timeout_ms must be non-zero")
	}
	return value, nil
}

func ProviderForConfigID(values map[string]any, providerID string, openAIBaseURL string) (*ProviderInfo, error) {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		providerID = OpenAIProviderID
	}
	if err := ValidateProviderID(providerID); err != nil {
		return nil, err
	}
	providers, err := ProvidersFromConfig(values, openAIBaseURL)
	if err != nil {
		return nil, err
	}
	provider, ok := providers[providerID]
	if !ok {
		return nil, fmt.Errorf("Model provider `%s` not found", providerID)
	}
	if err := provider.ValidateForConfigID(providerID); err != nil {
		return nil, err
	}
	return &provider, nil
}

func ConfiguredProviderMap(value any) (map[string]ProviderInfo, error) {
	rawProviders, ok := value.(map[string]any)
	if !ok || len(rawProviders) == 0 {
		return nil, nil
	}
	if err := validateReservedModelProviderIDs(rawProviders); err != nil {
		return nil, err
	}
	out := make(map[string]ProviderInfo, len(rawProviders))
	for id, raw := range rawProviders {
		rawProvider, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("model_providers.%s must be a table", id)
		}
		provider, err := providerInfoFromConfig(rawProvider, false)
		if err != nil {
			return nil, fmt.Errorf("model_providers.%s: %w", id, err)
		}
		if !isBedrockProviderID(id) {
			// Mirrors Rust validate_model_providers (config/src/config_toml.rs):
			// aws is only supported for the two bundled Bedrock providers and
			// a custom provider name must not be empty.
			if provider.AWS != nil {
				return nil, fmt.Errorf("model_providers.%s: provider aws is only supported for `%s` or `%s`", id, AmazonBedrockProviderID, AmazonBedrockRuntimeProviderID)
			}
			if strings.TrimSpace(provider.Name) == "" {
				return nil, fmt.Errorf("model_providers.%s: provider name must not be empty", id)
			}
		}
		if isBedrockProviderID(id) {
			if provider.Auth != nil && strings.TrimSpace(provider.Auth.Command) == "" {
				return nil, fmt.Errorf("model_providers.%s: provider auth.command must not be empty", id)
			}
			if provider.AWS != nil {
				if err := provider.AWS.ValidateCredentialExport(); err != nil {
					return nil, fmt.Errorf("model_providers.%s: %w", id, err)
				}
			}
		} else {
			if err := provider.Validate(); err != nil {
				return nil, fmt.Errorf("model_providers.%s: %w", id, err)
			}
		}
		out[id] = *provider
	}
	return out, nil
}

// reservedModelProviderIDs mirrors Rust RESERVED_MODEL_PROVIDER_IDS
// (config/src/config_toml.rs): these ids cannot be overridden by a custom
// model_providers entry; the two Bedrock ids are exempt because their config
// blocks only extend the bundled provider.
var reservedModelProviderIDs = map[string]bool{
	OpenAIProviderID:               true,
	OllamaOSSProviderID:            true,
	LMStudioOSSProviderID:          true,
	AmazonBedrockProviderID:        true,
	AmazonBedrockRuntimeProviderID: true,
}

func isBedrockProviderID(id string) bool {
	return id == AmazonBedrockProviderID || id == AmazonBedrockRuntimeProviderID
}

func validateReservedModelProviderIDs(rawProviders map[string]any) error {
	var conflicts []string
	for id := range rawProviders {
		if reservedModelProviderIDs[id] && !isBedrockProviderID(id) {
			conflicts = append(conflicts, "`"+id+"`")
		}
	}
	if len(conflicts) == 0 {
		return nil
	}
	sort.Strings(conflicts)
	return fmt.Errorf("model_providers contains reserved built-in provider IDs: %s. Built-in providers cannot be overridden. Rename your custom provider (for example, `openai-custom`).", strings.Join(conflicts, ", "))
}

func ProviderInfoFromConfig(values map[string]any) (*ProviderInfo, error) {
	return providerInfoFromConfig(values, true)
}

func providerInfoFromConfig(values map[string]any, validate bool) (*ProviderInfo, error) {
	provider := &ProviderInfo{
		Name:                    stringConfig(values, "name"),
		BaseURL:                 stringConfig(values, "base_url"),
		EnvKey:                  stringConfig(values, "env_key"),
		EnvKeyInstructions:      stringConfig(values, "env_key_instructions"),
		ExperimentalBearerToken: stringConfig(values, "experimental_bearer_token"),
		QueryParams:             stringMapConfig(values, "query_params"),
		HTTPHeaders:             stringMapConfig(values, "http_headers"),
		EnvHTTPHeaders:          stringMapConfig(values, "env_http_headers"),
		RequestMaxRetries:       uint64PtrConfig(values, "request_max_retries"),
		StreamMaxRetries:        uint64PtrConfig(values, "stream_max_retries"),
		StreamIdleTimeoutMS:     uint64PtrConfig(values, "stream_idle_timeout_ms"),
		WireAPI:                 WireAPIResponses,
		WebsocketConnectTimeoutMS: uint64PtrConfig(
			values,
			"websocket_connect_timeout_ms",
		),
		RequiresOpenAIAuth:          boolConfig(values, "requires_openai_auth"),
		SupportsWebsockets:          boolConfig(values, "supports_websockets"),
		SupportsStandaloneWebSearch: boolConfig(values, "supports_standalone_web_search"),
	}
	if wireAPI := stringConfig(values, "wire_api"); wireAPI != "" {
		parsed, err := ParseWireAPI(wireAPI)
		if err != nil {
			return nil, err
		}
		provider.WireAPI = parsed
	}
	if authConfig, ok := configValue(values, "auth").(map[string]any); ok {
		auth, err := providerAuthInfoFromConfig(authConfig)
		if err != nil {
			return nil, err
		}
		provider.Auth = auth
	}
	if awsConfig, ok := configValue(values, "aws").(map[string]any); ok {
		provider.AWS = &ProviderAWSAuthInfo{
			Profile: stringConfig(awsConfig, "profile"),
			Region:  stringConfig(awsConfig, "region"),
		}
		if exportConfig, ok := configValue(awsConfig, "credential_export").(map[string]any); ok {
			timeoutMS, err := providerCredentialExportTimeoutMSConfig(exportConfig)
			if err != nil {
				return nil, err
			}
			provider.AWS.CredentialExport = &ProviderCredentialExportInfo{
				Command:   stringConfig(exportConfig, "command"),
				Args:      stringSliceConfig(exportConfig, "args"),
				TimeoutMS: timeoutMS,
			}
		}
		if refreshConfig, ok := configValue(awsConfig, "auth_refresh").(map[string]any); ok {
			timeoutMS, err := providerAuthRefreshTimeoutMSConfig(refreshConfig)
			if err != nil {
				return nil, err
			}
			provider.AWS.AuthRefresh = &ProviderAuthRefreshInfo{
				Command:   stringConfig(refreshConfig, "command"),
				Args:      stringSliceConfig(refreshConfig, "args"),
				TimeoutMS: timeoutMS,
			}
		}
	}
	if gatewayConfig, ok := configValue(values, "gateway_oauth").(map[string]any); ok {
		gateway, err := gatewayOAuthConfigFromConfig(gatewayConfig)
		if err != nil {
			return nil, err
		}
		provider.GatewayOAuth = gateway
	}
	if validate {
		if err := provider.Validate(); err != nil {
			return nil, err
		}
	}
	return provider, nil
}

// gatewayOAuthConfigFromConfig parses Rust's GatewayOAuthConfig (#46482) from a
// model provider configuration table.
func gatewayOAuthConfigFromConfig(values map[string]any) (*GatewayOAuthConfig, error) {
	gateway := &GatewayOAuthConfig{
		AuthorizationURL: stringConfig(values, "authorization_url"),
		TokenURL:         stringConfig(values, "token_url"),
		ClientID:         stringConfig(values, "client_id"),
		Scopes:           stringSliceConfig(values, "scopes"),
	}
	if resource, ok := configValue(values, "resource").(string); ok {
		gateway.Resource = &resource
	}
	if rawPort := configValue(values, "redirect_port"); rawPort != nil {
		port, ok := uint64FromAny(rawPort)
		if !ok {
			return nil, fmt.Errorf("gateway_oauth.redirect_port must be a positive integer")
		}
		if port == 0 {
			return nil, fmt.Errorf("gateway_oauth requires a nonempty client_id and a nonzero redirect_port")
		}
		value := uint16(port)
		gateway.RedirectPort = &value
	}
	delivery, ok := configValue(values, "delivery").(map[string]any)
	if !ok {
		return nil, fmt.Errorf("gateway_oauth.delivery must be a table")
	}
	kind := strings.TrimSpace(stringConfig(delivery, "kind"))
	switch kind {
	case "header":
		gateway.Delivery = GatewayOAuthDelivery{
			Kind:   kind,
			Name:   stringConfig(delivery, "name"),
			Scheme: stringConfig(delivery, "scheme"),
		}
	case "cookie":
		gateway.Delivery = GatewayOAuthDelivery{Kind: kind, Name: stringConfig(delivery, "name")}
	default:
		return nil, fmt.Errorf("invalid gateway_oauth delivery kind")
	}
	return gateway, nil
}

func providerAuthRefreshTimeoutMSConfig(values map[string]any) (uint64, error) {
	raw := configValue(values, "timeout_ms")
	if raw == nil {
		return DefaultProviderAuthTimeoutMS, nil
	}
	value, ok := uint64FromAny(raw)
	if !ok {
		return 0, fmt.Errorf("provider aws.auth_refresh.timeout_ms must be a positive integer")
	}
	if value == 0 {
		return 0, fmt.Errorf("provider aws.auth_refresh.timeout_ms must be non-zero")
	}
	return value, nil
}

func providerAuthInfoFromConfig(values map[string]any) (*ProviderAuthInfo, error) {
	timeoutMS, err := providerAuthTimeoutMSConfig(values)
	if err != nil {
		return nil, err
	}
	return &ProviderAuthInfo{
		Command:           stringConfig(values, "command"),
		Args:              stringSliceConfig(values, "args"),
		TimeoutMS:         timeoutMS,
		RefreshIntervalMS: providerAuthRefreshIntervalMSConfig(values),
		CWD:               providerAuthCWDConfig(values),
	}, nil
}

func providerAuthTimeoutMSConfig(values map[string]any) (uint64, error) {
	raw := configValue(values, "timeout_ms")
	if raw == nil {
		return DefaultProviderAuthTimeoutMS, nil
	}
	value, ok := uint64FromAny(raw)
	if !ok {
		return 0, fmt.Errorf("provider auth.timeout_ms must be a positive integer")
	}
	if value == 0 {
		return 0, fmt.Errorf("provider auth.timeout_ms must be non-zero")
	}
	return value, nil
}

func providerAuthRefreshIntervalMSConfig(values map[string]any) uint64 {
	if configValue(values, "refresh_interval_ms") == nil {
		return DefaultProviderAuthRefreshMS
	}
	return uint64Config(values, "refresh_interval_ms")
}

func providerAuthCWDConfig(values map[string]any) string {
	cwd := stringConfig(values, "cwd")
	if cwd == "" {
		cwd = defaultProviderAuthCWD()
	}
	return resolveProviderAuthCWD(cwd)
}

func defaultProviderAuthCWD() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}

func resolveProviderAuthCWD(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return ""
	}
	if filepath.IsAbs(cwd) {
		return filepath.Clean(cwd)
	}
	absolute, err := filepath.Abs(cwd)
	if err != nil {
		return filepath.Clean(cwd)
	}
	return filepath.Clean(absolute)
}

func (p *ProviderInfo) ValidateForConfigID(providerID string) error {
	providerID = strings.TrimSpace(providerID)
	if p != nil && p.AWS != nil && !isBedrockProviderID(providerID) {
		return fmt.Errorf("provider aws is only supported for `%s` or `%s`", AmazonBedrockProviderID, AmazonBedrockRuntimeProviderID)
	}
	if isBedrockProviderID(providerID) {
		return nil
	}
	if err := p.Validate(); err != nil {
		return err
	}
	return nil
}

func configValue(values map[string]any, key string) any {
	if values == nil {
		return nil
	}
	return values[key]
}

func stringConfig(values map[string]any, key string) string {
	value, _ := configValue(values, key).(string)
	return strings.TrimSpace(value)
}

func boolConfig(values map[string]any, key string) bool {
	value, _ := configValue(values, key).(bool)
	return value
}

func uint64Config(values map[string]any, key string) uint64 {
	value, _ := uint64FromAny(configValue(values, key))
	return value
}

func uint64PtrConfig(values map[string]any, key string) *uint64 {
	value, ok := uint64FromAny(configValue(values, key))
	if !ok {
		return nil
	}
	return &value
}

func uint64FromAny(value any) (uint64, bool) {
	switch v := value.(type) {
	case int:
		if v >= 0 {
			return uint64(v), true
		}
	case int64:
		if v >= 0 {
			return uint64(v), true
		}
	case float64:
		if v >= 0 && v == float64(uint64(v)) {
			return uint64(v), true
		}
	}
	return 0, false
}

func stringMapConfig(values map[string]any, key string) map[string]string {
	raw, ok := configValue(values, key).(map[string]any)
	if !ok || len(raw) == 0 {
		return nil
	}
	out := make(map[string]string, len(raw))
	for name, value := range raw {
		text, ok := value.(string)
		if !ok {
			continue
		}
		out[name] = text
	}
	return out
}

func stringSliceConfig(values map[string]any, key string) []string {
	switch raw := configValue(values, key).(type) {
	case []any:
		out := make([]string, 0, len(raw))
		for _, value := range raw {
			text, ok := value.(string)
			if ok {
				out = append(out, text)
			}
		}
		return out
	case []string:
		return append([]string(nil), raw...)
	default:
		return nil
	}
}
