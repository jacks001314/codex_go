package model

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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
	// DefaultAWSCredentialExportTimeoutMS mirrors Rust
	// DEFAULT_AWS_CREDENTIAL_EXPORT_TIMEOUT_MS (#44028).
	DefaultAWSCredentialExportTimeoutMS uint64 = 30000
	// MaxAWSCredentialExportOutputBytes bounds credential-export command output
	// (Rust MAX_CREDENTIAL_OUTPUT_BYTES).
	MaxAWSCredentialExportOutputBytes        = 64 * 1024
	MaxStreamMaxRetries               uint64 = 100
	MaxRequestMaxRetries              uint64 = 100

	OpenAIProviderID                 = "openai"
	OpenAIProviderName               = "OpenAI"
	OpenAIActorAuthorizationHeader   = "x-openai-actor-authorization"
	ChatGPTCodexBaseURL              = "https://chatgpt.com/backend-api/codex"
	AmazonBedrockProviderID          = "amazon-bedrock"
	AmazonBedrockProviderName        = "Amazon Bedrock"
	AmazonBedrockRuntimeProviderID   = "amazon-bedrock-runtime"
	AmazonBedrockRuntimeProviderName = "Amazon Bedrock Runtime"
	AmazonBedrockDefaultBaseURL      = "https://bedrock-mantle.us-east-1.api.aws/openai/v1"
	AmazonBedrockBearerTokenEnv      = "AWS_BEARER_TOKEN_BEDROCK"
	AmazonBedrockMantleServiceName   = "bedrock-mantle"
	AmazonBedrockMantleClientHeader  = "x-amzn-mantle-client-agent"
	AmazonBedrockMantleClientValue   = "codex"
	AmazonBedrockGPT55ModelID        = "openai.gpt-5.5"
	AmazonBedrockGPT54ModelID        = "openai.gpt-5.4"
	AmazonBedrockGPT56SolModelID     = "openai.gpt-5.6-sol"
	AmazonBedrockGPT56TerraModelID   = "openai.gpt-5.6-terra"
	AmazonBedrockGPT56LunaModelID    = "openai.gpt-5.6-luna"
	// AmazonBedrockGPT6AstraModelID is the Bedrock slug for the bundled
	// gpt-6-astra model (Rust #42619).
	AmazonBedrockGPT6AstraModelID           = "openai.gpt-6-astra"
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
	// CredentialExport mirrors Rust #44028: a command whose JSON output supplies
	// SigV4 signing credentials for Amazon Bedrock.
	CredentialExport *ProviderCredentialExportInfo `json:"credential_export,omitempty"`
	// AuthRefresh mirrors Rust #39410: an `aws` command that refreshes
	// expired AWS SDK credentials for Bedrock sessions, plus a timeout.
	AuthRefresh *ProviderAuthRefreshInfo `json:"auth_refresh,omitempty"`
}

type ProviderCredentialExportInfo struct {
	Command   string   `json:"command,omitempty"`
	Args      []string `json:"args,omitempty"`
	TimeoutMS uint64   `json:"timeout_ms,omitempty"`
}

// ValidateCredentialExport mirrors Rust ModelProviderInfo::validate (#44028):
// credential export must not be combined with a shared profile, and its
// command must be a non-empty absolute path or bare executable name.
func (a *ProviderAWSAuthInfo) ValidateCredentialExport() error {
	if a == nil || a.CredentialExport == nil {
		return nil
	}
	if strings.TrimSpace(a.Profile) != "" {
		return errors.New("provider aws.credential_export cannot be combined with aws.profile")
	}
	if strings.TrimSpace(a.CredentialExport.Command) == "" {
		return errors.New("provider aws.credential_export.command must not be empty")
	}
	if !providerCommandIsAbsoluteOrBare(a.CredentialExport.Command) {
		return errors.New("provider aws.credential_export.command must be an absolute path or a bare executable name")
	}
	return nil
}

type ProviderAuthRefreshInfo struct {
	Command   string   `json:"command,omitempty"`
	Args      []string `json:"args,omitempty"`
	TimeoutMS uint64   `json:"timeout_ms,omitempty"`
}

// GatewayOAuthConfig mirrors Rust model-provider-info's GatewayOAuthConfig
// (#46482): secondary OAuth credentials delivered alongside the provider's
// primary authentication.
type GatewayOAuthConfig struct {
	AuthorizationURL string               `json:"authorization_url,omitempty"`
	TokenURL         string               `json:"token_url,omitempty"`
	ClientID         string               `json:"client_id,omitempty"`
	Resource         *string              `json:"resource,omitempty"`
	Scopes           []string             `json:"scopes,omitempty"`
	RedirectPort     *uint16              `json:"redirect_port,omitempty"`
	Delivery         GatewayOAuthDelivery `json:"delivery"`
}

// GatewayOAuthDelivery is Rust's tagged delivery enum: `{kind: "header", name,
// scheme}` or `{kind: "cookie", name}`. Scheme defaults to "Bearer" for the
// header kind.
type GatewayOAuthDelivery struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Scheme string `json:"scheme,omitempty"`
}

// EffectiveHeaderName returns the request header the delivery writes, and an
// empty string for an unknown kind. It mirrors Rust's `header` computation in
// GatewayOAuthConfig::validate.
func (d GatewayOAuthDelivery) EffectiveHeaderName() string {
	switch d.Kind {
	case "header":
		return d.Name
	case "cookie":
		return "cookie"
	}
	return ""
}

// HeaderScheme returns the delivery scheme, applying Rust's "Bearer" default.
func (d GatewayOAuthDelivery) HeaderScheme() string {
	if d.Kind == "header" && d.Scheme == "" {
		return "Bearer"
	}
	return d.Scheme
}

// reservedGatewayOAuthHeaders are the authentication, routing/framing and
// internal protocol headers the delivery must not overwrite.
var reservedGatewayOAuthHeaders = map[string]bool{
	"authorization":       true,
	"proxy-authorization": true,
	"cookie":              true,
	"host":                true,
	"content-length":      true,
	"transfer-encoding":   true,
	"connection":          true,
	"upgrade":             true,
	"chatgpt-account-id":  true,
}

var reservedGatewayOAuthHeaderPrefixes = []string{"x-codex-", "x-openai-", "sec-websocket-"}

// Validate mirrors Rust's GatewayOAuthConfig::validate (#46482): the config is
// rejected when combined with AWS authentication, when an endpoint is not HTTPS
// (loopback HTTP aside) or carries userinfo or a fragment, when the delivery
// header/scheme is invalid or reserved, or when the delivery collides with a
// configured provider header. Error text never echoes the offending URL.
func (c *GatewayOAuthConfig) Validate(provider *ProviderInfo) error {
	if c == nil {
		return nil
	}
	if provider == nil {
		return errors.New("gateway_oauth requires a provider")
	}
	if provider.AWS != nil || provider.IsAmazonBedrock() {
		return errors.New("provider gateway_oauth cannot be combined with AWS authentication")
	}
	if err := validateGatewayOAuthURL(c.AuthorizationURL, "gateway_oauth.authorization_url"); err != nil {
		return err
	}
	if err := validateGatewayOAuthURL(c.TokenURL, "gateway_oauth.token_url"); err != nil {
		return err
	}
	if strings.TrimSpace(provider.BaseURL) == "" {
		return errors.New("gateway_oauth requires base_url")
	}
	if err := validateGatewayOAuthURL(provider.BaseURL, "base_url"); err != nil {
		return err
	}
	if strings.TrimSpace(c.ClientID) == "" || (c.RedirectPort != nil && *c.RedirectPort == 0) {
		return errors.New("gateway_oauth requires a nonempty client_id and a nonzero redirect_port")
	}
	header := ""
	switch c.Delivery.Kind {
	case "header":
		if !isGatewayOAuthHeaderName(c.Delivery.Name) {
			return errors.New("invalid gateway_oauth header name")
		}
		scheme := c.Delivery.HeaderScheme()
		lowered := strings.ToLower(c.Delivery.Name)
		if !isGatewayOAuthToken(scheme) || reservedGatewayOAuthHeaders[lowered] ||
			hasGatewayOAuthReservedPrefix(lowered) {
			return errors.New("invalid or reserved gateway_oauth delivery header or scheme")
		}
		header = lowered
	case "cookie":
		if !isGatewayOAuthToken(c.Delivery.Name) {
			return errors.New("invalid gateway_oauth cookie name")
		}
		header = "cookie"
	default:
		return errors.New("invalid gateway_oauth delivery kind")
	}
	for name := range provider.HTTPHeaders {
		if strings.EqualFold(name, header) {
			return errors.New("gateway_oauth delivery conflicts with a configured provider header")
		}
	}
	for name := range provider.EnvHTTPHeaders {
		if strings.EqualFold(name, header) {
			return errors.New("gateway_oauth delivery conflicts with a configured provider header")
		}
	}
	return nil
}

func hasGatewayOAuthReservedPrefix(name string) bool {
	for _, prefix := range reservedGatewayOAuthHeaderPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func isGatewayOAuthHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for index := 0; index < len(name); index++ {
		ch := name[index]
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9':
		case strings.ContainsRune("!#$%&'*+-.^_`|~", rune(ch)):
		default:
			return false
		}
	}
	return true
}

func isGatewayOAuthToken(value string) bool {
	if value == "" {
		return false
	}
	for index := 0; index < len(value); index++ {
		ch := value[index]
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9':
		case strings.ContainsRune("!#$%&'*+-.^_`|~", rune(ch)):
		default:
			return false
		}
	}
	return true
}

// validateGatewayOAuthURL mirrors Rust's validate_url: HTTPS, or HTTP for a
// loopback host, without userinfo or a fragment.
func validateGatewayOAuthURL(value string, field string) error {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("invalid %s URL", field)
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("%s must use HTTPS (or loopback HTTP), without userinfo or a fragment", field)
	}
	switch parsed.Scheme {
	case "https":
		return nil
	case "http":
		if gatewayOAuthHostIsLoopback(parsed.Hostname()) {
			return nil
		}
	}
	return fmt.Errorf("%s must use HTTPS (or loopback HTTP), without userinfo or a fragment", field)
}

func gatewayOAuthHostIsLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

type ProviderInfo struct {
	Name    string `json:"name,omitempty"`
	BaseURL string `json:"base_url,omitempty"`
	// ModelCatalogURL is an optional full URL for a Codex-native model catalog
	// (Rust `model_catalog_url`). When unset, OpenAI discovery uses the Codex
	// backend unless BaseURL overrides the inference endpoint.
	ModelCatalogURL         string            `json:"model_catalog_url,omitempty"`
	EnvKey                  string            `json:"env_key,omitempty"`
	EnvKeyInstructions      string            `json:"env_key_instructions,omitempty"`
	ExperimentalBearerToken string            `json:"experimental_bearer_token,omitempty"`
	Auth                    *ProviderAuthInfo `json:"auth,omitempty"`
	// GatewayOAuth carries secondary OAuth credentials required by the
	// provider's gateway (Rust #46482).
	GatewayOAuth                *GatewayOAuthConfig  `json:"gateway_oauth,omitempty"`
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
	if p.GatewayOAuth != nil {
		if err := p.GatewayOAuth.Validate(p); err != nil {
			return err
		}
	}
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
		if export := p.AWS.CredentialExport; export != nil {
			if err := p.AWS.ValidateCredentialExport(); err != nil {
				return err
			}
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

// ApplyManagedResidency mirrors Rust's `enforce_managed_residency`: a managed
// residency requirement adds the internal residency header to every request the
// provider makes (and, because the header map is digested, to the model-catalog
// identity). An empty requirement leaves the provider untouched.
func (p *APIProvider) ApplyManagedResidency(residency string) {
	if p == nil {
		return
	}
	residency = strings.TrimSpace(residency)
	if residency == "" {
		return
	}
	if p.Headers == nil {
		p.Headers = http.Header{}
	}
	p.Headers.Set(ResidencyHeaderName, residency)
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

// SupportsCodexBackendRoutes reports whether this OpenAI provider may route
// inference through the dedicated Codex backend endpoints (/guardian,
// /guardian-classifier). Mirrors Rust ModelProviderInfo::supports_codex_backend_routes
// (#40892): true when no base_url is set (the default OpenAI backend) or when
// the base URL ends with "/backend-api/codex".
func (p *ProviderInfo) SupportsCodexBackendRoutes() bool {
	if p == nil || !p.IsOpenAI() {
		return false
	}
	baseURL := strings.TrimRight(strings.TrimSpace(p.BaseURL), "/")
	if baseURL == "" {
		return true
	}
	return strings.HasSuffix(baseURL, "/backend-api/codex")
}

func (p *ProviderInfo) IsAmazonBedrock() bool {
	if p == nil {
		return false
	}
	name := strings.TrimSpace(p.Name)
	return strings.EqualFold(name, AmazonBedrockProviderName) ||
		strings.EqualFold(name, AmazonBedrockRuntimeProviderName) ||
		strings.EqualFold(name, AmazonBedrockProviderID) ||
		strings.EqualFold(name, AmazonBedrockRuntimeProviderID)
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
	return p.IsOpenAI() || IsAzureResponsesProvider(p.Name, p.BaseURL) || p.IsAmazonBedrock()
}

func (p *ProviderInfo) HasCommandAuth() bool {
	return p.Auth != nil
}

// HasProviderAPIKey reports whether explicit provider configuration supplies
// API-key authentication (Rust `has_provider_api_key`). This takes precedence
// over any unrelated first-party login used by the model picker.
func (p *ProviderInfo) HasProviderAPIKey() bool {
	if p == nil {
		return false
	}
	return strings.TrimSpace(p.EnvKey) != "" || strings.TrimSpace(p.ExperimentalBearerToken) != ""
}

// SupportsAPIKeyModels reports whether the provider serves an authoritative
// model catalog for OpenAI API keys (Rust
// `ModelsEndpointClient::supports_api_key_models`). An explicit model catalog
// URL always qualifies; the default OpenAI provider qualifies only while no
// custom base URL overrides the inference endpoint.
func (p *ProviderInfo) SupportsAPIKeyModels() bool {
	if p == nil {
		return false
	}
	if strings.TrimSpace(p.ModelCatalogURL) != "" {
		return true
	}
	return p.IsOpenAI() && strings.TrimSpace(p.BaseURL) == ""
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

func cloneProviderAWSAuthInfoConfig(info *ProviderAWSAuthInfo) *ProviderAWSAuthInfo {
	if info == nil {
		return nil
	}
	clone := *info
	if info.CredentialExport != nil {
		export := *info.CredentialExport
		export.Args = append([]string(nil), info.CredentialExport.Args...)
		clone.CredentialExport = &export
	}
	if info.AuthRefresh != nil {
		refresh := *info.AuthRefresh
		refresh.Args = append([]string(nil), info.AuthRefresh.Args...)
		clone.AuthRefresh = &refresh
	}
	return &clone
}

// providerCommandIsAbsoluteOrBare mirrors Rust's AwsCredentialExport command
// check (#44028): the executable must be an absolute path or a single bare
// name, so relative paths and directory-qualified names are rejected.
func providerCommandIsAbsoluteOrBare(command string) bool {
	if filepath.IsAbs(command) {
		return true
	}
	return command != "" && filepath.Base(command) == command && !strings.ContainsAny(command, `/\`)
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

// CreateAmazonBedrockRuntimeProvider mirrors Rust
// create_amazon_bedrock_runtime_provider (model-provider-info/src/lib.rs): the
// runtime variant shares the Bedrock setup but drops the Mantle client header.
func CreateAmazonBedrockRuntimeProvider(aws *ProviderAWSAuthInfo) ProviderInfo {
	provider := CreateAmazonBedrockProvider(aws)
	provider.Name = AmazonBedrockRuntimeProviderName
	provider.HTTPHeaders = nil
	return provider
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
		OpenAIProviderID:               CreateOpenAIProvider(openAIBaseURL),
		AmazonBedrockProviderID:        CreateAmazonBedrockProvider(nil),
		AmazonBedrockRuntimeProviderID: CreateAmazonBedrockRuntimeProvider(nil),
		OllamaOSSProviderID:            CreateOSSProvider(DefaultOllamaPort, WireAPIResponses),
		LMStudioOSSProviderID:          CreateOSSProvider(DefaultLMStudioPort, WireAPIResponses),
	}
}

func MergeConfiguredProviders(providers map[string]ProviderInfo, configured map[string]ProviderInfo) (map[string]ProviderInfo, error) {
	out := cloneProviderMap(providers)
	for key, provider := range configured {
		if isBedrockProviderID(key) {
			baseURLOverride := provider.BaseURL
			provider.BaseURL = ""
			authOverride := provider.Auth
			provider.Auth = nil
			awsOverride := provider.AWS
			provider.AWS = nil
			httpHeadersOverride := provider.HTTPHeaders
			provider.HTTPHeaders = nil
			if !(&provider).isZero() {
				return nil, fmt.Errorf("model_providers.%s only supports changing `base_url`, `auth`, `http_headers`, `aws.profile`, `aws.region`, `aws.credential_export`, and `aws.auth_refresh`; other non-default provider fields are not supported", key)
			}
			builtIn := out[key]
			builtIn.BaseURL = baseURLOverride
			builtIn.Auth = cloneProviderAuthInfo(authOverride)
			if awsOverride != nil {
				builtIn.AWS = cloneProviderAWSAuthInfoConfig(awsOverride)
			}
			if len(httpHeadersOverride) > 0 {
				if builtIn.HTTPHeaders == nil {
					builtIn.HTTPHeaders = map[string]string{}
				}
				for name, value := range httpHeadersOverride {
					builtIn.HTTPHeaders[name] = value
				}
			}
			out[key] = builtIn
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
		p.ModelCatalogURL == "" &&
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
