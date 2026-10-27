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

// TestEMAConfidentialClientConfigValidationLikeRust mirrors #56001: a
// confidential EMA registration needs a non-empty secret and a pinned
// authorization server issuer, its authentication method needs the secret,
// and the method is meaningless outside ema_auth.
func TestEMAConfidentialClientConfigValidationLikeRust(t *testing.T) {
	base := func() *ServerConfig {
		return &ServerConfig{
			URL:                            "https://mcp.example.test/mcp",
			Auth:                           ServerAuthEMAAuth,
			EMAConfigured:                  true,
			OAuthClientID:                  "mcp-client",
			OAuthAuthorizationServerIssuer: "https://as.example",
		}
	}
	if err := ValidateServerAuth("enterprise", base()); err != nil {
		t.Fatalf("public EMA server rejected: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*ServerConfig)
		want   string
	}{
		{
			name:   "confidential without issuer",
			mutate: func(c *ServerConfig) { c.OAuthClientSecret = "s3cret"; c.OAuthAuthorizationServerIssuer = "" },
			want:   "confidential ema_auth requires oauth.authorization_server_issuer",
		},
		{
			name:   "whitespace-only secret",
			mutate: func(c *ServerConfig) { c.OAuthClientSecret = "   " },
			want:   "ema_auth oauth.client_secret must not be empty",
		},
		{
			name:   "method without secret",
			mutate: func(c *ServerConfig) { c.OAuthTokenEndpointAuthMethod = "client_secret_basic" },
			want:   "oauth.token_endpoint_auth_method requires oauth.client_secret",
		},
		{
			name: "unknown method",
			mutate: func(c *ServerConfig) {
				c.OAuthClientSecret = "s3cret"
				c.OAuthTokenEndpointAuthMethod = "private_key_jwt"
			},
			want: "unknown value",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config := base()
			tc.mutate(config)
			if err := ValidateServerAuth("enterprise", config); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
	nonEnterprise := &ServerConfig{
		URL:                          "https://mcp.example.test/mcp",
		OAuthClientID:                "client",
		OAuthClientSecret:            "s3cret",
		OAuthTokenEndpointAuthMethod: "client_secret_basic",
	}
	if err := ValidateServerAuth("docs", nonEnterprise); err == nil || !strings.Contains(err.Error(), `requires auth = "ema_auth"`) {
		t.Fatalf("non-EMA method error = %v", err)
	}
}
