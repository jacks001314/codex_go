package model

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"codex_go/auth"
)

const (
	DefaultApprovalReviewPreferredModel      = "codex-auto-review"
	DefaultMemoryExtractionPreferredModel    = "gpt-5.4-mini"
	DefaultMemoryConsolidationPreferredModel = "gpt-5.4"
)

type ProviderCapabilities struct {
	NamespaceTools  bool
	ImageGeneration bool
	WebSearch       bool
}

func DefaultProviderCapabilities() ProviderCapabilities {
	return ProviderCapabilities{
		NamespaceTools:  true,
		ImageGeneration: true,
		WebSearch:       true,
	}
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
	ModelsManager(configCatalog *ModelsResponse) ModelsManager
}

func CreateRuntimeProvider(info ProviderInfo, snapshot *auth.AuthDotJSON) RuntimeProvider {
	return CreateRuntimeProviderForID("", info, snapshot)
}

func CreateRuntimeProviderForID(providerID string, info ProviderInfo, snapshot *auth.AuthDotJSON) RuntimeProvider {
	if (&info).IsAmazonBedrock() {
		return &AmazonBedrockProvider{info: info, auth: snapshot}
	}
	return &ConfiguredProvider{providerID: providerID, info: info, auth: snapshot}
}

type ConfiguredProvider struct {
	providerID string
	info       ProviderInfo
	auth       *auth.AuthDotJSON
}

func (p *ConfiguredProvider) Info() ProviderInfo {
	return p.info
}

func (p *ConfiguredProvider) Capabilities() ProviderCapabilities {
	return DefaultProviderCapabilities()
}

func (p *ConfiguredProvider) ApprovalReviewPreferredModel() string {
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
	return p.info.ToAPIProvider(authMode)
}

func (p *ConfiguredProvider) RuntimeBaseURL() (string, error) {
	apiProvider, err := p.APIProvider()
	if err != nil {
		return "", err
	}
	return apiProvider.BaseURL, nil
}

func (p *ConfiguredProvider) APIAuth() (AuthHeaders, error) {
	return ResolveProviderAuth(p.auth, p.info)
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
	endpoint := NewHTTPModelsEndpoint(&apiProvider, &authHeaders, nil)
	return NewRemoteModelsManagerWithOptions(&RemoteModelsManagerOptions{
		Endpoint:                        endpoint,
		UseRemoteCatalogAsSourceOfTruth: authHasChatGPTAccount(p.auth),
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

func (p *AmazonBedrockProvider) Info() ProviderInfo {
	return p.info
}

func (p *AmazonBedrockProvider) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{
		NamespaceTools:  true,
		ImageGeneration: false,
		WebSearch:       false,
	}
}

func (p *AmazonBedrockProvider) ApprovalReviewPreferredModel() string {
	return AmazonBedrockGPT54ModelID
}

func (p *AmazonBedrockProvider) MemoryExtractionPreferredModel() string {
	return AmazonBedrockGPT54ModelID
}

func (p *AmazonBedrockProvider) MemoryConsolidationPreferredModel() string {
	return AmazonBedrockGPT54ModelID
}

func (p *AmazonBedrockProvider) SupportsAttestation() bool {
	return false
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
	return amazonBedrockMantleBaseURL(region)
}

func (p *AmazonBedrockProvider) RuntimeBaseURLNoError() string {
	region, err := p.resolveRegion()
	if err != nil || strings.TrimSpace(region) == "" {
		region = "us-east-1"
	}
	baseURL, err := amazonBedrockMantleBaseURL(region)
	if err != nil {
		baseURL, _ = amazonBedrockMantleBaseURL("us-east-1")
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
		if _, err := amazonBedrockMantleBaseURL(region); err != nil {
			return AuthHeaders{}, err
		}
		return AuthHeaders{Headers: headers}, nil
	}
	awsConfig := p.awsAuthConfig()
	awsContext, err := auth.LoadAWSAuthContext(awsConfig)
	if err != nil {
		return AuthHeaders{}, fmt.Errorf("failed to resolve Amazon Bedrock auth: %w", err)
	}
	return AuthHeaders{
		Headers: headers,
		SignRequest: func(_ context.Context, request *http.Request, body []byte) (*SignedRequest, error) {
			return signBedrockMantleRequest(awsContext, request, body)
		},
	}, nil
}

func (p *AmazonBedrockProvider) ModelsManager(configCatalog *ModelsResponse) ModelsManager {
	if configCatalog != nil {
		catalog := WithDefaultOnlyServiceTier(*configCatalog)
		return NewStaticModelsManager(catalog)
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
	region, err := auth.ResolveAWSRegion(p.awsAuthConfig())
	if err != nil {
		return "", err
	}
	return region, nil
}

func (p *AmazonBedrockProvider) awsAuthConfig() *auth.AWSAuthConfig {
	config := &auth.AWSAuthConfig{Service: AmazonBedrockMantleServiceName}
	if p != nil && p.info.AWS != nil {
		config.Profile = strings.TrimSpace(p.info.AWS.Profile)
		config.Region = strings.TrimSpace(p.info.AWS.Region)
	}
	return config
}

func amazonBedrockMantleBaseURL(region string) (string, error) {
	region = strings.TrimSpace(region)
	if _, ok := amazonBedrockMantleSupportedRegions[region]; !ok {
		return "", fmt.Errorf("Amazon Bedrock Mantle does not support region `%s`", region)
	}
	return fmt.Sprintf("https://bedrock-mantle.%s.api.aws/openai/v1", region), nil
}

func signBedrockMantleRequest(context *auth.AWSAuthContext, request *http.Request, body []byte) (*SignedRequest, error) {
	if request == nil {
		return &SignedRequest{Body: body}, nil
	}
	removeHeadersNotPreservedByBedrockMantle(request.Header)
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
