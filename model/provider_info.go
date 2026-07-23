package model

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultStreamIdleTimeoutMS       uint64 = 300000
	DefaultStreamMaxRetries          uint64 = 5
	DefaultRequestMaxRetries         uint64 = 4
	DefaultWebsocketConnectTimeoutMS uint64 = 15000
	DefaultProviderAuthTimeoutMS     uint64 = 5000
	DefaultProviderAuthRefreshMS     uint64 = 300000
	MaxStreamMaxRetries              uint64 = 100
	MaxRequestMaxRetries             uint64 = 100

	OpenAIProviderID                        = "openai"
	OpenAIProviderName                      = "OpenAI"
	OpenAIActorAuthorizationHeader          = "x-openai-actor-authorization"
	ChatGPTCodexBaseURL                     = "https://chatgpt.com/backend-api/codex"
	AmazonBedrockProviderID                 = "amazon-bedrock"
	AmazonBedrockProviderName               = "Amazon Bedrock"
	AmazonBedrockDefaultBaseURL             = "https://bedrock-mantle.us-east-1.api.aws/openai/v1"
	AmazonBedrockBearerTokenEnv             = "AWS_BEARER_TOKEN_BEDROCK"
	AmazonBedrockMantleServiceName          = "bedrock-mantle"
	AmazonBedrockMantleClientHeader         = "x-amzn-mantle-client-agent"
	AmazonBedrockMantleClientValue          = "codex"
	AmazonBedrockGPT55ModelID               = "openai.gpt-5.5"
	AmazonBedrockGPT54ModelID               = "openai.gpt-5.4"
	AmazonBedrockGPT56SolModelID            = "openai.gpt-5.6-sol"
	AmazonBedrockGPT56TerraModelID          = "openai.gpt-5.6-terra"
	AmazonBedrockGPT56LunaModelID           = "openai.gpt-5.6-luna"
	LegacyOllamaChatProviderID              = "ollama-chat"
	OllamaChatProviderRemovedMessage        = "`ollama-chat` is no longer supported.\nHow to fix: replace `ollama-chat` with `ollama` in `model_provider`, `oss_provider`, or `--local-provider`.\nMore info: https://github.com/openai/codex/discussions/7782"
	LMStudioOSSProviderID                   = "lmstudio"
	OllamaOSSProviderID                     = "ollama"
	DefaultLMStudioPort              uint16 = 1234
	DefaultOllamaPort                uint16 = 11434
)

const chatWireAPIRemovedMessage = "`wire_api = \"chat\"` is no longer supported.\nHow to fix: set `wire_api = \"responses\"` in your provider config.\nMore info: https://github.com/openai/codex/discussions/7782"

type WireAPI string

const (
	WireAPIResponses WireAPI = "responses"
)

func ParseWireAPI(value string) (WireAPI, error) {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "", string(WireAPIResponses):
		return WireAPIResponses, nil
	case "chat":
		return "", errors.New(chatWireAPIRemovedMessage)
	default:
		return "", fmt.Errorf("unknown wire_api %q", value)
	}
}

func ValidateProviderID(providerID string) error {
	if strings.TrimSpace(providerID) == LegacyOllamaChatProviderID {
		return errors.New(OllamaChatProviderRemovedMessage)
	}
	return nil
}

type ProviderAuthInfo struct {
	Command           string   `json:"command,omitempty"`
	Args              []string `json:"args,omitempty"`
	TimeoutMS         uint64   `json:"timeout_ms,omitempty"`
	RefreshIntervalMS uint64   `json:"refresh_interval_ms,omitempty"`
	CWD               string   `json:"cwd,omitempty"`
}

type ProviderAWSAuthInfo struct {
	Profile string `json:"profile,omitempty"`
	Region  string `json:"region,omitempty"`
}

type ProviderInfo struct {
	Name                        string               `json:"name,omitempty"`
	BaseURL                     string               `json:"base_url,omitempty"`
	EnvKey                      string               `json:"env_key,omitempty"`
	EnvKeyInstructions          string               `json:"env_key_instructions,omitempty"`
	ExperimentalBearerToken     string               `json:"experimental_bearer_token,omitempty"`
	Auth                        *ProviderAuthInfo    `json:"auth,omitempty"`
	AWS                         *ProviderAWSAuthInfo `json:"aws,omitempty"`
	WireAPI                     WireAPI              `json:"wire_api,omitempty"`
	QueryParams                 map[string]string    `json:"query_params,omitempty"`
	HTTPHeaders                 map[string]string    `json:"http_headers,omitempty"`
	EnvHTTPHeaders              map[string]string    `json:"env_http_headers,omitempty"`
	RequestMaxRetries           *uint64              `json:"request_max_retries,omitempty"`
	StreamMaxRetries            *uint64              `json:"stream_max_retries,omitempty"`
	StreamIdleTimeoutMS         *uint64              `json:"stream_idle_timeout_ms,omitempty"`
	WebsocketConnectTimeoutMS   *uint64              `json:"websocket_connect_timeout_ms,omitempty"`
	RequiresOpenAIAuth          bool                 `json:"requires_openai_auth,omitempty"`
	SupportsWebsockets          bool                 `json:"supports_websockets,omitempty"`
	SupportsStandaloneWebSearch bool                 `json:"supports_standalone_web_search,omitempty"`
}

type APIProvider struct {
	Name              string
	BaseURL           string
	QueryParams       map[string]string
	Headers           http.Header
	Auth              *ProviderAuthInfo
	RequestMaxRetries uint64
	StreamMaxRetries  uint64
	StreamIdleTimeout time.Duration
}

func (p *ProviderInfo) Validate() error {
	if p.AWS != nil {
		if p.SupportsWebsockets {
			return errors.New("provider aws cannot be combined with supports_websockets")
		}
		var conflicts []string
		if p.EnvKey != "" {
			conflicts = append(conflicts, "env_key")
		}
		if p.ExperimentalBearerToken != "" {
			conflicts = append(conflicts, "experimental_bearer_token")
		}
		if p.Auth != nil {
			conflicts = append(conflicts, "auth")
		}
		if p.RequiresOpenAIAuth {
			conflicts = append(conflicts, "requires_openai_auth")
		}
		if len(conflicts) > 0 {
			return fmt.Errorf("provider aws cannot be combined with %s", strings.Join(conflicts, ", "))
		}
	}

	if p.Auth == nil {
		return nil
	}
	if strings.TrimSpace(p.Auth.Command) == "" {
		return errors.New("provider auth.command must not be empty")
	}
	var conflicts []string
	if p.EnvKey != "" {
		conflicts = append(conflicts, "env_key")
	}
	if p.ExperimentalBearerToken != "" {
		conflicts = append(conflicts, "experimental_bearer_token")
	}
	if p.RequiresOpenAIAuth {
		conflicts = append(conflicts, "requires_openai_auth")
	}
	if len(conflicts) > 0 {
		return fmt.Errorf("provider auth cannot be combined with %s", strings.Join(conflicts, ", "))
	}
	return nil
}

func (p *ProviderInfo) ToAPIProvider(authMode string) (APIProvider, error) {
	baseURL := p.BaseURL
	if baseURL == "" {
		if usesChatGPTCodexBaseURL(authMode) {
			baseURL = ChatGPTCodexBaseURL
		} else {
			baseURL = "https://api.openai.com/v1"
		}
	}
	headers := p.BuildHeaderMap()
	return APIProvider{
		Name:              p.Name,
		BaseURL:           baseURL,
		QueryParams:       cloneMap(p.QueryParams),
		Headers:           headers,
		Auth:              cloneProviderAuthInfo(p.Auth),
		RequestMaxRetries: p.EffectiveRequestMaxRetries(),
		StreamMaxRetries:  p.EffectiveStreamMaxRetries(),
		StreamIdleTimeout: p.EffectiveStreamIdleTimeout(),
	}, nil
}

func (p *ProviderInfo) BuildHeaderMap() http.Header {
	headers := http.Header{}
	for key, value := range p.HTTPHeaders {
		if !validProviderHeaderName(key) || !validProviderHeaderValue(value) {
			continue
		}
		headers.Set(key, value)
	}
	for header, envKey := range p.EnvHTTPHeaders {
		value := strings.TrimSpace(os.Getenv(envKey))
		if value == "" || !validProviderHeaderName(header) || !validProviderHeaderValue(value) {
			continue
		}
		headers.Set(header, value)
	}
	return headers
}

func validProviderHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			continue
		}
		switch c {
		case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
			continue
		default:
			return false
		}
	}
	return true
}

func validProviderHeaderValue(value string) bool {
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c == '\t' {
			continue
		}
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

func (p *ProviderInfo) APIKey() (string, error) {
	if strings.TrimSpace(p.EnvKey) == "" {
		return "", nil
	}
	value := strings.TrimSpace(os.Getenv(p.EnvKey))
	if value == "" {
		if p.EnvKeyInstructions != "" {
			return "", fmt.Errorf("environment variable %s is required: %s", p.EnvKey, p.EnvKeyInstructions)
		}
		return "", fmt.Errorf("environment variable %s is required", p.EnvKey)
	}
	return value, nil
}

func (p *ProviderInfo) EffectiveRequestMaxRetries() uint64 {
	if p.RequestMaxRetries == nil {
		return DefaultRequestMaxRetries
	}
	return minUint64(*p.RequestMaxRetries, MaxRequestMaxRetries)
}

func (p *ProviderInfo) EffectiveStreamMaxRetries() uint64 {
	if p.StreamMaxRetries == nil {
		return DefaultStreamMaxRetries
	}
	return minUint64(*p.StreamMaxRetries, MaxStreamMaxRetries)
}

func (p *ProviderInfo) EffectiveStreamIdleTimeout() time.Duration {
	if p.StreamIdleTimeoutMS == nil {
		return time.Duration(DefaultStreamIdleTimeoutMS) * time.Millisecond
	}
	return time.Duration(*p.StreamIdleTimeoutMS) * time.Millisecond
}

func (p *ProviderInfo) EffectiveWebsocketConnectTimeout() time.Duration {
	if p.WebsocketConnectTimeoutMS == nil {
		return time.Duration(DefaultWebsocketConnectTimeoutMS) * time.Millisecond
	}
	return time.Duration(*p.WebsocketConnectTimeoutMS) * time.Millisecond
}

func (p *ProviderInfo) IsOpenAI() bool {
	return p.Name == OpenAIProviderName
}

func (p *ProviderInfo) IsAmazonBedrock() bool {
	return p.Name == AmazonBedrockProviderName
}

func (p *ProviderInfo) UsesOpenAIActorAuthorization() bool {
	if p.RequiresOpenAIAuth {
		return false
	}
	for name, value := range p.HTTPHeaders {
		if strings.EqualFold(name, OpenAIActorAuthorizationHeader) && strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func (p *ProviderInfo) SupportsRemoteCompaction() bool {
	return p.IsOpenAI() || IsAzureResponsesProvider(p.Name, p.BaseURL)
}

func (p *ProviderInfo) HasCommandAuth() bool {
	return p.Auth != nil
}

func cloneProviderAuthInfo(info *ProviderAuthInfo) *ProviderAuthInfo {
	if info == nil {
		return nil
	}
	return &ProviderAuthInfo{
		Command:           info.Command,
		Args:              append([]string(nil), info.Args...),
		TimeoutMS:         info.TimeoutMS,
		RefreshIntervalMS: info.RefreshIntervalMS,
		CWD:               info.CWD,
	}
}

func CreateOpenAIProvider(baseURL string) ProviderInfo {
	return ProviderInfo{
		Name:                        OpenAIProviderName,
		BaseURL:                     baseURL,
		WireAPI:                     WireAPIResponses,
		HTTPHeaders:                 map[string]string{"version": "go-port"},
		EnvHTTPHeaders:              map[string]string{"OpenAI-Organization": "OPENAI_ORGANIZATION", "OpenAI-Project": "OPENAI_PROJECT"},
		RequiresOpenAIAuth:          true,
		SupportsWebsockets:          true,
		SupportsStandaloneWebSearch: true,
	}
}

func CreateAmazonBedrockProvider(aws *ProviderAWSAuthInfo) ProviderInfo {
	if aws == nil {
		aws = &ProviderAWSAuthInfo{}
	}
	return ProviderInfo{
		Name: AmazonBedrockProviderName,
		// An empty base URL means the runtime derives the regional Mantle endpoint.
		BaseURL:            "",
		AWS:                &ProviderAWSAuthInfo{Profile: aws.Profile, Region: aws.Region},
		WireAPI:            WireAPIResponses,
		HTTPHeaders:        map[string]string{AmazonBedrockMantleClientHeader: AmazonBedrockMantleClientValue},
		RequiresOpenAIAuth: false,
		SupportsWebsockets: false,
	}
}

func CreateOSSProvider(defaultProviderPort uint16, wireAPI WireAPI) ProviderInfo {
	port := defaultProviderPort
	if value := strings.TrimSpace(os.Getenv("CODEX_OSS_PORT")); value != "" {
		if parsed, err := strconv.ParseUint(value, 10, 16); err == nil {
			port = uint16(parsed)
		}
	}
	baseURL := strings.TrimSpace(os.Getenv("CODEX_OSS_BASE_URL"))
	if baseURL == "" {
		baseURL = fmt.Sprintf("http://localhost:%d/v1", port)
	}
	return CreateOSSProviderWithBaseURL(baseURL, wireAPI)
}

func CreateOSSProviderWithBaseURL(baseURL string, wireAPI WireAPI) ProviderInfo {
	if wireAPI == "" {
		wireAPI = WireAPIResponses
	}
	return ProviderInfo{
		Name:               "gpt-oss",
		BaseURL:            baseURL,
		WireAPI:            wireAPI,
		RequiresOpenAIAuth: false,
		SupportsWebsockets: false,
	}
}

func BuiltInProviders(openAIBaseURL string) map[string]ProviderInfo {
	return map[string]ProviderInfo{
		OpenAIProviderID:        CreateOpenAIProvider(openAIBaseURL),
		AmazonBedrockProviderID: CreateAmazonBedrockProvider(nil),
		OllamaOSSProviderID:     CreateOSSProvider(DefaultOllamaPort, WireAPIResponses),
		LMStudioOSSProviderID:   CreateOSSProvider(DefaultLMStudioPort, WireAPIResponses),
	}
}

func MergeConfiguredProviders(providers map[string]ProviderInfo, configured map[string]ProviderInfo) (map[string]ProviderInfo, error) {
	out := cloneProviderMap(providers)
	for key, provider := range configured {
		if key == AmazonBedrockProviderID {
			baseURLOverride := provider.BaseURL
			provider.BaseURL = ""
			authOverride := provider.Auth
			provider.Auth = nil
			awsOverride := provider.AWS
			provider.AWS = nil
			httpHeadersOverride := provider.HTTPHeaders
			provider.HTTPHeaders = nil
			if !(&provider).isZero() {
				return nil, fmt.Errorf("model_providers.%s only supports changing `base_url`, `auth`, `http_headers`, `aws.profile`, and `aws.region`; other non-default provider fields are not supported", AmazonBedrockProviderID)
			}
			builtIn := out[AmazonBedrockProviderID]
			builtIn.BaseURL = baseURLOverride
			builtIn.Auth = cloneProviderAuthInfo(authOverride)
			if awsOverride != nil {
				builtIn.AWS = &ProviderAWSAuthInfo{Profile: awsOverride.Profile, Region: awsOverride.Region}
			}
			if len(httpHeadersOverride) > 0 {
				if builtIn.HTTPHeaders == nil {
					builtIn.HTTPHeaders = map[string]string{}
				}
				for name, value := range httpHeadersOverride {
					builtIn.HTTPHeaders[name] = value
				}
			}
			out[AmazonBedrockProviderID] = builtIn
			continue
		}
		if _, exists := out[key]; !exists {
			out[key] = provider
		}
	}
	return out, nil
}

func IsAzureResponsesProvider(name, baseURL string) bool {
	if strings.EqualFold(name, "azure") {
		return true
	}
	baseURL = strings.ToLower(baseURL)
	for _, marker := range []string{
		"openai.azure.",
		"cognitiveservices.azure.",
		"aoai.azure.",
		"azure-api.",
		"azurefd.",
		"windows.net/openai",
	} {
		if strings.Contains(baseURL, marker) {
			return true
		}
	}
	return false
}

func usesChatGPTCodexBaseURL(authMode string) bool {
	switch authMode {
	case "chatgpt", "chatgptAuthTokens", "chatgpt-auth-tokens", "agent-identity", "personal-access-token":
		return true
	default:
		return false
	}
}

func cloneProviderMap(in map[string]ProviderInfo) map[string]ProviderInfo {
	out := make(map[string]ProviderInfo, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func (p *ProviderInfo) isZero() bool {
	return p.Name == "" &&
		p.BaseURL == "" &&
		p.EnvKey == "" &&
		p.EnvKeyInstructions == "" &&
		p.ExperimentalBearerToken == "" &&
		p.Auth == nil &&
		p.AWS == nil &&
		(p.WireAPI == "" || p.WireAPI == WireAPIResponses) &&
		len(p.QueryParams) == 0 &&
		len(p.HTTPHeaders) == 0 &&
		len(p.EnvHTTPHeaders) == 0 &&
		p.RequestMaxRetries == nil &&
		p.StreamMaxRetries == nil &&
		p.StreamIdleTimeoutMS == nil &&
		p.WebsocketConnectTimeoutMS == nil &&
		!p.RequiresOpenAIAuth &&
		!p.SupportsWebsockets
}

func minUint64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}
