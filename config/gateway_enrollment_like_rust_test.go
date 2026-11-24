package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Rust #63476: a local `[gateway]` enrollment stands for one config server at
// the gateway root and a required Responses provider at `/v1` (model discovery
// at `/v1/models`), both authenticated with the shared OAuth settings.
func TestGatewayEnrollmentExpandsRequirementsLikeRust(t *testing.T) {
	requirements, err := ParseRequirementsTOML([]byte(`
[gateway]
name = "Acme Gateway"
base_url = "https://gateway.example.com/"

[gateway.auth]
authorization_url = "https://gateway.example.com/authorize"
token_url = "https://gateway.example.com/token"
client_id = "acme-client"
scopes = ["managed-config"]
redirect_port = 1455
`))
	if err != nil {
		t.Fatalf("ParseRequirementsTOML() error = %v", err)
	}
	if requirements == nil || requirements.ConfigServer == nil {
		t.Fatalf("config server = %#v", requirements)
	}
	if !requirements.RequiresRemoteConfig() {
		t.Fatal("RequiresRemoteConfig() = false")
	}
	if got := requirements.ConfigServer.BaseURL; got != "https://gateway.example.com" {
		t.Fatalf("config server base = %q", got)
	}
	if requirements.ConfigServer.OAuth == nil || requirements.ConfigServer.OAuth.ClientID != "acme-client" {
		t.Fatalf("config server oauth = %#v", requirements.ConfigServer.OAuth)
	}
	if requirements.ModelProvider == nil || *requirements.ModelProvider != "gateway" {
		t.Fatalf("model provider = %#v", requirements.ModelProvider)
	}
	provider, ok := requirements.ModelProviders["gateway"].(map[string]any)
	if !ok {
		t.Fatalf("model providers = %#v", requirements.ModelProviders)
	}
	if provider["name"] != "Acme Gateway" || provider["base_url"] != "https://gateway.example.com/v1" {
		t.Fatalf("provider = %#v", provider)
	}
	if provider["model_catalog_url"] != "https://gateway.example.com/v1/models" || provider["wire_api"] != "responses" {
		t.Fatalf("provider = %#v", provider)
	}
	if provider["requires_openai_auth"] != false {
		t.Fatalf("requires_openai_auth = %#v", provider["requires_openai_auth"])
	}
	oauth, ok := provider["gateway_oauth"].(map[string]any)
	if !ok || oauth["authorization_url"] != "https://gateway.example.com/authorize" || oauth["client_id"] != "acme-client" {
		t.Fatalf("gateway oauth = %#v", provider["gateway_oauth"])
	}
}

// The provider name defaults to the gateway role and the shared auth table also
// feeds the provider's gateway_oauth settings.
func TestGatewayEnrollmentDefaultsNameLikeRust(t *testing.T) {
	requirements, err := ParseRequirementsTOML([]byte(`
[gateway]
base_url = "https://gateway.example.com"

[gateway.auth]
authorization_url = "https://gateway.example.com/authorize"
token_url = "https://gateway.example.com/token"
client_id = "acme-client"
credentials_store = "keyring"
`))
	if err != nil {
		t.Fatalf("ParseRequirementsTOML() error = %v", err)
	}
	provider := requirements.ModelProviders["gateway"].(map[string]any)
	if provider["name"] != "Gateway" {
		t.Fatalf("provider name = %#v", provider["name"])
	}
	oauth := provider["gateway_oauth"].(map[string]any)
	if oauth["credentials_store"] != "keyring" {
		t.Fatalf("gateway oauth = %#v", oauth)
	}
}

func TestGatewayEnrollmentRejectionsLikeRust(t *testing.T) {
	const authTable = `
[gateway.auth]
authorization_url = "https://gateway.example.com/authorize"
token_url = "https://gateway.example.com/token"
client_id = "acme-client"
`
	cases := map[string]struct{ document, want string }{
		"reserved azure name": {
			document: "[gateway]\nname = \"Azure\"\nbase_url = \"https://gateway.example.com\"\n" + authTable,
			want:     "non-reserved provider name",
		},
		"reserved builtin name": {
			document: "[gateway]\nname = \"OpenAI\"\nbase_url = \"https://gateway.example.com\"\n" + authTable,
			want:     "non-reserved provider name",
		},
		"plain http": {
			document: "[gateway]\nbase_url = \"http://gateway.example.com\"\n" + authTable,
			want:     "gateway",
		},
		"missing base url": {
			document: "[gateway]\n" + authTable,
			want:     "missing field `base_url`",
		},
		"missing auth": {
			document: "[gateway]\nbase_url = \"https://gateway.example.com\"\n",
			want:     "missing field `auth`",
		},
		"unknown field": {
			document: "[gateway]\nbase_url = \"https://gateway.example.com\"\nextra = true\n" + authTable,
			want:     "unknown field `extra`",
		},
		"conflicting config server": {
			document: "[config_server]\nbase_url = \"https://config.example.com\"\n\n[gateway]\nbase_url = \"https://gateway.example.com\"\n" + authTable,
			want:     "supplies config_server",
		},
		"conflicting model provider": {
			document: "model_provider = \"other\"\n\n[gateway]\nbase_url = \"https://gateway.example.com\"\n" + authTable,
			want:     "supplies model_provider",
		},
		"conflicting gateway provider": {
			document: "[model_providers.gateway]\nname = \"Existing\"\nbase_url = \"https://existing.example.com/v1\"\n\n[gateway]\nbase_url = \"https://gateway.example.com\"\n" + authTable,
			want:     "supplies model_providers.gateway",
		},
	}
	for name, tc := range cases {
		if _, err := ParseRequirementsTOML([]byte(tc.document)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: error = %v, want %q", name, err, tc.want)
		}
	}
}

// Rust #63476: fetched policy may never enroll a gateway, because the
// shorthand selects the config server the machine trusts.
func TestCloudManagedGatewayRejectedLikeRust(t *testing.T) {
	const contents = "[gateway]\nbase_url = \"https://attacker.example.com\"\n\n[gateway.auth]\n" +
		"authorization_url = \"https://attacker.example.com/authorize\"\n" +
		"token_url = \"https://attacker.example.com/token\"\nclient_id = \"x\"\n"
	bundle := CloudConfigBundle{
		RequirementsTOML: CloudConfigRequirementsTOMLBundle{EnterpriseManaged: []CloudConfigFragment{{
			ID:       "remote",
			Name:     "remote",
			Contents: contents,
		}}},
	}
	merged, err := applyCloudConfigBundle(map[string]any{}, nil, bundle, t.TempDir())
	if !errors.Is(err, ErrInvalidCloudConfig) || !strings.Contains(err.Error(), "gateway may only be configured in local managed requirements") {
		t.Fatalf("applyCloudConfigBundle() error = %v", err)
	}
	if merged != nil {
		t.Fatalf("applyCloudConfigBundle() requirements = %#v, want nil", merged)
	}
}

// The expansion reaches the effective configuration: a machine-level
// requirements.toml enrollment selects the gateway provider and its definition.
func TestGatewayEnrollmentReachesEffectiveConfigLikeRust(t *testing.T) {
	home := t.TempDir()
	body := "[gateway]\nbase_url = \"https://gateway.example.com\"\n\n[gateway.auth]\n" +
		"authorization_url = \"https://gateway.example.com/authorize\"\n" +
		"token_url = \"https://gateway.example.com/token\"\nclient_id = \"acme-client\"\n"
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write requirements.toml: %v", err)
	}
	cfg, err := Load(home)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.Values["model_provider"]; got != "gateway" {
		t.Fatalf("model_provider = %#v", got)
	}
	providers, ok := cfg.Values["model_providers"].(map[string]any)
	if !ok {
		t.Fatalf("model_providers = %#v", cfg.Values["model_providers"])
	}
	provider, ok := providers["gateway"].(map[string]any)
	if !ok || provider["base_url"] != "https://gateway.example.com/v1" {
		t.Fatalf("gateway provider = %#v", providers["gateway"])
	}
}
