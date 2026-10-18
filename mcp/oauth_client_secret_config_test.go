package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOAuthClientSecretRequiresClientIDLikeRust(t *testing.T) {
	config := &ServerConfig{URL: "https://mcp.example.test/mcp", OAuthClientSecret: "secret"}
	err := ValidateServerAuth("docs", config)
	if err == nil || !strings.Contains(err.Error(), "oauth.client_secret requires oauth.client_id") {
		t.Fatalf("ValidateServerAuth() error = %v", err)
	}
	config.OAuthClientID = "client"
	if err := ValidateServerAuth("docs", config); err != nil {
		t.Fatalf("ValidateServerAuth(client) error = %v", err)
	}
}

func TestOAuthClientSecretRoundTripsInServerConfigLikeRust(t *testing.T) {
	config := ServerConfig{
		URL:               "https://mcp.example.test/mcp",
		OAuthClientID:     "client",
		OAuthClientSecret: "secret",
		Enabled:           true,
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var decoded ServerConfig
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if decoded.OAuthClientSecret != "secret" {
		t.Fatalf("decoded secret = %q", decoded.OAuthClientSecret)
	}
}
