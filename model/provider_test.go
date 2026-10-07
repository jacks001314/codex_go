package model

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"codex_go/agent"
	"codex_go/auth"
)

func TestCreateRuntimeProviderConfiguredProvider(t *testing.T) {
	provider := CreateRuntimeProvider(CreateOpenAIProvider(""), &auth.AuthDotJSON{
		AuthMode:     "api-key",
		OpenAIAPIKey: "sk-test",
	})
	if provider.Info().Name != OpenAIProviderName {
		t.Fatalf("Info = %#v", provider.Info())
	}
	if provider.ApprovalReviewPreferredModel() != APIKeyApprovalReviewPreferredModel {
		t.Fatalf("approval model = %q", provider.ApprovalReviewPreferredModel())
	}
	if !provider.Capabilities().ImageGeneration || !provider.Capabilities().WebSearch {
		t.Fatalf("capabilities = %#v", provider.Capabilities())
	}
}

func TestConfiguredProviderApprovalReviewModelUsesLunaForAPIKeyLikeRust(t *testing.T) {
	provider := CreateRuntimeProvider(CreateOpenAIProvider(""), &auth.AuthDotJSON{
		AuthMode:     "api-key",
		OpenAIAPIKey: "sk-test",
	})
	if got := provider.ApprovalReviewPreferredModel(); got != APIKeyApprovalReviewPreferredModel {
		t.Fatalf("ApprovalReviewPreferredModel = %q, want %q (Rust c4f42d161a)", got, APIKeyApprovalReviewPreferredModel)
	}
}

func TestConfiguredProviderApprovalReviewModelKeepsDefaultForChatGPTLikeRust(t *testing.T) {
	provider := CreateRuntimeProvider(CreateOpenAIProvider(""), &auth.AuthDotJSON{
		AuthMode: "chatgpt",
		Tokens:   map[string]any{"access_token": "token"},
	})
	if got := provider.ApprovalReviewPreferredModel(); got != DefaultApprovalReviewPreferredModel {
		t.Fatalf("ApprovalReviewPreferredModel = %q, want %q (Rust c4f42d161a)", got, DefaultApprovalReviewPreferredModel)
	}

	nilAuth := CreateRuntimeProvider(CreateOpenAIProvider(""), nil)
	if got := nilAuth.ApprovalReviewPreferredModel(); got != DefaultApprovalReviewPreferredModel {
		t.Fatalf("ApprovalReviewPreferredModel with nil auth = %q, want %q", got, DefaultApprovalReviewPreferredModel)
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
	// Rust AmazonBedrockModelProvider::capabilities: the default provider is the
	// Mantle endpoint, where web search is available (Runtime is not).
	if capabilities.ImageGeneration || !capabilities.WebSearch {
		t.Fatalf("capabilities = %#v", capabilities)
	}
}

// Mirrors Rust #38470's provider wiring: AmazonBedrockModelProvider::models_manager
// builds its StaticModelsManager from `default_model_catalog`, so the Bedrock
// Runtime provider serves its own cross-region catalog while Mantle keeps the
// shared Bedrock catalog (codex-rs/model-provider/src/amazon_bedrock/mod.rs).
// The Go production caller is ResponsesAgentRunner
// (model/responses_agent.go), which calls runtimeProvider.ModelsManager(nil).
// The runtime fallback expectation matches upstream
// `thread_start_bedrock_runtime_prefers_global_cross_region_models`
// (codex-rs/app-server/tests/suite/v2/thread_start.rs): a Mantle slug falls back
// to the highest-priority Runtime variant, while supported cross-region slugs
// are preserved.
func TestAmazonBedrockProviderModelsManagerUsesRuntimeCatalogLikeRust(t *testing.T) {
	mantleManager := CreateRuntimeProvider(CreateAmazonBedrockProvider(nil), nil).ModelsManager(nil)
	if got := mantleManager.GetDefaultModel("", true, RefreshOffline); got != AmazonBedrockGPT61SolModelID {
		t.Fatalf("mantle default model = %q, want %q", got, AmazonBedrockGPT61SolModelID)
	}
	if got := mantleManager.GetDefaultModel(AmazonBedrockGPT56SolModelID, true, RefreshOffline); got != AmazonBedrockGPT56SolModelID {
		t.Fatalf("mantle supported model = %q, want %q", got, AmazonBedrockGPT56SolModelID)
	}

	runtimeManager := CreateRuntimeProvider(CreateAmazonBedrockRuntimeProvider(nil), nil).ModelsManager(nil)
	globalSol := bedrockRuntimeGlobalSlugPrefix + AmazonBedrockGPT61SolModelID
	if got := runtimeManager.GetDefaultModel("", true, RefreshOffline); got != globalSol {
		t.Fatalf("runtime default model = %q, want %q", got, globalSol)
	}
	if got := runtimeManager.GetDefaultModel(AmazonBedrockGPT56SolModelID, true, RefreshOffline); got != globalSol {
		t.Fatalf("runtime fallback model = %q, want %q", got, globalSol)
	}
	for _, supported := range []string{
		bedrockRuntimeGlobalSlugPrefix + AmazonBedrockGPT56SolModelID,
		bedrockRuntimeUSSlugPrefix + AmazonBedrockGPT56SolModelID,
	} {
		if got := runtimeManager.GetDefaultModel(supported, true, RefreshOffline); got != supported {
			t.Fatalf("runtime supported model = %q, want %q", got, supported)
		}
	}
}

// Mirrors Rust #38470 (d5e256ceb2)
// `preferred_background_models_match_bedrock_endpoint` in
// codex-rs/model-provider/src/amazon_bedrock/mod.rs: the Mantle endpoint
// prefers the GPT-5.6 Luna/Terra slugs, while the Bedrock Runtime endpoint
// prefers their `global.` cross-region variants. The same expectations are in
// `preferred_background_models_match_bedrock_endpoint`'s assertions on
// AMAZON_BEDROCK_GPT_5_6_LUNA/TERRA_MODEL_ID and
// AMAZON_BEDROCK_RUNTIME_GLOBAL_GPT_5_6_LUNA/TERRA_MODEL_ID.
func TestAmazonBedrockPreferredBackgroundModelsMatchEndpointLikeRust(t *testing.T) {
	cases := []struct {
		name     string
		provider RuntimeProvider
		want     [3]string
	}{
		{
			name:     "mantle",
			provider: CreateRuntimeProvider(CreateAmazonBedrockProvider(nil), nil),
			want: [3]string{
				AmazonBedrockGPT56LunaModelID,
				AmazonBedrockGPT56LunaModelID,
				AmazonBedrockGPT56TerraModelID,
			},
		},
		{
			name:     "runtime",
			provider: CreateRuntimeProvider(CreateAmazonBedrockRuntimeProvider(nil), nil),
			want: [3]string{
				AmazonBedrockRuntimeGlobalGPT56LunaModelID,
				AmazonBedrockRuntimeGlobalGPT56LunaModelID,
				AmazonBedrockRuntimeGlobalGPT56TerraModelID,
			},
		},
	}
	for _, testCase := range cases {
		got := [3]string{
			testCase.provider.ApprovalReviewPreferredModel(),
			testCase.provider.MemoryExtractionPreferredModel(),
			testCase.provider.MemoryConsolidationPreferredModel(),
		}
		if got != testCase.want {
			t.Fatalf("%s preferred background models = %v, want %v", testCase.name, got, testCase.want)
		}
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

// Mirrors Rust #38470 (d5e256ceb2) runtime::base_url and
// `runtime_managed_auth_resolves_runtime_endpoint` in
// codex-rs/model-provider/src/amazon_bedrock/mod.rs: the Bedrock Runtime
// provider resolves `https://bedrock-runtime.{region}.amazonaws.com/openai/v1`,
// while Mantle keeps `bedrock-mantle.{region}.api.aws`. Unlike Mantle, the
// Runtime endpoint has no supported-region allowlist upstream, so regions
// outside BEDROCK_MANTLE_SUPPORTED_REGIONS still resolve. The production call
// points are AmazonBedrockProvider::APIProvider/RuntimeBaseURL
// (model/provider.go), reached from ResponsesAgentRunner
// (model/responses_agent.go) and APIAuth's bearer-token region check.
func TestAmazonBedrockRuntimeEndpointLikeRust(t *testing.T) {
	cases := []struct {
		name   string
		region string
		want   string
	}{
		{
			name:   "configured region",
			region: "eu-west-1",
			want:   "https://bedrock-runtime.eu-west-1.amazonaws.com/openai/v1",
		},
		{
			name:   "region outside the Mantle allowlist",
			region: "us-west-1",
			want:   "https://bedrock-runtime.us-west-1.amazonaws.com/openai/v1",
		},
		{
			name:   "gov-cloud region outside the Mantle allowlist",
			region: "us-gov-west-1",
			want:   "https://bedrock-runtime.us-gov-west-1.amazonaws.com/openai/v1",
		},
	}
	for _, testCase := range cases {
		provider := CreateRuntimeProvider(
			CreateAmazonBedrockRuntimeProvider(&ProviderAWSAuthInfo{Region: testCase.region}),
			nil,
		)
		baseURL, err := provider.RuntimeBaseURL()
		if err != nil {
			t.Fatalf("%s: RuntimeBaseURL returned error: %v", testCase.name, err)
		}
		if baseURL != testCase.want {
			t.Fatalf("%s: baseURL = %q, want %q", testCase.name, baseURL, testCase.want)
		}
		apiProvider, err := provider.APIProvider()
		if err != nil {
			t.Fatalf("%s: APIProvider returned error: %v", testCase.name, err)
		}
		if apiProvider.BaseURL != testCase.want {
			t.Fatalf("%s: APIProvider.BaseURL = %q, want %q", testCase.name, apiProvider.BaseURL, testCase.want)
		}
	}

	mantle, err := CreateRuntimeProvider(
		CreateAmazonBedrockProvider(&ProviderAWSAuthInfo{Region: "eu-west-1"}),
		nil,
	).RuntimeBaseURL()
	if err != nil {
		t.Fatalf("mantle RuntimeBaseURL returned error: %v", err)
	}
	if mantle != "https://bedrock-mantle.eu-west-1.api.aws/openai/v1" {
		t.Fatalf("mantle baseURL = %q", mantle)
	}

	managed := CreateRuntimeProvider(CreateAmazonBedrockRuntimeProvider(nil), &auth.AuthDotJSON{
		AuthMode: "bedrock-api-key",
		BedrockAPIKey: map[string]any{
			"api_key": "managed-bedrock-api-key",
			"region":  "eu-west-1",
		},
	})
	managedBaseURL, err := managed.RuntimeBaseURL()
	if err != nil {
		t.Fatalf("managed RuntimeBaseURL returned error: %v", err)
	}
	if managedBaseURL != "https://bedrock-runtime.eu-west-1.amazonaws.com/openai/v1" {
		t.Fatalf("managed baseURL = %q", managedBaseURL)
	}
}

// Mirrors Rust #38470 (d5e256ceb2) runtime::aws_auth_config: the regional
// Bedrock Runtime endpoint signs with SigV4 service `bedrock`, while Mantle
// keeps `bedrock-mantle`. The production call point is
// AmazonBedrockProvider::awsAuthConfig (model/provider.go), used by APIAuth for
// the AWS SDK credential chain.
func TestAmazonBedrockRuntimeSigV4ServiceLikeRust(t *testing.T) {
	aws := &ProviderAWSAuthInfo{Profile: "codex-bedrock", Region: " us-west-2 "}
	mantle := CreateRuntimeProvider(CreateAmazonBedrockProvider(aws), nil).(*AmazonBedrockProvider)
	runtime := CreateRuntimeProvider(CreateAmazonBedrockRuntimeProvider(aws), nil).(*AmazonBedrockProvider)

	if got := mantle.awsAuthConfig(); got.Service != AmazonBedrockMantleServiceName ||
		got.Profile != "codex-bedrock" || got.Region != "us-west-2" {
		t.Fatalf("mantle awsAuthConfig = %#v", got)
	}
	if got := runtime.awsAuthConfig(); got.Service != AmazonBedrockRuntimeServiceName ||
		got.Profile != "codex-bedrock" || got.Region != "us-west-2" {
		t.Fatalf("runtime awsAuthConfig = %#v", got)
	}
	if AmazonBedrockRuntimeServiceName != "bedrock" {
		t.Fatalf("AmazonBedrockRuntimeServiceName = %q, want %q", AmazonBedrockRuntimeServiceName, "bedrock")
	}
}

// Mirrors Rust #38470 (d5e256ceb2) BedrockSigV4AuthProvider::apply_auth: only
// the Mantle endpoint strips the snake_case compatibility headers before SigV4,
// because the Mantle front door does not preserve them; the Bedrock Runtime
// endpoint signs them. The production call point is the SignRequest closure
// installed by AmazonBedrockProvider::APIAuth (model/provider.go), which passes
// the provider's endpoint into signBedrockRequest.
func TestSignBedrockRequestStripsCompatibilityHeadersOnlyForMantleLikeRust(t *testing.T) {
	newSignedHeaders := func(t *testing.T, endpoint bedrockEndpoint) http.Header {
		t.Helper()
		awsContext, err := auth.NewAWSAuthContext(&auth.AWSAuthConfig{
			Region:  "us-west-2",
			Service: bedrockServiceNameForEndpoint(endpoint),
		}, &auth.AWSAuthCredentials{
			AccessKeyID:     "AKIDEXAMPLE",
			SecretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
		})
		if err != nil {
			t.Fatalf("NewAWSAuthContext returned error: %v", err)
		}
		request, err := http.NewRequest(http.MethodPost, "https://bedrock-runtime.us-west-2.amazonaws.com/openai/v1/responses", nil)
		if err != nil {
			t.Fatalf("NewRequest returned error: %v", err)
		}
		request.Header.Set("session_id", "019dae79-15c3-70c3-8736-3219b8602b37")
		request.Header.Set("thread_id", "thread-1")
		if _, err := signBedrockRequest(awsContext, request, nil, endpoint); err != nil {
			t.Fatalf("signBedrockRequest returned error: %v", err)
		}
		return request.Header
	}

	runtimeEndpoint := CreateRuntimeProvider(CreateAmazonBedrockRuntimeProvider(nil), nil).(*AmazonBedrockProvider).endpoint()
	if runtimeEndpoint != bedrockEndpointRuntime {
		t.Fatalf("runtime provider endpoint = %v, want bedrockEndpointRuntime", runtimeEndpoint)
	}
	runtimeHeaders := newSignedHeaders(t, runtimeEndpoint)
	if runtimeHeaders.Get("session_id") != "019dae79-15c3-70c3-8736-3219b8602b37" ||
		runtimeHeaders.Get("thread_id") != "thread-1" {
		t.Fatalf("runtime headers dropped snake_case compatibility headers: %#v", runtimeHeaders)
	}

	mantleHeaders := newSignedHeaders(t, bedrockEndpointMantle)
	if mantleHeaders.Get("session_id") != "" || mantleHeaders.Get("thread_id") != "" {
		t.Fatalf("mantle headers kept snake_case compatibility headers: %#v", mantleHeaders)
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

func TestAmazonBedrockProviderAPIAuthUsesCommandOverride(t *testing.T) {
	info := CreateAmazonBedrockProvider(nil)
	info.Auth = commandAuthForTest("command-bedrock-token")
	provider := CreateRuntimeProvider(info, nil)
	headers, err := provider.APIAuth()
	if err != nil {
		t.Fatal(err)
	}
	if headers.Headers.Get("Authorization") != "Bearer command-bedrock-token" {
		t.Fatalf("headers = %#v", headers.Headers)
	}
	if headers.SignRequest != nil {
		t.Fatal("command-auth Bedrock request should not use AWS request signing")
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

	signed, err := signBedrockRequest(awsContext, request, []byte(`{"model":"gpt-5.5"}`), bedrockEndpointMantle)
	if err != nil {
		t.Fatalf("signBedrockRequest returned error: %v", err)
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
