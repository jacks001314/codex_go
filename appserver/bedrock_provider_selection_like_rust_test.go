package appserver

// Rust #62222: account/bedrock/setup and account/login/start accept an optional
// providerId. "amazonBedrockRuntime" writes the Runtime provider table, while an
// omitted or null value keeps the Amazon Bedrock Mantle default.

import (
	"strings"
	"testing"

	"codex_go/auth"
	"codex_go/config"
)

func bedrockSetupProviderTable(t *testing.T, home string, providerID string) map[string]any {
	t.Helper()
	values := readConfigValues(t, home)
	providers, _ := values["model_providers"].(map[string]any)
	provider, _ := providers[providerID].(map[string]any)
	aws, _ := provider["aws"].(map[string]any)
	return aws
}

func TestBedrockSetupSelectsRuntimeProviderLikeRust(t *testing.T) {
	clearBedrockEnv(t)
	home := t.TempDir()
	writeAWSFixtures(t, home)

	router := bedrockTestRouter(home)
	setup := router.Handle(requestWithParams(t, IntID(1), MethodBedrockSetup, map[string]any{
		"type":       "profile",
		"profile":    " engineering ",
		"region":     " us-west-2 ",
		"providerId": BedrockProviderIDAmazonBedrockRuntime,
	}))
	if setup.Error != nil {
		t.Fatalf("bedrock setup = %+v", setup.Error)
	}

	configText := readFileString(t, config.ConfigPath(home))
	if !strings.Contains(configText, `model_provider = "amazon-bedrock-runtime"`) {
		t.Fatalf("config = %q, want amazon-bedrock-runtime model_provider", configText)
	}
	aws := bedrockSetupProviderTable(t, home, "amazon-bedrock-runtime")
	if got := aws["profile"]; got != "engineering" {
		t.Fatalf("runtime aws.profile = %#v, want engineering", got)
	}
	if got := aws["region"]; got != "us-west-2" {
		t.Fatalf("runtime aws.region = %#v, want us-west-2", got)
	}
	if mantle := bedrockSetupProviderTable(t, home, "amazon-bedrock"); len(mantle) != 0 {
		t.Fatalf("Mantle table = %#v, want the Runtime selection to leave it untouched", mantle)
	}
}

func TestBedrockSetupEnvironmentSelectionKeepsMantleByDefaultLikeRust(t *testing.T) {
	clearBedrockEnv(t)
	home := t.TempDir()
	writeAWSFixtures(t, home)
	t.Setenv("AWS_ACCESS_KEY_ID", "environment-id")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "environment-secret")

	router := bedrockTestRouter(home)
	setup := router.Handle(requestWithParams(t, IntID(1), MethodBedrockSetup, map[string]any{
		"type":       "environment",
		"region":     "us-east-2",
		"providerId": nil,
	}))
	if setup.Error != nil {
		t.Fatalf("bedrock setup = %+v", setup.Error)
	}
	configText := readFileString(t, config.ConfigPath(home))
	if !strings.Contains(configText, `model_provider = "amazon-bedrock"`) {
		t.Fatalf("config = %q, want the Mantle default", configText)
	}
	if mantle := bedrockSetupProviderTable(t, home, "amazon-bedrock"); mantle["region"] != "us-east-2" {
		t.Fatalf("Mantle aws = %#v, want the trimmed region", mantle)
	}
	if runtime := bedrockSetupProviderTable(t, home, "amazon-bedrock-runtime"); len(runtime) != 0 {
		t.Fatalf("Runtime table = %#v, want default selection to leave it untouched", runtime)
	}
}

func TestBedrockSetupRejectsUnknownProviderIDLikeRust(t *testing.T) {
	clearBedrockEnv(t)
	home := t.TempDir()
	writeAWSFixtures(t, home)
	router := bedrockTestRouter(home)
	for _, params := range []map[string]any{
		{"type": "profile", "profile": "engineering", "region": "us-west-2", "providerId": "amazonBedrockMantle"},
		{"type": "environment", "region": "us-west-2", "providerId": ""},
	} {
		response := router.Handle(requestWithParams(t, IntID(1), MethodBedrockSetup, params))
		if response.Error == nil {
			t.Fatalf("setup %#v succeeded, want an unknown variant error", params)
		}
		if message := response.Error.Message; !strings.Contains(message, "unknown variant") ||
			!strings.Contains(message, BedrockProviderIDAmazonBedrock) ||
			!strings.Contains(message, BedrockProviderIDAmazonBedrockRuntime) {
			t.Fatalf("error = %q, want an unknown variant listing both variants", message)
		}
	}
}

func TestBedrockLoginSelectsRuntimeProviderLikeRust(t *testing.T) {
	clearBedrockEnv(t)
	home := t.TempDir()
	runtimeProvider := BedrockProviderIDAmazonBedrockRuntime
	router := bedrockTestRouter(home)
	login := router.Handle(requestWithParams(t, IntID(1), MethodLoginAccount, map[string]any{
		"type":            "amazonBedrockAccessKeys",
		"accessKeyId":     "AKIDEXAMPLE",
		"secretAccessKey": "secret-example",
		"region":          "us-west-2",
		"providerId":      runtimeProvider,
	}))
	if login.Error != nil {
		t.Fatalf("login = %+v", login.Error)
	}
	configText := readFileString(t, config.ConfigPath(home))
	if !strings.Contains(configText, `model_provider = "amazon-bedrock-runtime"`) {
		t.Fatalf("config = %q, want amazon-bedrock-runtime", configText)
	}
	aws := bedrockSetupProviderTable(t, home, "amazon-bedrock-runtime")
	if aws["region"] != "us-west-2" {
		t.Fatalf("runtime aws = %#v, want the login region", aws)
	}
	logout := router.Handle(requestWithParams(t, IntID(2), MethodLogoutAccount, map[string]any{}))
	if logout.Error != nil {
		t.Fatalf("logout = %+v", logout.Error)
	}
	configText = readFileString(t, config.ConfigPath(home))
	if strings.Contains(configText, `model_provider = "amazon-bedrock-runtime"`) ||
		strings.Contains(configText, `region = "us-west-2"`) {
		t.Fatalf("logout did not clear the Runtime provider config: %q", configText)
	}
}

func TestBedrockLoginRejectsUnknownProviderIDLikeRust(t *testing.T) {
	clearBedrockEnv(t)
	home := t.TempDir()
	router := bedrockTestRouter(home)
	login := router.Handle(requestWithParams(t, IntID(1), MethodLoginAccount, map[string]any{
		"type":       auth.AccountAmazonBedrock,
		"apiKey":     "bedrock-api-key",
		"region":     "us-east-2",
		"providerId": "amazonBedrockOther",
	}))
	if login.Error == nil {
		t.Fatal("login succeeded with an unknown providerId")
	}
	if message := login.Error.Message; !strings.Contains(message, "unknown variant") {
		t.Fatalf("error = %q, want an unknown variant error", message)
	}
	if snapshot := router.requireAccount().AuthSnapshot(); snapshot != nil {
		t.Fatalf("auth snapshot = %#v, want the rejected login to store nothing", snapshot)
	}
}
