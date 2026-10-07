package model

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"codex_go/auth"
)

const (
	DefaultApprovalReviewPreferredModel      = "codex-auto-review"
	APIKeyApprovalReviewPreferredModel       = "gpt-5.6-luna"
	DefaultMemoryExtractionPreferredModel    = "gpt-5.4-mini"
	DefaultMemoryConsolidationPreferredModel = "gpt-5.4"
)

type ProviderCapabilities struct {
	ImageGeneration bool
	WebSearch       bool
	// ExternalWebAccess reports whether hosted web search may access the live
	// web (Rust ProviderCapabilities::external_web_access, #50459).
	ExternalWebAccess bool
	// RemoteCompaction is the provider's remote context-compaction protocol.
	RemoteCompaction RemoteCompactionSupport
}

func DefaultProviderCapabilities() ProviderCapabilities {
	return ProviderCapabilities{
		ImageGeneration:   true,
		WebSearch:         true,
		ExternalWebAccess: true,
		RemoteCompaction:  RemoteCompactionUnsupported,
	}
}

// authUsesAPIKey reports whether the resolved auth is an OpenAI API key, which
// can opt into model discovery (Rust #44392).
func authUsesAPIKey(snapshot *auth.AuthDotJSON) bool {
	if snapshot == nil {
		return false
	}
	return snapshot.Mode() == "api-key"
}

// AuthUsesAPIKey reports whether a resolved auth authenticates with an OpenAI
// API key (Rust CodexAuth::is_api_key_auth), so callers outside this package can
// classify the credential for the remote-model fetch metric (#46570).
func AuthUsesAPIKey(snapshot *auth.AuthDotJSON) bool {
	return authUsesAPIKey(snapshot)
}

type ProviderAccount struct {
	Type             string
	Email            string
	PlanType         string
	CredentialSource string
}

type ProviderAccountState struct {
	Account            *ProviderAccount
	RequiresOpenAIAuth bool
}

type RuntimeProvider interface {
	Info() ProviderInfo
	Capabilities() ProviderCapabilities
	ApprovalReviewPreferredModel() string
	MemoryExtractionPreferredModel() string
	MemoryConsolidationPreferredModel() string
	SupportsAttestation() bool
	AccountState() (ProviderAccountState, error)
	APIProvider() (APIProvider, error)
	RuntimeBaseURL() (string, error)
	APIAuth() (AuthHeaders, error)
	// GatewayAuthManager returns the provider's gateway credential manager, or
	// nil when the provider has no gateway OAuth configuration. Hosts use it to
	// run an explicit login; configured setup failures stay errors (Rust #46490).
	GatewayAuthManager() (*auth.GatewayAuthManager, error)
	ModelsManager(configCatalog *ModelsResponse) ModelsManager
}

func CreateRuntimeProvider(info ProviderInfo, snapshot *auth.AuthDotJSON) RuntimeProvider {
	return CreateRuntimeProviderForID("", info, snapshot)
}

func CreateRuntimeProviderForID(providerID string, info ProviderInfo, snapshot *auth.AuthDotJSON) RuntimeProvider {
	if (&info).IsAmazonBedrock() {
		return &AmazonBedrockProvider{info: info, auth: snapshot}
	}
	configured := &ConfiguredProvider{providerID: providerID, info: info, auth: snapshot}
	if info.GatewayOAuth != nil {
		// Rust constructs the gateway credential manager eagerly and reports
		// setup failures when auth is requested, because the factory is
		// infallible.
		if err := info.Validate(); err != nil {
			configured.gatewayAuthErr = err
		} else if manager, err := sharedGatewayAuthManager(info.GatewayOAuth, auth.DefaultCodexHome()); err != nil {
			configured.gatewayAuthErr = errors.New("failed to create provider OAuth HTTP client")
		} else {
			configured.gatewayAuthManager = manager
		}
	}
	return configured
}

// CreateRuntimeProviderWithResidency builds a provider with the managed
// residency requirement already resolved (Rust sets the process-wide
// `enforce_residency` from config; Go threads the resolved value through).
func CreateRuntimeProviderWithResidency(providerID string, info ProviderInfo, snapshot *auth.AuthDotJSON, residency string) RuntimeProvider {
	provider := CreateRuntimeProviderForID(providerID, info, snapshot)
	if configured, ok := provider.(*ConfiguredProvider); ok {
		configured.SetManagedResidency(residency)
	}
	return provider
}

type ConfiguredProvider struct {
	providerID string
	info       ProviderInfo
	auth       *auth.AuthDotJSON
	// residency is the managed `enforce_residency` requirement applied to this
	// provider's requests and catalog identity (Rust Config::enforce_residency).
	residency string
	// gatewayAuthManager is the shared gateway credential manager, eagerly
	// constructed when the provider configures `gateway_oauth` (Rust #46490).
	gatewayAuthManager *auth.GatewayAuthManager
	// gatewayAuthErr reports why the gateway manager could not be built; it is
	// surfaced when authentication is requested.
	gatewayAuthErr error
}

// SetManagedResidency records the managed residency requirement for this
// provider (Rust's process-wide `enforce_residency`, applied per config here).
func (p *ConfiguredProvider) SetManagedResidency(residency string) {
	if p == nil {
		return
	}
	p.residency = strings.TrimSpace(residency)
}

func (p *ConfiguredProvider) Info() ProviderInfo {
	return p.info
}

func (p *ConfiguredProvider) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{
		ImageGeneration:   true,
		WebSearch:         true,
		ExternalWebAccess: p.info.ExternalWebAccess(),
		RemoteCompaction:  p.info.RemoteCompactionSupport(),
	}
}

func (p *ConfiguredProvider) ApprovalReviewPreferredModel() string {
	// Rust c4f42d161a (#37103): API-key authenticated Guardian reviews use
	// Luna, while ChatGPT-authenticated reviews keep codex-auto-review.
	if p.auth != nil && p.auth.Mode() == "api-key" {
		return APIKeyApprovalReviewPreferredModel
	}
	return DefaultApprovalReviewPreferredModel
}

func (p *ConfiguredProvider) MemoryExtractionPreferredModel() string {
	return DefaultMemoryExtractionPreferredModel
}

func (p *ConfiguredProvider) MemoryConsolidationPreferredModel() string {
	return DefaultMemoryConsolidationPreferredModel
}

func (p *ConfiguredProvider) SupportsAttestation() bool {
	return p.auth != nil && (p.auth.BackendMode() == "chatgpt" || p.auth.Mode() == "agent-identity")
}

func (p *ConfiguredProvider) AccountState() (ProviderAccountState, error) {
	state := ProviderAccountState{RequiresOpenAIAuth: p.info.RequiresOpenAIAuth}
	if !p.info.RequiresOpenAIAuth || p.auth == nil {
		return state, nil
	}
	switch p.auth.Mode() {
	case "api-key":
		state.Account = &ProviderAccount{Type: "api-key"}
	case "bedrock-api-key":
		return ProviderAccountState{}, fmt.Errorf(BedrockAPIKeyUnsupportedMessage)
	case "chatgpt", "chatgptAuthTokens":
		account := auth.AccountFromAuth(p.auth)
		email := stringFromAny(p.auth.Tokens, "email")
		plan := stringFromAny(p.auth.Tokens, "plan_type")
		if account != nil {
			if account.Email != nil {
				email = *account.Email
			}
			if account.PlanType != "" && account.PlanType != auth.PlanUnknown {
				plan = string(account.PlanType)
			}
		}
		if strings.TrimSpace(plan) == "" {
			plan = string(auth.PlanUnknown)
		}
		state.Account = &ProviderAccount{
			Type:     "chatgpt",
			Email:    email,
			PlanType: plan,
		}
	case "personal-access-token", "agent-identity":
		account := auth.AccountFromAuth(p.auth)
		if account != nil {
			state.Account = &ProviderAccount{
				Type:     "chatgpt",
				PlanType: string(account.PlanType),
			}
			if account.Email != nil {
				state.Account.Email = *account.Email
			}
		} else {
			state.Account = &ProviderAccount{Type: p.auth.Mode()}
		}
	}
	return state, nil
}

func (p *ConfiguredProvider) APIProvider() (APIProvider, error) {
	authMode := ""
	if p.auth != nil {
		authMode = p.auth.BackendMode()
	}
	apiProvider, err := p.info.ToAPIProvider(authMode)
	if err != nil {
		return APIProvider{}, err
	}
	apiProvider.ApplyManagedResidency(p.residency)
	return apiProvider, nil
}

func (p *ConfiguredProvider) RuntimeBaseURL() (string, error) {
	apiProvider, err := p.APIProvider()
	if err != nil {
		return "", err
	}
	return apiProvider.BaseURL, nil
}

func (p *ConfiguredProvider) APIAuth() (AuthHeaders, error) {
	primary, err := ResolveProviderAuth(p.auth, p.info)
	if err != nil {
		return AuthHeaders{}, err
	}
	if p.info.GatewayOAuth == nil {
		return primary, nil
	}
	if err := p.info.Validate(); err != nil {
		return AuthHeaders{}, err
	}
	if p.gatewayAuthErr != nil {
		return AuthHeaders{}, p.gatewayAuthErr
	}
	// Model discovery uses this same composed auth, so gateway tokens refresh
	// once for inference and discovery alike.
	return composeGatewayAuth(context.Background(), p.info.GatewayOAuth, p.gatewayAuthManager, primary)
}

func (p *ConfiguredProvider) GatewayAuthManager() (*auth.GatewayAuthManager, error) {
	if p == nil || p.info.GatewayOAuth == nil {
		return nil, nil
	}
	if p.gatewayAuthErr != nil {
		return nil, p.gatewayAuthErr
	}
	return p.gatewayAuthManager, nil
}

func (p *ConfiguredProvider) ModelsManager(configCatalog *ModelsResponse) ModelsManager {
	if configCatalog != nil {
		return NewStaticModelsManager(*configCatalog)
	}
	if IsOSSProviderID(p.providerID) {
		return NewStaticModelsManager(OSSModelCatalog(p.providerID))
	}
	apiProvider, err := p.APIProvider()
	if err != nil {
		return NewStaticModelsManager(BundledModelsResponse())
	}
	authHeaders, err := p.APIAuth()
	if err != nil {
		return NewStaticModelsManager(BundledModelsResponse())
	}
	supportsAPIKeyModels := p.info.SupportsAPIKeyModels()
	// Rust `uses_api_key_auth`: an explicit provider key also opts the session
	// into API-key discovery, even when the picker itself uses ChatGPT (#46561).
	usesAPIKeyAuth := p.info.HasProviderAPIKey() || authUsesAPIKey(p.auth)
	if supportsAPIKeyModels && usesAPIKeyAuth && strings.TrimSpace(p.info.ModelCatalogURL) == "" && strings.TrimSpace(p.info.BaseURL) == "" {
		// Codex model metadata is served by the Codex backend, not the public
		// /v1/models API (Rust #44392). Inference keeps its own base URL.
		apiProvider.BaseURL = ChatGPTCodexBaseURL
	}
	endpoint := NewHTTPModelsEndpoint(&apiProvider, &authHeaders, nil)
	endpoint.CatalogURL = strings.TrimSpace(p.info.ModelCatalogURL)
	return NewRemoteModelsManagerWithOptions(&RemoteModelsManagerOptions{
		Endpoint:                        endpoint,
		UseRemoteCatalogAsSourceOfTruth: authHasChatGPTAccount(p.auth),
		Identity:                        ModelsCatalogIdentity(&p.info, p.auth, &authHeaders, p.residency),
		SupportsAPIKeyModels:            supportsAPIKeyModels,
		APIKeyAuth:                      usesAPIKeyAuth,
		HasAuth:                         p.auth != nil,
		CommandAuth:                     p.info.HasCommandAuth(),
	})
}

type AmazonBedrockProvider struct {
	info ProviderInfo
	auth *auth.AuthDotJSON
}

var amazonBedrockMantleSupportedRegions = map[string]struct{}{
	"us-east-2":      {},
	"us-east-1":      {},
	"us-west-2":      {},
	"ap-southeast-3": {},
	"ap-south-1":     {},
	"ap-northeast-1": {},
	"eu-central-1":   {},
	"eu-west-1":      {},
	"eu-west-2":      {},
	"eu-south-1":     {},
	"eu-north-1":     {},
	"sa-east-1":      {},
}

// The bedrockEndpoint selector (catalog.go) decides whether the Amazon Bedrock
// provider targets the Mantle front door or the regional Bedrock Runtime
// endpoint; the URL and SigV4 service below hang off it (#38470 d5e256ceb2).

// bedrockEndpointForProviderName maps an Amazon Bedrock provider name onto the
// endpoint it targets, mirroring Rust AmazonBedrockModelProvider::new's
// selection through ModelProviderInfo::is_amazon_bedrock_runtime (d5e256ceb2).
func bedrockEndpointForProviderName(name string) bedrockEndpoint {
	if info := (ProviderInfo{Name: name}); info.IsAmazonBedrockRuntime() {
		return bedrockEndpointRuntime
	}
	return bedrockEndpointMantle
}

// bedrockServiceNameForEndpoint mirrors Rust mantle::aws_auth_config /
// runtime::aws_auth_config service selection (d5e256ceb2).
func bedrockServiceNameForEndpoint(endpoint bedrockEndpoint) string {
	if endpoint == bedrockEndpointRuntime {
		return AmazonBedrockRuntimeServiceName
	}
	return AmazonBedrockMantleServiceName
}

// endpoint mirrors Rust AmazonBedrockModelProvider::new's endpoint selection
// (d5e256ceb2): the provider is the Runtime variant exactly when its info is
// the `amazon-bedrock-runtime` provider, otherwise it is Mantle.
func (p *AmazonBedrockProvider) endpoint() bedrockEndpoint {
	return bedrockEndpointForProviderName(p.info.Name)
}

// bedrockBaseURLForRegion mirrors Rust runtime::base_url / mantle::base_url
// (d5e256ceb2): the Runtime endpoint is
// `https://bedrock-runtime.{region}.amazonaws.com/openai/v1` and, unlike the
// Mantle front door, has no supported-region allowlist; Mantle keeps
// `https://bedrock-mantle.{region}.api.aws/openai/v1` plus its region check.
func (p *AmazonBedrockProvider) bedrockBaseURLForRegion(region string) (string, error) {
	if p.endpoint() == bedrockEndpointRuntime {
		return amazonBedrockRuntimeBaseURL(region), nil
	}
	return amazonBedrockMantleBaseURL(region)
}

func (p *AmazonBedrockProvider) Info() ProviderInfo {
	return p.info
}

func (p *AmazonBedrockProvider) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{
		ImageGeneration: false,
		// Rust AmazonBedrockModelProvider::capabilities: web search is available
		// on the Mantle endpoint and unsupported on the Bedrock Runtime endpoint;
		// live web access is disallowed and remote compaction v2 is supported.
		WebSearch:         p.info.Name == AmazonBedrockProviderName,
		ExternalWebAccess: false,
		RemoteCompaction:  RemoteCompactionV2,
	}
}

// The three preferred-model accessors mirror Rust
// AmazonBedrockModelProvider::{approval_review_preferred_model,
// memory_extraction_preferred_model, memory_consolidation_preferred_model}
// (codex-rs/model-provider/src/amazon_bedrock/mod.rs, #38470): the Mantle
// endpoint prefers the GPT-5.6 Luna/Terra slugs while the Bedrock Runtime
// endpoint prefers their `global.` cross-region variants.
func (p *AmazonBedrockProvider) ApprovalReviewPreferredModel() string {
	if p.info.IsAmazonBedrockRuntime() {
		return AmazonBedrockRuntimeGlobalGPT56LunaModelID
	}
	return AmazonBedrockGPT56LunaModelID
}

func (p *AmazonBedrockProvider) MemoryExtractionPreferredModel() string {
	if p.info.IsAmazonBedrockRuntime() {
		return AmazonBedrockRuntimeGlobalGPT56LunaModelID
	}
	return AmazonBedrockGPT56LunaModelID
}

func (p *AmazonBedrockProvider) MemoryConsolidationPreferredModel() string {
	if p.info.IsAmazonBedrockRuntime() {
		return AmazonBedrockRuntimeGlobalGPT56TerraModelID
	}
	return AmazonBedrockGPT56TerraModelID
}

func (p *AmazonBedrockProvider) SupportsAttestation() bool {
	return false
}

// GatewayAuthManager: gateway OAuth cannot be combined with AWS authentication,
// so the Bedrock provider never exposes one.
func (p *AmazonBedrockProvider) GatewayAuthManager() (*auth.GatewayAuthManager, error) {
	return nil, nil
}

func (p *AmazonBedrockProvider) AccountState() (ProviderAccountState, error) {
	source := "aws-managed"
	if p.auth != nil && p.auth.Mode() == "bedrock-api-key" {
		source = "codex-managed"
	}
	return ProviderAccountState{
		Account:            &ProviderAccount{Type: "amazon-bedrock", CredentialSource: source},
		RequiresOpenAIAuth: false,
	}, nil
}

func (p *AmazonBedrockProvider) APIProvider() (APIProvider, error) {
	info := p.info
	if info.BaseURL == "" {
		baseURL, err := p.RuntimeBaseURL()
		if err != nil {
			return APIProvider{}, err
		}
		info.BaseURL = baseURL
	}
	return info.ToAPIProvider("")
}

func (p *AmazonBedrockProvider) RuntimeBaseURL() (string, error) {
	region, err := p.resolveRegion()
	if err != nil {
		return "", err
	}
	return p.bedrockBaseURLForRegion(region)
}

func (p *AmazonBedrockProvider) RuntimeBaseURLNoError() string {
	region, err := p.resolveRegion()
	if err != nil || strings.TrimSpace(region) == "" {
		region = "us-east-1"
	}
	baseURL, err := p.bedrockBaseURLForRegion(region)
	if err != nil {
		baseURL, _ = p.bedrockBaseURLForRegion("us-east-1")
	}
	return baseURL
}

func (p *AmazonBedrockProvider) APIAuth() (AuthHeaders, error) {
	headers := p.info.BuildHeaderMap()
	if p.info.Auth != nil {
		resolved, err := ResolveProviderCommandAuth(context.Background(), p.info.Auth)
		if err != nil {
			return AuthHeaders{}, err
		}
		for name, values := range resolved.Headers {
			for _, value := range values {
				headers.Add(name, value)
			}
		}
		return AuthHeaders{Headers: headers}, nil
	}
	if p.auth != nil && p.auth.Mode() == "bedrock-api-key" {
		if token := bedrockAPIKeyValue(p.auth.BedrockAPIKey); token != "" {
			headers.Set("Authorization", "Bearer "+token)
		}
		return AuthHeaders{Headers: headers}, nil
	}
	if token := strings.TrimSpace(os.Getenv(AmazonBedrockBearerTokenEnv)); token != "" {
		headers.Set("Authorization", "Bearer "+token)
		region, err := p.resolveRegion()
		if err != nil {
			return AuthHeaders{}, fmt.Errorf("Amazon Bedrock bearer token auth requires model_providers.amazon-bedrock.aws.region, AWS_REGION, or AWS_DEFAULT_REGION")
		}
		if _, err := p.bedrockBaseURLForRegion(region); err != nil {
			return AuthHeaders{}, err
		}
		return AuthHeaders{Headers: headers}, nil
	}
	awsConfig := p.awsAuthConfig()
	var awsContext *auth.AWSAuthContext
	var err error
	if config, ok := p.credentialExportConfig(); ok {
		awsContext, err = auth.LoadAWSAuthContextWithProviderAndOptions(awsConfig, credentialExportProviderForConfig(config), awsAuthLoadOptions())
	} else {
		awsContext, err = auth.LoadAWSAuthContextWithOptions(awsConfig, awsAuthLoadOptions())
	}
	if err != nil {
		return AuthHeaders{}, fmt.Errorf("failed to resolve Amazon Bedrock auth: %w", err)
	}
	return AuthHeaders{
		Headers: headers,
		SignRequest: func(_ context.Context, request *http.Request, body []byte) (*SignedRequest, error) {
			return signBedrockRequest(awsContext, request, body, p.endpoint())
		},
	}, nil
}

func (p *AmazonBedrockProvider) ModelsManager(configCatalog *ModelsResponse) ModelsManager {
	if configCatalog != nil {
		catalog := WithDefaultOnlyServiceTier(*configCatalog)
		return NewStaticModelsManager(catalog)
	}
	// Rust AmazonBedrockModelProvider::default_model_catalog (#38470): the
	// Bedrock Runtime endpoint serves its own cross-region catalog, not the
	// Mantle slugs.
	if p.info.IsAmazonBedrockRuntime() {
		return NewStaticModelsManager(AmazonBedrockRuntimeModelCatalog())
	}
	return NewStaticModelsManager(AmazonBedrockModelCatalog())
}

func stringFromAny(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, _ := values[key].(string)
	return value
}

func bedrockAPIKeyValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case map[string]string:
		return strings.TrimSpace(typed["api_key"])
	case map[string]any:
		if token, ok := typed["api_key"].(string); ok {
			return strings.TrimSpace(token)
		}
	}
	return ""
}

func bedrockAPIKeyRegion(value any) string {
	switch typed := value.(type) {
	case map[string]string:
		return strings.TrimSpace(typed["region"])
	case map[string]any:
		if region, ok := typed["region"].(string); ok {
			return strings.TrimSpace(region)
		}
	}
	return ""
}

func (p *AmazonBedrockProvider) resolveRegion() (string, error) {
	if p.auth != nil && p.auth.Mode() == "bedrock-api-key" {
		if region := bedrockAPIKeyRegion(p.auth.BedrockAPIKey); region != "" {
			return region, nil
		}
	}
	region, err := auth.ResolveAWSRegionWithOptions(p.awsAuthConfig(), awsAuthLoadOptions())
	if err != nil {
		return "", err
	}
	return region, nil
}

// awsAuthConfig mirrors Rust mantle::aws_auth_config / runtime::aws_auth_config
// (d5e256ceb2): the SigV4 service is endpoint-specific, `bedrock-mantle` for the
// Mantle front door and `bedrock` for the regional Bedrock Runtime endpoint.
func (p *AmazonBedrockProvider) awsAuthConfig() *auth.AWSAuthConfig {
	config := &auth.AWSAuthConfig{Service: bedrockServiceNameForEndpoint(p.endpoint())}
	if p != nil && p.info.AWS != nil {
		config.Profile = strings.TrimSpace(p.info.AWS.Profile)
		config.Region = strings.TrimSpace(p.info.AWS.Region)
	}
	return config
}

// credentialExportConfig returns the configured Bedrock credential-export
// command, if any (Rust #44028).
func (p *AmazonBedrockProvider) credentialExportConfig() (ProviderCredentialExportInfo, bool) {
	return bedrockCredentialExportConfig(p.info.AWS)
}

func bedrockCredentialExportConfig(info *ProviderAWSAuthInfo) (ProviderCredentialExportInfo, bool) {
	if info == nil || info.CredentialExport == nil {
		return ProviderCredentialExportInfo{}, false
	}
	return *info.CredentialExport, true
}

func amazonBedrockMantleBaseURL(region string) (string, error) {
	region = strings.TrimSpace(region)
	if _, ok := amazonBedrockMantleSupportedRegions[region]; !ok {
		return "", fmt.Errorf("Amazon Bedrock Mantle does not support region `%s`", region)
	}
	return fmt.Sprintf("https://bedrock-mantle.%s.api.aws/openai/v1", region), nil
}

// amazonBedrockRuntimeBaseURL mirrors Rust runtime::base_url (d5e256ceb2):
// the regional `bedrock-runtime` OpenAI-compatible endpoint. Unlike the Mantle
// front door this endpoint has no supported-region allowlist upstream, so no
// region check is applied here.
func amazonBedrockRuntimeBaseURL(region string) string {
	return fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com/openai/v1", strings.TrimSpace(region))
}

// signBedrockRequest signs an Amazon Bedrock OpenAI-compatible request with
// SigV4. Rust BedrockSigV4AuthProvider::apply_auth (d5e256ceb2) only strips the
// snake_case compatibility headers for the Mantle endpoint, because the Mantle
// front door does not preserve them before SigV4 verification; the Bedrock
// Runtime endpoint keeps them.
func signBedrockRequest(context *auth.AWSAuthContext, request *http.Request, body []byte, endpoint bedrockEndpoint) (*SignedRequest, error) {
	if request == nil {
		return &SignedRequest{Body: body}, nil
	}
	if endpoint == bedrockEndpointMantle {
		removeHeadersNotPreservedByBedrockMantle(request.Header)
	}
	removeCompressionHeadersForPreparedBedrockBody(request.Header)
	payloadHash := sha256.Sum256(body)
	request.Header.Set("X-Amz-Content-Sha256", fmt.Sprintf("%x", payloadHash[:]))
	signed, err := context.Sign(&auth.AWSAuthRequestToSign{
		Method:  request.Method,
		URL:     request.URL.String(),
		Headers: request.Header,
		Body:    body,
	})
	if err != nil {
		return nil, err
	}
	request.Header = signed.Headers
	if signed.URL != "" {
		if parsed, err := url.Parse(signed.URL); err == nil {
			request.URL = parsed
		}
	}
	if request.URL != nil {
		request.Host = request.URL.Host
	}
	return &SignedRequest{Body: body}, nil
}

func removeHeadersNotPreservedByBedrockMantle(headers http.Header) {
	for key := range headers {
		if strings.Contains(key, "_") {
			headers.Del(key)
		}
	}
}

func removeCompressionHeadersForPreparedBedrockBody(headers http.Header) {
	if headers == nil {
		return
	}
	headers.Del("Content-Encoding")
	headers.Del("Content-Length")
}

func authHasChatGPTAccount(snapshot *auth.AuthDotJSON) bool {
	if snapshot == nil {
		return false
	}
	switch snapshot.Mode() {
	case "chatgpt", "chatgptAuthTokens", "personal-access-token", "agent-identity":
		return true
	default:
		return false
	}
}
