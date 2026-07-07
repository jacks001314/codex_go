package model

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"codex_go/internal/agent"
	"codex_go/internal/auth"
)

func TestCreateRuntimeProviderConfiguredProvider(t *testing.T) {
	provider := CreateRuntimeProvider(CreateOpenAIProvider(""), &auth.AuthDotJSON{
		AuthMode:     "api-key",
		OpenAIAPIKey: "sk-test",
	})
	if provider.Info().Name != OpenAIProviderName {
		t.Fatalf("Info = %#v", provider.Info())
	}
	if provider.ApprovalReviewPreferredModel() != DefaultApprovalReviewPreferredModel {
		t.Fatalf("approval model = %q", provider.ApprovalReviewPreferredModel())
	}
	if !provider.Capabilities().ImageGeneration || !provider.Capabilities().WebSearch {
		t.Fatalf("capabilities = %#v", provider.Capabilities())
	}
}

func TestConfiguredProviderAccountStateRequiresOpenAIAuth(t *testing.T) {
	provider := CreateRuntimeProvider(CreateOpenAIProvider(""), &auth.AuthDotJSON{
		AuthMode:     "api-key",
		OpenAIAPIKey: "sk-test",
	})
	state, err := provider.AccountState()
	if err != nil {
		t.Fatalf("AccountState returned error: %v", err)
	}
	if !state.RequiresOpenAIAuth {
		t.Fatal("RequiresOpenAIAuth = false, want true")
	}
	if state.Account == nil || state.Account.Type != "api-key" {
		t.Fatalf("account = %#v", state.Account)
	}
}

func TestConfiguredProviderChatGPTAccountStateDefaultsUnknownPlan(t *testing.T) {
	provider := CreateRuntimeProvider(CreateOpenAIProvider(""), &auth.AuthDotJSON{
		AuthMode: "chatgpt",
		Tokens:   map[string]any{"access_token": "token"},
	})
	state, err := provider.AccountState()
	if err != nil {
		t.Fatalf("AccountState returned error: %v", err)
	}
	if state.Account == nil || state.Account.Type != "chatgpt" || state.Account.PlanType != string(auth.PlanUnknown) {
		t.Fatalf("account = %#v", state.Account)
	}
}

func TestConfiguredProviderSupportsAttestationForChatGPT(t *testing.T) {
	provider := CreateRuntimeProvider(CreateOpenAIProvider(""), &auth.AuthDotJSON{
		AuthMode: "chatgpt",
		Tokens:   map[string]any{"access_token": "token"},
	})
	if !provider.SupportsAttestation() {
		t.Fatal("SupportsAttestation = false, want true")
	}

	external := CreateRuntimeProvider(CreateOpenAIProvider(""), &auth.AuthDotJSON{
		AuthMode: "chatgptAuthTokens",
		Tokens:   map[string]any{"access_token": "token"},
	})
	if !external.SupportsAttestation() {
		t.Fatal("SupportsAttestation = false for external chatgpt tokens, want true")
	}
}

func TestConfiguredProviderRuntimeBaseURL(t *testing.T) {
	provider := CreateRuntimeProvider(ProviderInfo{
		Name:    "Custom",
		BaseURL: "https://example.com/v1",
	}, nil)
	baseURL, err := provider.RuntimeBaseURL()
	if err != nil {
		t.Fatalf("RuntimeBaseURL returned error: %v", err)
	}
	if baseURL != "https://example.com/v1" {
		t.Fatalf("baseURL = %q", baseURL)
	}
}

func TestConfiguredProviderModelsManagerUsesRemoteByDefault(t *testing.T) {
	provider := CreateRuntimeProvider(ProviderInfo{
		Name:    "Custom",
		BaseURL: "https://example.com/v1",
	}, nil)
	manager := provider.ModelsManager(nil)
	if _, ok := manager.(*RemoteModelsManager); !ok {
		t.Fatalf("manager type = %T, want *RemoteModelsManager", manager)
	}
}

func TestConfiguredProviderModelsManagerUsesChatGPTRemoteCatalogAsSourceOfTruth(t *testing.T) {
	provider := CreateRuntimeProvider(CreateOpenAIProvider(""), &auth.AuthDotJSON{
		AuthMode: "chatgpt",
		Tokens:   map[string]any{"access_token": "token"},
	})
	manager, ok := provider.ModelsManager(nil).(*RemoteModelsManager)
	if !ok {
		t.Fatalf("manager type = %T, want *RemoteModelsManager", provider.ModelsManager(nil))
	}
	if !manager.useRemoteCatalogAsSourceOfTruth {
		t.Fatal("useRemoteCatalogAsSourceOfTruth = false, want true")
	}

	apiKeyProvider := CreateRuntimeProvider(CreateOpenAIProvider(""), &auth.AuthDotJSON{
		AuthMode:     "api-key",
		OpenAIAPIKey: "sk-test",
	})
	apiKeyManager, ok := apiKeyProvider.ModelsManager(nil).(*RemoteModelsManager)
	if !ok {
		t.Fatalf("manager type = %T, want *RemoteModelsManager", apiKeyProvider.ModelsManager(nil))
	}
	if apiKeyManager.useRemoteCatalogAsSourceOfTruth {
		t.Fatal("useRemoteCatalogAsSourceOfTruth = true for API key auth")
	}

	externalProvider := CreateRuntimeProvider(CreateOpenAIProvider(""), &auth.AuthDotJSON{
		AuthMode: "chatgptAuthTokens",
		Tokens:   map[string]any{"access_token": "token"},
	})
	externalManager, ok := externalProvider.ModelsManager(nil).(*RemoteModelsManager)
	if !ok {
		t.Fatalf("manager type = %T, want *RemoteModelsManager", externalProvider.ModelsManager(nil))
	}
	if !externalManager.useRemoteCatalogAsSourceOfTruth {
		t.Fatal("useRemoteCatalogAsSourceOfTruth = false for external chatgpt tokens, want true")
	}

	taskID := "task-1"
	keyMaterial, err := agent.GenerateAgentKeyMaterial()
	if err != nil {
		t.Fatalf("GenerateAgentKeyMaterial() error = %v", err)
	}
	agentIdentityProvider := CreateRuntimeProvider(CreateOpenAIProvider(""), &auth.AuthDotJSON{
		AuthMode: "agent-identity",
		AgentIdentity: &auth.AgentIdentityAuthRecord{
			AgentRuntimeID:        "agent-runtime",
			AgentPrivateKey:       keyMaterial.PrivateKeyPKCS8Base64,
			AccountID:             "account-agent",
			ChatGPTUserID:         "user-agent",
			ChatGPTAccountFedRAMP: true,
			TaskID:                &taskID,
		},
	})
	agentIdentityManager, ok := agentIdentityProvider.ModelsManager(nil).(*RemoteModelsManager)
	if !ok {
		t.Fatalf("manager type = %T, want *RemoteModelsManager", agentIdentityProvider.ModelsManager(nil))
	}
	if !agentIdentityManager.useRemoteCatalogAsSourceOfTruth {
		t.Fatal("useRemoteCatalogAsSourceOfTruth = false for agent identity auth, want true")
	}
}

func TestExternalChatGPTTokensUseChatGPTBaseURLAndAccountState(t *testing.T) {
	snapshot := auth.FromChatGPTAuthTokens("token", "account-1", stringPtrProviderTest("pro"))
	provider := CreateRuntimeProvider(CreateOpenAIProvider(""), &snapshot)
	apiProvider, err := provider.APIProvider()
	if err != nil {
		t.Fatalf("APIProvider returned error: %v", err)
	}
	if apiProvider.BaseURL != ChatGPTCodexBaseURL {
		t.Fatalf("BaseURL = %q, want %q", apiProvider.BaseURL, ChatGPTCodexBaseURL)
	}
	state, err := provider.AccountState()
	if err != nil {
		t.Fatalf("AccountState returned error: %v", err)
	}
	if state.Account == nil || state.Account.Type != "chatgpt" || state.Account.PlanType != "pro" {
		t.Fatalf("account state = %+v", state)
	}
}

func TestAgentIdentityProviderAccountStateUsesChatGPTAccount(t *testing.T) {
	snapshot := auth.AuthDotJSON{
		AuthMode: "agent-identity",
		AgentIdentity: map[string]any{
			"account_id":      "account-1",
			"chatgpt_user_id": "user-1",
			"email":           "agent@example.com",
			"plan_type":       "team",
		},
	}
	provider := CreateRuntimeProvider(CreateOpenAIProvider(""), &snapshot)
	state, err := provider.AccountState()
	if err != nil {
		t.Fatalf("AccountState returned error: %v", err)
	}
	if state.Account == nil || state.Account.Type != "chatgpt" || state.Account.Email != "agent@example.com" || state.Account.PlanType != "team" {
		t.Fatalf("account state = %+v", state)
	}
}

func TestConfiguredProviderModelsManagerUsesConfigCatalog(t *testing.T) {
	provider := CreateRuntimeProvider(ProviderInfo{
		Name:    "Custom",
		BaseURL: "https://example.com/v1",
	}, nil)
	manager := provider.ModelsManager(&ModelsResponse{Models: []ModelInfo{{
		Slug:           "configured",
		DisplayName:    "Configured",
		Visibility:     VisibilityVisible,
		SupportedInAPI: true,
		Priority:       0,
	}}})
	if _, ok := manager.(*StaticModelsManager); !ok {
		t.Fatalf("manager type = %T, want *StaticModelsManager", manager)
	}
	if got := manager.GetDefaultModel("", true, RefreshOffline); got != "configured" {
		t.Fatalf("default model = %q", got)
	}
}

func TestAmazonBedrockProviderCapabilitiesAndModels(t *testing.T) {
	provider := CreateRuntimeProvider(CreateAmazonBedrockProvider(nil), nil)
	capabilities := provider.Capabilities()
	if !capabilities.NamespaceTools || capabilities.ImageGeneration || capabilities.WebSearch {
		t.Fatalf("capabilities = %#v", capabilities)
	}
	if provider.ApprovalReviewPreferredModel() != AmazonBedrockGPT54ModelID {
		t.Fatalf("approval model = %q", provider.ApprovalReviewPreferredModel())
	}
	if provider.MemoryExtractionPreferredModel() != AmazonBedrockGPT54ModelID {
		t.Fatalf("memory extraction model = %q", provider.MemoryExtractionPreferredModel())
	}
	if provider.MemoryConsolidationPreferredModel() != AmazonBedrockGPT54ModelID {
		t.Fatalf("memory consolidation model = %q", provider.MemoryConsolidationPreferredModel())
	}
}

func TestAmazonBedrockProviderRuntimeBaseURLUsesRegion(t *testing.T) {
	info := CreateAmazonBedrockProvider(&ProviderAWSAuthInfo{Region: "eu-central-1"})
	provider := CreateRuntimeProvider(info, nil)
	baseURL, err := provider.RuntimeBaseURL()
	if err != nil {
		t.Fatalf("RuntimeBaseURL returned error: %v", err)
	}
	if baseURL != "https://bedrock-mantle.eu-central-1.api.aws/openai/v1" {
		t.Fatalf("baseURL = %q", baseURL)
	}
}

func TestAmazonBedrockProviderRuntimeBaseURLRejectsUnsupportedRegion(t *testing.T) {
	info := CreateAmazonBedrockProvider(&ProviderAWSAuthInfo{Region: "us-west-1"})
	provider := CreateRuntimeProvider(info, nil)
	_, err := provider.RuntimeBaseURL()
	if err == nil {
		t.Fatal("RuntimeBaseURL returned nil error, want unsupported region failure")
	}
	if !strings.Contains(err.Error(), "Amazon Bedrock Mantle does not support region `us-west-1`") {
		t.Fatalf("error = %v", err)
	}
	_, err = provider.APIProvider()
	if err == nil {
		t.Fatal("APIProvider returned nil error, want unsupported region failure")
	}
}

func TestAmazonBedrockProviderRuntimeBaseURLUsesManagedAPIKeyRegion(t *testing.T) {
	provider := CreateRuntimeProvider(CreateAmazonBedrockProvider(&ProviderAWSAuthInfo{Region: "eu-central-1"}), &auth.AuthDotJSON{
		AuthMode: "bedrock-api-key",
		BedrockAPIKey: map[string]any{
			"api_key": "managed-bedrock-api-key",
			"region":  "ap-northeast-1",
		},
	})
	baseURL, err := provider.RuntimeBaseURL()
	if err != nil {
		t.Fatalf("RuntimeBaseURL returned error: %v", err)
	}
	if baseURL != "https://bedrock-mantle.ap-northeast-1.api.aws/openai/v1" {
		t.Fatalf("baseURL = %q", baseURL)
	}
}

func TestAmazonBedrockProviderRuntimeBaseURLRejectsManagedAPIKeyUnsupportedRegion(t *testing.T) {
	provider := CreateRuntimeProvider(CreateAmazonBedrockProvider(&ProviderAWSAuthInfo{Region: "eu-central-1"}), &auth.AuthDotJSON{
		AuthMode: "bedrock-api-key",
		BedrockAPIKey: map[string]any{
			"api_key": "managed-bedrock-api-key",
			"region":  "us-west-1",
		},
	})
	_, err := provider.RuntimeBaseURL()
	if err == nil {
		t.Fatal("RuntimeBaseURL returned nil error, want unsupported region failure")
	}
	if !strings.Contains(err.Error(), "Amazon Bedrock Mantle does not support region `us-west-1`") {
		t.Fatalf("error = %v", err)
	}
}

func TestAmazonBedrockMantleBaseURL(t *testing.T) {
	baseURL, err := amazonBedrockMantleBaseURL(" ap-northeast-1 ")
	if err != nil {
		t.Fatalf("amazonBedrockMantleBaseURL returned error: %v", err)
	}
	if baseURL != "https://bedrock-mantle.ap-northeast-1.api.aws/openai/v1" {
		t.Fatalf("baseURL = %q", baseURL)
	}
}

func TestAmazonBedrockProviderAccountState(t *testing.T) {
	provider := CreateRuntimeProvider(CreateAmazonBedrockProvider(nil), nil)
	state, err := provider.AccountState()
	if err != nil {
		t.Fatalf("AccountState returned error: %v", err)
	}
	if state.Account == nil || state.Account.Type != "amazon-bedrock" || state.Account.CredentialSource != "aws-managed" {
		t.Fatalf("account = %#v", state.Account)
	}

	provider = CreateRuntimeProvider(CreateAmazonBedrockProvider(nil), &auth.AuthDotJSON{
		AuthMode:      "bedrock-api-key",
		BedrockAPIKey: map[string]string{"api_key": "bedrock"},
	})
	state, err = provider.AccountState()
	if err != nil {
		t.Fatalf("AccountState returned error: %v", err)
	}
	if state.Account == nil || state.Account.CredentialSource != "codex-managed" {
		t.Fatalf("account = %#v", state.Account)
	}
}

func TestAmazonBedrockProviderAPIAuthUsesManagedAPIKey(t *testing.T) {
	provider := CreateRuntimeProvider(CreateAmazonBedrockProvider(nil), &auth.AuthDotJSON{
		AuthMode:      "bedrock-api-key",
		BedrockAPIKey: map[string]string{"api_key": "managed-bedrock-api-key"},
	})
	headers, err := provider.APIAuth()
	if err != nil {
		t.Fatalf("APIAuth returned error: %v", err)
	}
	if headers.Headers.Get("Authorization") != "Bearer managed-bedrock-api-key" {
		t.Fatalf("Authorization = %q", headers.Headers.Get("Authorization"))
	}
	if headers.Headers.Get(AmazonBedrockMantleClientHeader) != AmazonBedrockMantleClientValue {
		t.Fatalf("%s = %q", AmazonBedrockMantleClientHeader, headers.Headers.Get(AmazonBedrockMantleClientHeader))
	}
}

func TestAmazonBedrockProviderAPIAuthUsesBearerTokenEnv(t *testing.T) {
	t.Setenv(AmazonBedrockBearerTokenEnv, "env-bedrock-token")
	t.Setenv("AWS_REGION", "us-east-2")
	provider := CreateRuntimeProvider(CreateAmazonBedrockProvider(nil), nil)
	headers, err := provider.APIAuth()
	if err != nil {
		t.Fatalf("APIAuth returned error: %v", err)
	}
	if headers.Headers.Get("Authorization") != "Bearer env-bedrock-token" {
		t.Fatalf("Authorization = %q", headers.Headers.Get("Authorization"))
	}
}

func TestAmazonBedrockProviderAPIAuthRejectsBearerTokenWithoutRegion(t *testing.T) {
	t.Setenv(AmazonBedrockBearerTokenEnv, "env-bedrock-token")
	provider := CreateRuntimeProvider(CreateAmazonBedrockProvider(nil), nil)
	_, err := provider.APIAuth()
	if err == nil {
		t.Fatal("APIAuth returned nil error, want missing region")
	}
	if !strings.Contains(err.Error(), "requires model_providers.amazon-bedrock.aws.region") {
		t.Fatalf("error = %v", err)
	}
}

func TestAmazonBedrockProviderAPIAuthRejectsBearerTokenUnsupportedRegion(t *testing.T) {
	t.Setenv(AmazonBedrockBearerTokenEnv, "env-bedrock-token")
	t.Setenv("AWS_REGION", "us-west-1")
	provider := CreateRuntimeProvider(CreateAmazonBedrockProvider(nil), nil)
	_, err := provider.APIAuth()
	if err == nil {
		t.Fatal("APIAuth returned nil error, want unsupported region failure")
	}
	if !strings.Contains(err.Error(), "Amazon Bedrock Mantle does not support region `us-west-1`") {
		t.Fatalf("error = %v", err)
	}
}

func TestAmazonBedrockProviderAPIAuthUsesSigV4(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIDEXAMPLE")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY")
	info := CreateAmazonBedrockProvider(&ProviderAWSAuthInfo{Region: "us-west-2"})
	provider := CreateRuntimeProvider(info, nil)
	authHeaders, err := provider.APIAuth()
	if err != nil {
		t.Fatalf("APIAuth returned error: %v", err)
	}
	if authHeaders.SignRequest == nil {
		t.Fatal("SignRequest is nil")
	}

	var gotAuthorization string
	var gotSignedHeaders string
	var gotSnakeHeader string
	var gotContentEncoding string
	var gotContentLength int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthorization = r.Header.Get("Authorization")
		gotSignedHeaders = r.Header.Get("X-Amz-Date")
		gotSnakeHeader = r.Header.Get("session_id")
		gotContentEncoding = r.Header.Get("Content-Encoding")
		gotContentLength = r.ContentLength
		_, _ = w.Write([]byte(`{"id":"resp-1","output_text":"ok","output":[]}`))
	}))
	defer server.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{
			Name:    AmazonBedrockProviderName,
			BaseURL: server.URL + "/openai/v1",
			Headers: http.Header{
				AmazonBedrockMantleClientHeader: []string{AmazonBedrockMantleClientValue},
				"session_id":                    []string{"thread-1"},
				"Content-Encoding":              []string{"zstd"},
				"Content-Length":                []string{"999"},
			},
			RequestMaxRetries: 0,
		},
		Auth:       &authHeaders,
		HTTPClient: server.Client(),
		ProviderID: AmazonBedrockProviderID,
	})
	if _, err := runner.Run(context.Background(), &AgentRequest{Model: AmazonBedrockGPT55ModelID, Prompt: "hello"}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.HasPrefix(gotAuthorization, "AWS4-HMAC-SHA256 ") {
		t.Fatalf("Authorization = %q", gotAuthorization)
	}
	if gotSignedHeaders == "" {
		t.Fatal("X-Amz-Date was not sent")
	}
	if gotSnakeHeader != "" {
		t.Fatalf("session_id should be stripped before signing, got %q", gotSnakeHeader)
	}
	if gotContentEncoding != "" {
		t.Fatalf("Content-Encoding should be stripped before Bedrock signing, got %q", gotContentEncoding)
	}
	if gotContentLength == 999 {
		t.Fatalf("Content-Length should be rebuilt from the signed body, got stale value %d", gotContentLength)
	}
	if gotContentLength <= 0 {
		t.Fatalf("Content-Length = %d, want signed request body length", gotContentLength)
	}
}

func TestRemoveCompressionHeadersForPreparedBedrockBody(t *testing.T) {
	headers := http.Header{
		"Accept-Encoding":  []string{"gzip"},
		"Content-Encoding": []string{"zstd"},
		"Content-Length":   []string{"999"},
	}
	removeCompressionHeadersForPreparedBedrockBody(headers)
	if headers.Get("Content-Encoding") != "" {
		t.Fatalf("Content-Encoding = %q, want stripped", headers.Get("Content-Encoding"))
	}
	if headers.Get("Content-Length") != "" {
		t.Fatalf("Content-Length = %q, want stripped", headers.Get("Content-Length"))
	}
	if headers.Get("Accept-Encoding") != "gzip" {
		t.Fatalf("Accept-Encoding = %q, want preserved", headers.Get("Accept-Encoding"))
	}
}

func TestSignBedrockMantleRequestSetsFinalHost(t *testing.T) {
	awsContext, err := auth.NewAWSAuthContext(&auth.AWSAuthConfig{
		Region:  "us-west-2",
		Service: AmazonBedrockMantleServiceName,
	}, &auth.AWSAuthCredentials{
		AccessKeyID:     "AKIDEXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
	})
	if err != nil {
		t.Fatalf("NewAWSAuthContext returned error: %v", err)
	}
	request, err := http.NewRequest(http.MethodPost, "https://bedrock-mantle.us-west-2.api.aws/openai/v1/responses", nil)
	if err != nil {
		t.Fatalf("NewRequest returned error: %v", err)
	}
	request.Header.Set("thread_id", "thread-1")
	request.Header.Set("Content-Encoding", "zstd")
	request.Header.Set("Content-Length", "999")

	signed, err := signBedrockMantleRequest(awsContext, request, []byte(`{"model":"gpt-5.5"}`))
	if err != nil {
		t.Fatalf("signBedrockMantleRequest returned error: %v", err)
	}
	if string(signed.Body) != `{"model":"gpt-5.5"}` {
		t.Fatalf("signed body = %q", string(signed.Body))
	}
	if request.Host != "bedrock-mantle.us-west-2.api.aws" {
		t.Fatalf("request.Host = %q", request.Host)
	}
	if request.Header.Get("thread_id") != "" {
		t.Fatalf("thread_id should be stripped, got %q", request.Header.Get("thread_id"))
	}
	if request.Header.Get("Content-Encoding") != "" {
		t.Fatalf("Content-Encoding should be stripped, got %q", request.Header.Get("Content-Encoding"))
	}
	if request.Header.Get("Content-Length") != "" {
		t.Fatalf("Content-Length should be stripped before signing, got %q", request.Header.Get("Content-Length"))
	}
	if request.Header.Get("Authorization") == "" || request.Header.Get("X-Amz-Content-Sha256") == "" {
		t.Fatalf("signed headers missing Authorization or X-Amz-Content-Sha256: %#v", request.Header)
	}
}

func stringPtrProviderTest(value string) *string {
	return &value
}
