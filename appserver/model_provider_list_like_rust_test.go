package appserver

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"codex_go/config"
	"codex_go/model"
)

// modelProviderListResult drives modelProvider/list through the router and
// returns the wire shape a client would see.
func modelProviderListResult(t *testing.T, router *RuntimeRouter) map[string]any {
	t.Helper()
	response := router.Handle(requestWithParams(t, IntID(1), MethodModelProviderList, model.ProviderListParams{}))
	if response.Error != nil {
		t.Fatalf("modelProvider/list error = %+v", response.Error)
	}
	raw, err := json.Marshal(response.Result)
	if err != nil {
		t.Fatalf("marshal modelProvider/list result: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode modelProvider/list result: %v", err)
	}
	return decoded
}

func assertModelProviderListEqual(t *testing.T, got map[string]any, want string) {
	t.Helper()
	var expected map[string]any
	if err := json.Unmarshal([]byte(want), &expected); err != nil {
		t.Fatalf("decode expected modelProvider/list JSON: %v", err)
	}
	if !reflect.DeepEqual(got, expected) {
		gotJSON, _ := json.MarshalIndent(got, "", "  ")
		wantJSON, _ := json.MarshalIndent(expected, "", "  ")
		t.Fatalf("modelProvider/list mismatch\ngot:  %s\nwant: %s", gotJSON, wantJSON)
	}
}

// Mirrors Rust #58845 list_without_provider_definitions_only_returns_openai.
func TestModelProviderListWithoutDefinitionsReturnsOnlyOpenAI(t *testing.T) {
	cases := []struct {
		config    string
		defaultID string
		baseURL   string
	}{
		{config: "", defaultID: "openai", baseURL: "null"},
		{config: "model_provider = 'ollama'", defaultID: "ollama", baseURL: "null"},
		{
			config:    "openai_base_url = 'https://proxy.example.test/v1'",
			defaultID: "openai",
			baseURL:   `"https://proxy.example.test/v1"`,
		},
		{
			config:    "openai_base_url = 'https://proxy.example.test/v1?token=test-only'",
			defaultID: "openai",
			baseURL:   "null",
		},
	}
	for _, testCase := range cases {
		home := t.TempDir()
		if err := os.WriteFile(config.ConfigPath(home), []byte(testCase.config), 0o600); err != nil {
			t.Fatalf("write config.toml: %v", err)
		}
		router := newManagedProviderRouter(t, home)
		got := modelProviderListResult(t, router)
		assertModelProviderListEqual(t, got, `{
			"data": [{"id":"openai", "name":"OpenAI", "canEdit":false,
				"baseUrl":`+testCase.baseURL+`, "settings":{"type":"openai"}}],
			"defaultProviderId":"`+testCase.defaultID+`", "canChangeDefault":true
		}`)
	}
}

// Mirrors Rust #58845 list_builtin_and_custom_providers_with_editable_settings.
func TestModelProviderListBuiltinAndCustomProvidersLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte(`
[model_providers.amazon-bedrock]
base_url = "https://bedrock.example.test/v1"
[model_providers.amazon-bedrock.aws]
profile = "work"
region = "us-east-1"
[model_providers.amazon-bedrock-runtime.aws]
region = "us-east-1"
[model_providers.gcp]
[model_providers.basic]
name = "Basic auth gateway"
base_url = "https://user:password@basic.example.test/v1"
[model_providers.external]
name = "External gateway"
base_url = "https://external.example.test/v1?token=test-only"
env_key = "EXTERNAL_KEY"
http_headers = { Authorization = "Bearer test-only" }
[model_providers.query]
name = "Query gateway"
base_url = "https://query.example.test/v1"
query_params = { api_key = "test-only-query-key" }
[model_providers.team]
name = "Team Gateway"
base_url = "https://gateway.example.test/v1"
env_key = "TEAM_KEY"
`), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	writeManagedProviderRequirements(t, home, `
model_provider = "corp.gateway"
[model_providers."corp.gateway"]
name = "Managed gateway"
base_url = "https://managed.example.test"
`)
	router := newManagedProviderRouter(t, home)
	got := modelProviderListResult(t, router)
	assertModelProviderListEqual(t, got, `{
		"defaultProviderId":"corp.gateway", "canChangeDefault":false,
		"data": [
			{
				"id":"amazon-bedrock", "name":"Amazon Bedrock Mantle", "canEdit":true,
				"baseUrl":"https://bedrock.example.test/v1", "settings":{"type":"bedrock", "profile":"work", "region":"us-east-1"}
			},
			{
				"id":"amazon-bedrock-runtime", "name":"Amazon Bedrock Runtime", "canEdit":true,
				"baseUrl":null, "settings":{"type":"bedrock", "profile":null, "region":"us-east-1"}
			},
			{
				"id":"basic", "name":"Basic auth gateway", "canEdit":true, "baseUrl":null,
				"settings":{"type":"customGateway", "auth":{"mode":"externallyConfigured", "envKey":null}}
			},
			{
				"id":"corp.gateway", "name":"Managed gateway", "canEdit":false,
				"baseUrl":"https://managed.example.test", "settings":{"type":"customGateway", "auth":{"mode":"none"}}
			},
			{
				"id":"external", "name":"External gateway", "canEdit":true,
				"baseUrl":null, "settings":{"type":"customGateway", "auth":{"mode":"externallyConfigured", "envKey":"EXTERNAL_KEY"}}
			},
			{
				"id":"gcp", "name":"Google Cloud Vertex AI", "canEdit":true,
				"baseUrl":null, "settings":{"type":"gcp", "projectId":null, "location":null, "credentialFile":null}
			},
			{
				"id":"openai", "name":"OpenAI", "canEdit":false,
				"baseUrl":null, "settings":{"type":"openai"}
			},
			{
				"id":"query", "name":"Query gateway", "canEdit":true,
				"baseUrl":"https://query.example.test/v1", "settings":{"type":"customGateway", "auth":{"mode":"externallyConfigured", "envKey":null}}
			},
			{
				"id":"team", "name":"Team Gateway", "canEdit":true,
				"baseUrl":"https://gateway.example.test/v1", "settings":{"type":"customGateway", "auth":{"mode":"environmentVariable", "envKey":"TEAM_KEY"}}
			}
		]
	}`)
}

// A managed provider definition without a managed selection keeps the user's
// default changeable while the managed definition itself stays read-only.
func TestModelProviderListManagedDefinitionKeepsDefaultEditableLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte(`
model_provider = "team"
[model_providers.team]
name = "Team Gateway"
base_url = "https://gateway.example.test/v1"
env_key = "TEAM_KEY"
`), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	writeManagedProviderRequirements(t, home, `
[model_providers."corp.gateway"]
name = "Managed gateway"
base_url = "https://managed.example.test"
`)
	router := newManagedProviderRouter(t, home)
	got := modelProviderListResult(t, router)
	assertModelProviderListEqual(t, got, `{
		"defaultProviderId":"team", "canChangeDefault":true,
		"data": [
			{
				"id":"corp.gateway", "name":"Managed gateway", "canEdit":false,
				"baseUrl":"https://managed.example.test", "settings":{"type":"customGateway", "auth":{"mode":"none"}}
			},
			{
				"id":"openai", "name":"OpenAI", "canEdit":false,
				"baseUrl":null, "settings":{"type":"openai"}
			},
			{
				"id":"team", "name":"Team Gateway", "canEdit":true,
				"baseUrl":"https://gateway.example.test/v1", "settings":{"type":"customGateway", "auth":{"mode":"environmentVariable", "envKey":"TEAM_KEY"}}
			}
		]
	}`)
}
