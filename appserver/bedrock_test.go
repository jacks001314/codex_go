package appserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/auth"
	"codex_go/config"

	"github.com/pelletier/go-toml/v2"
)

// clearBedrockEnv mirrors the env clearing that Rust's TestAppServer does in
// app-server/tests/suite/v2/bedrock_setup.rs::bedrock_app_server.
func clearBedrockEnv(t *testing.T) {
	t.Helper()
	t.Setenv("AWS_REGION", "")
	t.Setenv("AWS_DEFAULT_REGION", "")
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_DEFAULT_PROFILE", "")
	t.Setenv("AWS_CONFIG_FILE", "")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_BEARER_TOKEN_BEDROCK", "")
	clearAuthEnvAppserver(t)
}

// writeAWSFixtures mirrors the shared config/credentials files that Rust's
// bedrock_app_server helper writes.
func writeAWSFixtures(t *testing.T, home string) {
	t.Helper()
	awsConfig := filepath.Join(home, "aws-config")
	awsCredentials := filepath.Join(home, "aws-credentials")
	if err := os.WriteFile(awsConfig, []byte("[profile engineering]\nregion = us-west-2\n"), 0o600); err != nil {
		t.Fatalf("write aws config: %v", err)
	}
	if err := os.WriteFile(awsCredentials, []byte("[engineering]\naws_access_key_id = engineering-id\naws_secret_access_key = engineering-secret\n[finance]\naws_access_key_id = finance-id\naws_secret_access_key = finance-secret\n"), 0o600); err != nil {
		t.Fatalf("write aws credentials: %v", err)
	}
	t.Setenv("AWS_CONFIG_FILE", awsConfig)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", awsCredentials)
}

func bedrockTestRouter(home string) *RuntimeRouter {
	return NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home), Account: auth.NewAccountManager()})
}

// Rust #39277 ("Declare experimental Amazon Bedrock setup APIs"),
// app-server/tests/suite/v2/bedrock_setup.rs::discover_bedrock_profiles_and_environment_credentials.
func TestBedrockDiscoverProfilesAndEnvironmentCredentialsLikeRust(t *testing.T) {
	clearBedrockEnv(t)
	home := t.TempDir()
	writeAWSFixtures(t, home)
	t.Setenv("AWS_PROFILE", "engineering")
	t.Setenv("AWS_ACCESS_KEY_ID", "environment-id")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "environment-secret")
	t.Setenv("AWS_SESSION_TOKEN", "environment-token")
	t.Setenv("AWS_BEARER_TOKEN_BEDROCK", "environment-bedrock-key")
	t.Setenv("AWS_DEFAULT_REGION", "us-west-2")

	router := bedrockTestRouter(home)
	response := router.Handle(requestWithParams(t, IntID(1), MethodBedrockDiscover, map[string]any{}))
	if response.Error != nil {
		t.Fatalf("bedrock discover = %+v", response.Error)
	}
	encoded, err := json.Marshal(response.Result)
	if err != nil {
		t.Fatalf("marshal discover response: %v", err)
	}
	// AWS_DEFAULT_REGION is not consulted: Rust reads only AWS_REGION, so both
	// environment credentials carry a null region.
	want := `{"profiles":[{"name":"engineering","region":"us-west-2"},{"name":"finance","region":null}],"environmentCredentials":[{"type":"accessKeys","region":null},{"type":"bedrockApiKey","region":null}]}`
	if string(encoded) != want {
		t.Fatalf("discover response = %s, want %s", encoded, want)
	}

	setup := router.Handle(requestWithParams(t, IntID(2), MethodBedrockSetup, map[string]any{"type": "environment", "region": " us-east-2 "}))
	if setup.Error != nil {
		t.Fatalf("bedrock setup = %+v", setup.Error)
	}
	configText := readFileString(t, config.ConfigPath(home))
	if !strings.Contains(configText, `model_provider = "amazon-bedrock"`) {
		t.Fatalf("config = %q, want amazon-bedrock model_provider", configText)
	}
	if !strings.Contains(configText, `region = "us-east-2"`) {
		t.Fatalf("config = %q, want trimmed us-east-2 region", configText)
	}
	if strings.Contains(configText, "profile") {
		t.Fatalf("config = %q, environment setup must clear the aws profile", configText)
	}
	if _, err := os.Stat(filepath.Join(home, ".env")); !os.IsNotExist(err) {
		t.Fatalf(".env should not exist, stat err = %v", err)
	}
}

// Rust #39277,
// app-server/tests/suite/v2/bedrock_setup.rs::setup_bedrock_profile_and_environment.
func TestBedrockSetupProfilePreservesExistingAWSSettingsLikeRust(t *testing.T) {
	clearBedrockEnv(t)
	home := t.TempDir()
	writeAWSFixtures(t, home)
	t.Setenv("AWS_ACCESS_KEY_ID", "environment-id")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "environment-secret")
	t.Setenv("AWS_DEFAULT_REGION", "us-east-1")
	existing := "[model_providers.amazon-bedrock]\nhttp_headers = { X-Existing = \"preserved\" }\n" +
		"[model_providers.amazon-bedrock.aws]\nprofile = \"old\"\nregion = \"us-east-1\"\n"
	if err := os.WriteFile(config.ConfigPath(home), []byte(existing), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	router := bedrockTestRouter(home)
	setup := router.Handle(requestWithParams(t, IntID(1), MethodBedrockSetup, map[string]any{"type": "profile", "profile": " engineering ", "region": " us-west-2 "}))
	if setup.Error != nil {
		t.Fatalf("bedrock setup = %+v", setup.Error)
	}
	values := readConfigValues(t, home)
	aws := awsTable(t, values)
	if got := aws["profile"]; got != "engineering" {
		t.Fatalf("aws.profile = %#v, want trimmed engineering", got)
	}
	if got := aws["region"]; got != "us-west-2" {
		t.Fatalf("aws.region = %#v, want trimmed us-west-2", got)
	}
	providers, _ := values["model_providers"].(map[string]any)
	bedrock, _ := providers["amazon-bedrock"].(map[string]any)
	if got := bedrock["http_headers"]; got == nil {
		t.Fatalf("existing model_providers.amazon-bedrock.http_headers was dropped: %#v", values)
	}
}

// Rust #39277,
// app-server/tests/suite/v2/bedrock_setup.rs::setup_bedrock_rejects_invalid_or_conflicting_credentials.
func TestBedrockSetupRejectsInvalidOrConflictingCredentialsLikeRust(t *testing.T) {
	clearBedrockEnv(t)
	home := t.TempDir()
	writeAWSFixtures(t, home)
	router := bedrockTestRouter(home)
	cases := []struct {
		params   map[string]any
		want     string
		contains bool
	}{
		{params: map[string]any{"type": "profile", "profile": "engineering", "region": "us-west-1"}, want: "Amazon Bedrock does not support region `us-west-1`"},
		{params: map[string]any{"type": "profile", "profile": " ", "region": "us-west-2"}, want: "AWS profile name must not be empty."},
		{params: map[string]any{"type": "profile", "profile": "missing", "region": "us-west-2"}, want: "failed to load credentials for AWS profile `missing`:", contains: true},
		{params: map[string]any{"type": "environment", "region": "us-west-2"}, want: "No AWS credentials found. Please Configure AWS credentials or complete AWS sign-in, then try again."},
	}
	for _, testCase := range cases {
		response := router.Handle(requestWithParams(t, IntID(1), MethodBedrockSetup, testCase.params))
		if response.Error == nil {
			t.Fatalf("setup %v succeeded, want %q", testCase.params, testCase.want)
		}
		if testCase.contains {
			if !strings.Contains(response.Error.Message, testCase.want) {
				t.Fatalf("setup %v = %q, want it to contain %q", testCase.params, response.Error.Message, testCase.want)
			}
			continue
		}
		if response.Error.Message != testCase.want {
			t.Fatalf("setup %v = %q, want %q", testCase.params, response.Error.Message, testCase.want)
		}
	}
	if _, err := os.Stat(config.ConfigPath(home)); !os.IsNotExist(err) {
		t.Fatalf("config.toml should not exist after rejected setup, stat err = %v", err)
	}

	restricted := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(restricted), []byte("forced_login_method = \"chatgpt\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	restrictedRouter := bedrockTestRouter(restricted)
	response := restrictedRouter.Handle(requestWithParams(t, IntID(1), MethodBedrockDiscover, map[string]any{}))
	if response.Error == nil || response.Error.Message != "Amazon Bedrock login is disabled. Use ChatGPT login instead." {
		t.Fatalf("discover under forced chatgpt = %+v", response.Error)
	}
}

// Rust #39277,
// app-server/tests/suite/v2/bedrock_setup.rs::setup_bedrock_profile_and_environment
// (Codex-managed credentials take priority over environment credentials).
func TestBedrockSetupRejectsEnvironmentWithManagedCredentialsLikeRust(t *testing.T) {
	clearBedrockEnv(t)
	home := t.TempDir()
	writeAWSFixtures(t, home)
	t.Setenv("AWS_BEARER_TOKEN_BEDROCK", "environment-bedrock-api-key")
	router := bedrockTestRouter(home)
	snapshot := auth.FromBedrockAPIKey("managed-bedrock-api-key", "us-east-1")
	router.requireAccount().ApplyAuthSnapshot(&snapshot)

	response := router.Handle(requestWithParams(t, IntID(1), MethodBedrockSetup, map[string]any{"type": "environment", "region": "us-east-1"}))
	want := "Codex-managed Bedrock credentials are already configured and take priority over AWS environment credentials. Run `codex logout` and try again."
	if response.Error == nil || response.Error.Message != want {
		t.Fatalf("setup with managed credentials = %+v, want %q", response.Error, want)
	}
	if _, err := os.Stat(config.ConfigPath(home)); !os.IsNotExist(err) {
		t.Fatalf("config.toml should not be written, stat err = %v", err)
	}
}

// Rust #49817 ("Advisory GovCloud requirements check") / #49813,
// app-server/tests/suite/v2/bedrock_gov_cloud_tests.rs::checks_current_provider_after_login_without_restart.
func TestBedrockCheckGovCloudTracksCurrentProviderLikeRust(t *testing.T) {
	clearBedrockEnv(t)
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte(""), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	router := bedrockTestRouter(home)
	snapshot := auth.FromBedrockAPIKey("test-key", "us-gov-west-1")
	router.requireAccount().ApplyAuthSnapshot(&snapshot)

	assertGovCloud(t, router, false, false)

	if err := os.WriteFile(config.ConfigPath(home), []byte("model_provider = 'amazon-bedrock'\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	assertGovCloud(t, router, true, true)
}

// Rust #49813/#49817,
// app-server/tests/suite/v2/bedrock_gov_cloud_tests.rs::checks_required_policy_fields_and_endpoint.
func TestBedrockCheckGovCloudRequiresPolicyBaselineLikeRust(t *testing.T) {
	clearBedrockEnv(t)
	const baseline = "\nallowed_login_methods = [\"api\"]\n[application.network]\nenabled = true\n[application.network.domains]\n\"bedrock-mantle.us-gov-west-1.api.aws\" = \"allow\"\n"
	for _, testCase := range []struct {
		name         string
		provider     string
		config       string
		domain       string
		requirements string
		shouldWarn   bool
	}{
		{name: "mantle", provider: "amazon-bedrock", domain: "bedrock-mantle.us-gov-west-1.api.aws", requirements: baseline, shouldWarn: false},
		{name: "runtime", provider: "amazon-bedrock-runtime", domain: "bedrock-runtime.us-gov-west-1.amazonaws.com", shouldWarn: false},
		{
			name:       "custom_endpoint",
			provider:   "amazon-bedrock",
			config:     "[model_providers.amazon-bedrock]\nbase_url = 'https://bedrock.example.com/openai/v1'\n",
			domain:     "bedrock.example.com",
			shouldWarn: false,
		},
		{name: "mixed_login_methods", provider: "amazon-bedrock", domain: "bedrock-mantle.us-gov-west-1.api.aws", requirements: strings.Replace(baseline, "[\"api\"]", "[\"api\", \"chatgpt\"]", 1), shouldWarn: true},
		{name: "network_disabled", provider: "amazon-bedrock", domain: "bedrock-mantle.us-gov-west-1.api.aws", requirements: strings.Replace(baseline, "enabled = true", "enabled = false", 1), shouldWarn: true},
		{name: "denied_domain", provider: "amazon-bedrock", domain: "bedrock-mantle.us-gov-west-1.api.aws", requirements: strings.Replace(baseline, "\"allow\"", "\"deny\"", 1), shouldWarn: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			clearBedrockEnv(t)
			home := t.TempDir()
			configText := "model_provider = '" + testCase.provider + "'\n" + testCase.config
			if err := os.WriteFile(config.ConfigPath(home), []byte(configText), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}
			router := bedrockTestRouter(home)
			snapshot := auth.FromBedrockAPIKey("test-key", "us-gov-west-1")
			router.requireAccount().ApplyAuthSnapshot(&snapshot)

			assertGovCloud(t, router, true, true)

			requirements := testCase.requirements
			if requirements == "" {
				requirements = baseline
			}
			requirements = strings.Replace(requirements, "bedrock-mantle.us-gov-west-1.api.aws", testCase.domain, 1)
			if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte(requirements), 0o600); err != nil {
				t.Fatalf("write requirements: %v", err)
			}
			assertGovCloud(t, router, true, testCase.shouldWarn)
		})
	}
}

// Rust #49813/#49817,
// app-server/tests/suite/v2/bedrock_gov_cloud_tests.rs::official_endpoint_takes_precedence_over_auth_region.
func TestBedrockCheckGovCloudEndpointPrecedenceLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		domain     string
		authRegion string
		isGovCloud bool
	}{
		{name: "mantle_url_overrides_commercial_region", domain: "bedrock-mantle.us-gov-west-1.api.aws", authRegion: "us-west-2", isGovCloud: true},
		{name: "runtime_url_overrides_commercial_region", domain: "bedrock-runtime.us-gov-east-1.amazonaws.com", authRegion: "us-west-2", isGovCloud: true},
		{name: "fips_url_overrides_commercial_region", domain: "bedrock-runtime-fips.us-gov-west-1.amazonaws.com", authRegion: "us-west-2", isGovCloud: true},
		{name: "commercial_url_overrides_govcloud_region", domain: "bedrock-mantle.us-west-2.api.aws", authRegion: "us-gov-west-1", isGovCloud: false},
		{name: "lookalike_proxy_uses_region", domain: "bedrock-mantle.us-gov-west-1.api.aws.example.com", authRegion: "us-west-2", isGovCloud: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			clearBedrockEnv(t)
			home := t.TempDir()
			configText := "model_provider = 'amazon-bedrock'\n" +
				"[model_providers.amazon-bedrock]\nbase_url = 'https://" + testCase.domain + "/openai/v1'\n"
			if err := os.WriteFile(config.ConfigPath(home), []byte(configText), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}
			router := bedrockTestRouter(home)
			snapshot := auth.FromBedrockAPIKey("test-key", testCase.authRegion)
			router.requireAccount().ApplyAuthSnapshot(&snapshot)

			assertGovCloud(t, router, testCase.isGovCloud, testCase.isGovCloud)
		})
	}
}

// Rust #49813/#49817: an unreadable managed requirements file is an internal
// RPC error (-32603) and the message is prefixed with the config load failure.
func TestBedrockCheckGovCloudReportsInvalidRequirementsLikeRust(t *testing.T) {
	clearBedrockEnv(t)
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte("model_provider = 'amazon-bedrock'\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte("[invalid toml"), 0o600); err != nil {
		t.Fatalf("write requirements: %v", err)
	}
	router := bedrockTestRouter(home)
	snapshot := auth.FromBedrockAPIKey("test-key", "us-gov-west-1")
	router.requireAccount().ApplyAuthSnapshot(&snapshot)

	response := router.Handle(requestWithParams(t, IntID(1), MethodBedrockCheckGovCloudRequirements, map[string]any{}))
	if response.Error == nil {
		t.Fatalf("expected an internal error, got %+v", response.Result)
	}
	if response.Error.Code != JSONRPCInternalErrorCode {
		t.Fatalf("error code = %d, want %d", response.Error.Code, JSONRPCInternalErrorCode)
	}
	if !strings.HasPrefix(response.Error.Message, "failed to load configuration:") {
		t.Fatalf("error message = %q", response.Error.Message)
	}
}

// Rust #39277/#49813: the Bedrock methods are experimental client requests.
func TestBedrockMethodsRequireExperimentalAPILikeRust(t *testing.T) {
	for _, method := range []Method{MethodBedrockDiscover, MethodBedrockSetup, MethodBedrockCheckGovCloudRequirements} {
		if !experimentalAPIMethod(method) {
			t.Fatalf("experimentalAPIMethod(%s) = false, want true", method)
		}
	}
	stable := BuildProtocolSchema(false, false)
	for _, entry := range stable.ClientRequests {
		switch Method(entry.Method) {
		case MethodBedrockDiscover, MethodBedrockSetup, MethodBedrockCheckGovCloudRequirements:
			t.Fatalf("%s must not be part of the stable client request surface", entry.Method)
		}
	}
	experimental := BuildProtocolSchema(true, false)
	for method, wantParams := range map[Method]string{
		MethodBedrockDiscover:                  "BedrockDiscoverParams",
		MethodBedrockSetup:                     "BedrockSetupParams",
		MethodBedrockCheckGovCloudRequirements: "BedrockCheckGovCloudRequirementsParams",
	} {
		found := false
		for _, entry := range experimental.ClientRequests {
			if Method(entry.Method) == method {
				found = true
				if entry.Params != wantParams {
					t.Fatalf("%s params = %q, want %q", method, entry.Params, wantParams)
				}
			}
		}
		if !found {
			t.Fatalf("experimental client requests missing %s", method)
		}
	}
}

// The Go app-server keeps its own copy of the supported Bedrock region tables
// because model/ is outside this work item's write scope; keep them aligned with
// model/amazonBedrockMantleSupportedRegions and the gov-cloud set.
func TestBedrockSupportedRegionsMatchModelLikeRust(t *testing.T) {
	mantleRegions := []string{
		"us-east-2", "us-east-1", "us-west-2", "ap-southeast-3", "ap-south-1",
		"ap-northeast-1", "eu-central-1", "eu-west-1", "eu-west-2", "eu-south-1",
		"eu-north-1", "sa-east-1",
	}
	for _, region := range mantleRegions {
		if !isSupportedAmazonBedrockRegion(region) {
			t.Fatalf("isSupportedAmazonBedrockRegion(%q) = false", region)
		}
		if isAmazonBedrockGovCloudRegion(region) {
			t.Fatalf("isAmazonBedrockGovCloudRegion(%q) = true", region)
		}
	}
	for _, region := range []string{"us-gov-east-1", "us-gov-west-1"} {
		if !isSupportedAmazonBedrockRegion(region) || !isAmazonBedrockGovCloudRegion(region) {
			t.Fatalf("gov cloud region %q must be supported and reported as gov cloud", region)
		}
	}
	for _, region := range []string{"us-west-1", "us-gov-east-2", ""} {
		if isSupportedAmazonBedrockRegion(region) {
			t.Fatalf("isSupportedAmazonBedrockRegion(%q) = true", region)
		}
	}
}

func assertGovCloud(t *testing.T, router *RuntimeRouter, wantGovCloud bool, wantWarn bool) {
	t.Helper()
	response := router.Handle(requestWithParams(t, IntID(1), MethodBedrockCheckGovCloudRequirements, map[string]any{}))
	if response.Error != nil {
		t.Fatalf("checkGovCloudRequirements = %+v", response.Error)
	}
	result, ok := response.Result.(*BedrockCheckGovCloudRequirementsResponse)
	if !ok {
		t.Fatalf("result = %#v", response.Result)
	}
	if result.IsGovCloud != wantGovCloud || result.ShouldWarn != wantWarn {
		t.Fatalf("check = %+v, want isGovCloud=%v shouldWarn=%v", result, wantGovCloud, wantWarn)
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func readConfigValues(t *testing.T, home string) map[string]any {
	t.Helper()
	var values map[string]any
	if err := toml.Unmarshal([]byte(readFileString(t, config.ConfigPath(home))), &values); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	return values
}

func awsTable(t *testing.T, values map[string]any) map[string]any {
	t.Helper()
	providers, _ := values["model_providers"].(map[string]any)
	bedrock, _ := providers["amazon-bedrock"].(map[string]any)
	aws, _ := bedrock["aws"].(map[string]any)
	return aws
}
