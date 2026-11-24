package config

import (
	"strings"
	"testing"
)

// Rust #63476 shares one OAuth table between the config server and the gateway
// provider, so the credentials-store choice must survive on both destinations.
func TestConfigServerOAuthCredentialsStoreLikeRust(t *testing.T) {
	requirements, err := ParseRequirementsTOML([]byte(`
[config_server]
base_url = "https://gateway.example.com"

[config_server.oauth]
authorization_url = "https://gateway.example.com/authorize"
token_url = "https://gateway.example.com/token"
client_id = "acme-client"
credentials_store = "file"
`))
	if err != nil {
		t.Fatalf("ParseRequirementsTOML() error = %v", err)
	}
	if requirements.ConfigServer == nil || requirements.ConfigServer.OAuth == nil || requirements.ConfigServer.OAuth.CredentialsStore == nil {
		t.Fatalf("config server oauth = %#v", requirements.ConfigServer)
	}
	if got := *requirements.ConfigServer.OAuth.CredentialsStore; got != "file" {
		t.Fatalf("credentials_store = %q, want file", got)
	}

	cloned := cloneConfigServer(requirements.ConfigServer)
	if cloned == nil || cloned.OAuth == nil || cloned.OAuth.CredentialsStore == nil || *cloned.OAuth.CredentialsStore != "file" {
		t.Fatalf("cloned config server oauth = %#v", cloned)
	}
	*cloned.OAuth.CredentialsStore = "keyring"
	if got := *requirements.ConfigServer.OAuth.CredentialsStore; got != "file" {
		t.Fatalf("clone aliased the credentials store: %q", got)
	}
}

func TestConfigServerOAuthCredentialsStoreRejectsUnknownLikeRust(t *testing.T) {
	_, err := ParseRequirementsTOML([]byte(`
[config_server]
base_url = "https://gateway.example.com"

[config_server.oauth]
authorization_url = "https://gateway.example.com/authorize"
token_url = "https://gateway.example.com/token"
client_id = "acme-client"
credentials_store = "vault"
`))
	if err == nil || !strings.Contains(err.Error(), "invalid value for credentials_store") {
		t.Fatalf("error = %v, want the credentials-store diagnostic", err)
	}
}

func TestGatewayEnrollmentSharesCredentialsStoreLikeRust(t *testing.T) {
	requirements, err := ParseRequirementsTOML([]byte(`
[gateway]
base_url = "https://gateway.example.com"

[gateway.auth]
authorization_url = "https://gateway.example.com/authorize"
token_url = "https://gateway.example.com/token"
client_id = "acme-client"
credentials_store = "file"
`))
	if err != nil {
		t.Fatalf("ParseRequirementsTOML() error = %v", err)
	}
	if requirements.ConfigServer == nil || requirements.ConfigServer.OAuth == nil || requirements.ConfigServer.OAuth.CredentialsStore == nil {
		t.Fatalf("config server oauth = %#v", requirements.ConfigServer)
	}
	if got := *requirements.ConfigServer.OAuth.CredentialsStore; got != "file" {
		t.Fatalf("config server credentials_store = %q, want file", got)
	}
	provider, ok := requirements.ModelProviders["gateway"].(map[string]any)
	if !ok {
		t.Fatalf("model providers = %#v", requirements.ModelProviders)
	}
	oauth, ok := provider["gateway_oauth"].(map[string]any)
	if !ok || oauth["credentials_store"] != "file" {
		t.Fatalf("gateway oauth = %#v", provider["gateway_oauth"])
	}
}
