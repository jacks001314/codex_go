package appserver

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/model"
)

func providerTestString(value string) *string {
	return &value
}

func providerTestOptional(value string) model.OptionalString {
	return model.OptionalString{Set: true, Value: providerTestString(value)}
}

func providerTestNull() model.OptionalString {
	return model.OptionalString{Set: true}
}

func customGatewaySettings(name string, baseURL model.OptionalString, envKey model.OptionalString, providerID *string) model.ProviderSettingsPatch {
	patch := model.ProviderSettingsPatch{
		Type: "customGateway",
		CustomGateway: &model.CustomGatewaySettingsParams{
			ProviderID: providerID,
			BaseURL:    baseURL,
			EnvKey:     envKey,
		},
	}
	if name != "" {
		patch.CustomGateway.Name = providerTestString(name)
	}
	return patch
}

func writeModelProviderSettings(t *testing.T, router *RuntimeRouter, settings model.ProviderSettingsPatch) map[string]any {
	t.Helper()
	response := router.Handle(requestWithParams(t, IntID(1), MethodModelProviderConfigWrite, model.ProviderConfigWriteParams{Settings: settings}))
	if response.Error != nil {
		t.Fatalf("modelProvider/config/write error = %+v", response.Error)
	}
	raw, err := json.Marshal(response.Result)
	if err != nil {
		t.Fatalf("marshal write result: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode write result: %v", err)
	}
	return decoded
}

func writeModelProviderSettingsError(t *testing.T, router *RuntimeRouter, settings model.ProviderSettingsPatch) *ResponseError {
	t.Helper()
	response := router.Handle(requestWithParams(t, IntID(1), MethodModelProviderConfigWrite, model.ProviderConfigWriteParams{Settings: settings}))
	if response.Error == nil {
		t.Fatalf("modelProvider/config/write unexpectedly succeeded: %#v", response.Result)
	}
	return response.Error
}

func writeModelProviderRawError(t *testing.T, router *RuntimeRouter, params string) *ResponseError {
	t.Helper()
	response := router.Handle(&Request{JSONRPC: "2.0", ID: IntID(1), Method: MethodModelProviderConfigWrite, Params: json.RawMessage(params)})
	if response.Error == nil {
		t.Fatalf("modelProvider/config/write unexpectedly succeeded: %#v", response.Result)
	}
	return response.Error
}

func requireProviderWriteErrorCode(t *testing.T, err *ResponseError, code string) {
	t.Helper()
	if err.Data == nil || err.Data["config_write_error_code"] != code {
		t.Fatalf("write error = %+v, want config_write_error_code %q", err, code)
	}
}

func assertProviderWriteJSON(t *testing.T, got map[string]any, want string) {
	t.Helper()
	var expected map[string]any
	if err := json.Unmarshal([]byte(want), &expected); err != nil {
		t.Fatalf("decode expected provider JSON: %v", err)
	}
	if !reflect.DeepEqual(got, expected) {
		gotJSON, _ := json.MarshalIndent(got, "", "  ")
		t.Fatalf("provider mismatch\ngot: %s", gotJSON)
	}
}

func readParsedConfigFile(t *testing.T, home string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(config.ConfigPath(home))
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	values, err := config.ParseConfigDocumentValues(data)
	if err != nil {
		t.Fatalf("parse config.toml: %v", err)
	}
	return values
}

// Mirrors Rust #58876 create_and_patch_gateway_providers_without_changing_the_default.
func TestModelProviderConfigWriteCreatesAndPatchesGatewaysLikeRust(t *testing.T) {
	home := t.TempDir()
	router := newManagedProviderRouter(t, home)

	saved := writeModelProviderSettings(t, router, customGatewaySettings("Team Gateway", providerTestOptional("https://gateway.example.test/v1"), providerTestOptional("TEAM_KEY"), nil))
	assertProviderWriteJSON(t, saved, `{"provider":{"id":"team-gateway","name":"Team Gateway","canEdit":true,
		"baseUrl":"https://gateway.example.test/v1",
		"settings":{"type":"customGateway","auth":{"mode":"environmentVariable","envKey":"TEAM_KEY"}}}}`)

	duplicate := writeModelProviderSettings(t, router, customGatewaySettings("Team Gateway", providerTestOptional("https://gateway.example.test/v1"), providerTestOptional("TEAM_KEY"), nil))
	if duplicate["provider"].(map[string]any)["id"] != "team-gateway-2" {
		t.Fatalf("duplicate provider = %#v", duplicate)
	}

	providerID := providerTestString("team-gateway")
	expected := map[string]any{}
	if err := json.Unmarshal([]byte(`{"provider":{"id":"team-gateway","name":"Team Gateway","canEdit":true,
		"baseUrl":"https://gateway.example.test/v1",
		"settings":{"type":"customGateway","auth":{"mode":"environmentVariable","envKey":"TEAM_KEY"}}}}`), &expected); err != nil {
		t.Fatal(err)
	}
	provider := expected["provider"].(map[string]any)
	patches := []struct {
		settings model.ProviderSettingsPatch
		apply    func()
	}{
		{
			settings: customGatewaySettings("Renamed", model.OptionalString{}, model.OptionalString{}, providerID),
			apply:    func() { provider["name"] = "Renamed" },
		},
		{
			settings: customGatewaySettings("", providerTestOptional("https://new.example.test"), model.OptionalString{}, providerID),
			apply:    func() { provider["baseUrl"] = "https://new.example.test" },
		},
		{
			settings: model.ProviderSettingsPatch{Type: "customGateway", CustomGateway: &model.CustomGatewaySettingsParams{ProviderID: providerID, Name: providerTestString("Latest")}},
			apply:    func() { provider["name"] = "Latest" },
		},
		{
			settings: customGatewaySettings("", model.OptionalString{}, providerTestNull(), providerID),
			apply: func() {
				provider["settings"] = map[string]any{"type": "customGateway", "auth": map[string]any{"mode": "none"}}
			},
		},
		{
			settings: customGatewaySettings("", providerTestNull(), model.OptionalString{}, providerID),
			apply:    func() { provider["baseUrl"] = nil },
		},
	}
	for _, patch := range patches {
		response := writeModelProviderSettings(t, router, patch.settings)
		patch.apply()
		assertProviderWriteJSON(t, response, `{"provider":`+providerTestJSON(t, provider)+`}`)
	}

	persisted := readParsedConfigFile(t, home)
	providers, _ := persisted["model_providers"].(map[string]any)
	team, _ := providers["team-gateway"].(map[string]any)
	assertProviderWriteJSON(t, map[string]any{"provider": team}, `{"provider":{"name":"Latest","wire_api":"responses"}}`)

	list := modelProviderListResult(t, router)
	if list["defaultProviderId"] != "openai" {
		t.Fatalf("default provider = %#v, want openai", list["defaultProviderId"])
	}
}

func providerTestJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(data)
}

// Mirrors Rust #58876 custom_provider_edits_preserve_other_fields_and_respect_policy.
func TestModelProviderConfigWritePreservesFieldsAndRespectsPolicyLikeRust(t *testing.T) {
	home := t.TempDir()
	writeManagedProviderRequirements(t, home, `
model_provider = "corp.gateway"
[model_providers."corp.gateway"]
name = "Managed gateway"
base_url = "https://managed.example.test"
`)
	if err := os.WriteFile(config.ConfigPath(home), []byte(`
[model_providers.basic]
name = "Basic"
base_url = "https://user:password@basic.example.test/v1"
[model_providers.external]
name = "External"
base_url = "https://external.example.test"
http_headers = { Authorization = "Bearer test-only" }
query_params = { "api-version" = "v1" }
`), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	router := newManagedProviderRouter(t, home)

	saved := writeModelProviderSettings(t, router, model.ProviderSettingsPatch{
		Type:          "customGateway",
		CustomGateway: &model.CustomGatewaySettingsParams{ProviderID: providerTestString("external"), Name: providerTestString("Renamed")},
	})
	if saved["provider"].(map[string]any)["id"] != "external" {
		t.Fatalf("saved provider = %#v", saved)
	}
	providers, _ := readParsedConfigFile(t, home)["model_providers"].(map[string]any)
	external, _ := providers["external"].(map[string]any)
	assertProviderWriteJSON(t, map[string]any{"provider": external}, `{"provider":{
		"name":"Renamed",
		"base_url":"https://external.example.test",
		"http_headers":{"Authorization":"Bearer test-only"},
		"query_params":{"api-version":"v1"}
	}}`)

	cases := []struct {
		settings model.ProviderSettingsPatch
		code     string
	}{
		{customGatewaySettings("Changed", model.OptionalString{}, model.OptionalString{}, providerTestString("corp.gateway")), "configLayerReadonly"},
		{customGatewaySettings("Changed", model.OptionalString{}, model.OptionalString{}, providerTestString("openai")), "configValidationError"},
		{customGatewaySettings("", model.OptionalString{}, providerTestNull(), providerTestString("external")), "configValidationError"},
		{customGatewaySettings("", model.OptionalString{}, providerTestOptional("NEW_KEY"), providerTestString("basic")), "configValidationError"},
		{customGatewaySettings("", model.OptionalString{}, providerTestNull(), providerTestString("basic")), "configValidationError"},
		{customGatewaySettings("", model.OptionalString{}, model.OptionalString{}, providerTestString("ollama")), "configValidationError"},
		{customGatewaySettings("", model.OptionalString{}, model.OptionalString{}, providerTestString("gcp")), "configValidationError"},
	}
	for _, testCase := range cases {
		requireProviderWriteErrorCode(t, writeModelProviderSettingsError(t, router, testCase.settings), testCase.code)
	}
	after := readParsedConfigFile(t, home)
	assertProviderWriteJSON(t, map[string]any{"model_providers": after["model_providers"]}, `{"model_providers":{
		"basic":{"name":"Basic","base_url":"https://user:password@basic.example.test/v1"},
		"external":{
			"name":"Renamed",
			"base_url":"https://external.example.test",
			"http_headers":{"Authorization":"Bearer test-only"},
			"query_params":{"api-version":"v1"}
		}
	}}`)
}

// Mirrors Rust #58876 builtin_provider_patches_preserve_auth_and_do_not_select_or_authenticate.
func TestModelProviderConfigWriteBuiltinPatchesLikeRust(t *testing.T) {
	home := t.TempDir()
	writeManagedProviderRequirements(t, home, "model_provider = 'openai'\n")
	router := newManagedProviderRouter(t, home)
	credentialFile := home + string(os.PathSeparator) + "missing-credentials.json"

	bedrockSaved := writeModelProviderSettings(t, router, model.ProviderSettingsPatch{
		Type:    "bedrock",
		Bedrock: &model.BedrockSettingsParams{ProviderID: "amazon-bedrock", Profile: providerTestOptional("work"), Region: providerTestOptional("us-east-1")},
	})
	assertProviderWriteJSON(t, bedrockSaved, `{"provider":{"id":"amazon-bedrock","name":"Amazon Bedrock Mantle","canEdit":true,
		"baseUrl":null,"settings":{"type":"bedrock","profile":"work","region":"us-east-1"}}}`)

	runtimeSaved := writeModelProviderSettings(t, router, model.ProviderSettingsPatch{
		Type:    "bedrock",
		Bedrock: &model.BedrockSettingsParams{ProviderID: "amazon-bedrock-runtime", Profile: providerTestNull(), Region: providerTestOptional("us-west-2")},
	})
	assertProviderWriteJSON(t, runtimeSaved, `{"provider":{"id":"amazon-bedrock-runtime","name":"Amazon Bedrock Runtime","canEdit":true,
		"baseUrl":null,"settings":{"type":"bedrock","profile":null,"region":"us-west-2"}}}`)

	gcpSaved := writeModelProviderSettings(t, router, model.ProviderSettingsPatch{
		Type: "gcp",
		Gcp: &model.GcpSettingsParams{
			ProviderID:     "gcp",
			ProjectID:      providerTestOptional("test-project"),
			Location:       providerTestOptional("global"),
			CredentialFile: providerTestOptional(credentialFile),
		},
	})
	assertProviderWriteJSON(t, gcpSaved, `{"provider":{"id":"gcp","name":"Google Cloud Vertex AI","canEdit":true,
		"baseUrl":null,"settings":{"type":"gcp","projectId":"test-project","location":"global","credentialFile":`+providerTestJSON(t, credentialFile)+`}}}`)

	// Add authentication settings the gateway form does not expose.
	if err := os.WriteFile(config.ConfigPath(home), []byte(`
[model_providers.amazon-bedrock.aws]
profile = "work"
auth_refresh = { command = "aws", args = ["sso", "login"] }
[model_providers.amazon-bedrock-runtime.aws]
region = "us-west-2"
credential_export = { command = "unused-credential-command", args = ["test-only"] }
[model_providers.gcp]
project_id = "test-project"
location = "global"
credential_file = "`+strings.ReplaceAll(credentialFile, "\\", "\\\\")+`"
`), 0o600); err != nil {
		t.Fatalf("rewrite config.toml: %v", err)
	}

	writeModelProviderSettings(t, router, model.ProviderSettingsPatch{
		Type:    "bedrock",
		Bedrock: &model.BedrockSettingsParams{ProviderID: "amazon-bedrock", Region: providerTestNull()},
	})
	writeModelProviderSettings(t, router, model.ProviderSettingsPatch{
		Type:    "bedrock",
		Bedrock: &model.BedrockSettingsParams{ProviderID: "amazon-bedrock-runtime", Region: providerTestOptional("eu-west-1")},
	})
	writeModelProviderSettings(t, router, model.ProviderSettingsPatch{
		Type: "gcp",
		Gcp:  &model.GcpSettingsParams{ProviderID: "gcp", Location: providerTestOptional("us-central1"), CredentialFile: providerTestNull()},
	})
	writeModelProviderSettings(t, router, model.ProviderSettingsPatch{
		Type: "gcp",
		Gcp:  &model.GcpSettingsParams{ProviderID: "gcp"},
	})

	list := modelProviderListResult(t, router)
	if list["defaultProviderId"] != "openai" || list["canChangeDefault"] != false {
		t.Fatalf("provider list = %#v", list)
	}

	beforeRejections, err := os.ReadFile(config.ConfigPath(home))
	if err != nil {
		t.Fatal(err)
	}
	persisted := readParsedConfigFile(t, home)
	assertProviderWriteJSON(t, map[string]any{"model_providers": persisted["model_providers"]}, `{"model_providers":{
		"amazon-bedrock":{"aws":{"profile":"work","auth_refresh":{"command":"aws","args":["sso","login"]}}},
		"amazon-bedrock-runtime":{"aws":{"region":"eu-west-1","credential_export":{"command":"unused-credential-command","args":["test-only"]}}},
		"gcp":{"project_id":"test-project","location":"us-central1"}
	}}`)

	rejected := []model.ProviderSettingsPatch{
		{Type: "bedrock", Bedrock: &model.BedrockSettingsParams{ProviderID: "amazon-bedrock-runtime", Profile: providerTestOptional("conflicts-with-command"), Region: providerTestOptional("must-not-save")}},
		{Type: "gcp", Gcp: &model.GcpSettingsParams{ProviderID: "gcp", CredentialFile: providerTestOptional("relative/path"), Location: providerTestOptional("must-not-save")}},
	}
	for _, settings := range rejected {
		requireProviderWriteErrorCode(t, writeModelProviderSettingsError(t, router, settings), "configValidationError")
	}
	afterRejections, err := os.ReadFile(config.ConfigPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if string(afterRejections) != string(beforeRejections) {
		t.Fatalf("rejected writes changed config.toml\nbefore: %s\nafter: %s", beforeRejections, afterRejections)
	}
}

// Mirrors Rust #58876 wire validation: mismatched provider ids and unknown
// fields are invalid requests, not configuration errors.
func TestModelProviderConfigWriteRejectsMismatchedParamsLikeRust(t *testing.T) {
	home := t.TempDir()
	router := newManagedProviderRouter(t, home)
	params := []string{
		`{"settings":{"type":"bedrock","providerId":"gcp","region":"must-not-save"}}`,
		`{"settings":{"type":"gcp","providerId":"amazon-bedrock","location":"must-not-save"}}`,
		`{"settings":{"type":"bedrock","providerId":"amazon-bedrock","envKey":"UNSUPPORTED"}}`,
		`{"settings":{"type":"gcp","providerId":"gcp","profile":"wrong-provider-field"}}`,
		`{"settings":{"type":"gcp","location":"missing-id"}}`,
		`{"settings":{"type":"customGateway","name":null,"baseUrl":"https://must-not-save.example.test"}}`,
	}
	for _, raw := range params {
		responseErr := writeModelProviderRawError(t, router, raw)
		if responseErr.Code != JSONRPCInvalidRequestErrorCode {
			t.Fatalf("params %s error = %+v, want invalid request", raw, responseErr)
		}
	}
	if _, err := os.Stat(config.ConfigPath(home)); err == nil {
		if data, readErr := os.ReadFile(config.ConfigPath(home)); readErr == nil && len(data) > 0 {
			t.Fatalf("invalid params wrote config.toml: %s", data)
		}
	}
}

// Provider creation rejects reserved built-in names and requires a base URL.
func TestModelProviderConfigWriteValidatesCustomGatewaysLikeRust(t *testing.T) {
	home := t.TempDir()
	router := newManagedProviderRouter(t, home)
	cases := []model.ProviderSettingsPatch{
		customGatewaySettings("OpenAI", providerTestOptional("https://new.example.test"), model.OptionalString{}, nil),
		customGatewaySettings(" Amazon Bedrock ", providerTestOptional("https://new.example.test"), model.OptionalString{}, nil),
		customGatewaySettings("Team Gateway", model.OptionalString{}, model.OptionalString{}, nil),
		customGatewaySettings("Team Gateway", providerTestOptional("https://user:password@gateway.example.test"), model.OptionalString{}, nil),
		customGatewaySettings("Team Gateway", providerTestOptional("https://gateway.example.test?v=1"), model.OptionalString{}, nil),
		customGatewaySettings("Team Gateway", providerTestOptional("https://gateway.example.test"), providerTestOptional("9BAD"), nil),
	}
	for _, settings := range cases {
		requireProviderWriteErrorCode(t, writeModelProviderSettingsError(t, router, settings), "configValidationError")
	}
}

// Saving the provider model/list is using invalidates the catalog; edits to
// other providers leave it alone.
func TestModelProviderConfigWriteInvalidatesActiveProviderCatalogLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte(`
model_provider = "team"
[model_providers.team]
name = "Team"
base_url = "https://team.example.test"
`), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	router := newManagedProviderRouter(t, home)
	if router.modelCatalogRefreshPending() {
		t.Fatal("fresh router already reports a pending catalog refresh")
	}
	writeModelProviderSettings(t, router, customGatewaySettings("Other", providerTestOptional("https://other.example.test"), model.OptionalString{}, nil))
	if router.modelCatalogRefreshPending() {
		t.Fatal("editing a non-active provider invalidated the catalog")
	}
	writeModelProviderSettings(t, router, customGatewaySettings("Team Renamed", model.OptionalString{}, model.OptionalString{}, providerTestString("team")))
	if !router.modelCatalogRefreshPending() {
		t.Fatal("editing the active provider did not invalidate the catalog")
	}
}
