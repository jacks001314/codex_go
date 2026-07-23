package model

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func ProvidersFromConfig(values map[string]any, openAIBaseURL string) (map[string]ProviderInfo, error) {
	configured, err := ConfiguredProviderMap(configValue(values, "model_providers"))
	if err != nil {
		return nil, err
	}
	return MergeConfiguredProviders(BuiltInProviders(openAIBaseURL), configured)
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
		if provider.AWS != nil && id != AmazonBedrockProviderID {
			return nil, fmt.Errorf("model_providers.%s: provider aws is only supported for `%s`", id, AmazonBedrockProviderID)
		}
		if id == AmazonBedrockProviderID {
			if provider.Auth != nil && strings.TrimSpace(provider.Auth.Command) == "" {
				return nil, fmt.Errorf("model_providers.%s: provider auth.command must not be empty", id)
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
	}
	if validate {
		if err := provider.Validate(); err != nil {
			return nil, err
		}
	}
	return provider, nil
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
	if p != nil && p.AWS != nil && providerID != AmazonBedrockProviderID {
		return fmt.Errorf("provider aws is only supported for `%s`", AmazonBedrockProviderID)
	}
	if providerID == AmazonBedrockProviderID {
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
	raw, ok := configValue(values, key).([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, value := range raw {
		text, ok := value.(string)
		if ok {
			out = append(out, text)
		}
	}
	return out
}
