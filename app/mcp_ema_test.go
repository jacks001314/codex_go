package app

import "testing"

// TestMCPConfigValueRoundTripsEMAAuthorizationServerIssuer mirrors Rust
// blocking_replace_mcp_servers_round_trips (#44832): an ema_auth server
// serializes `auth = "ema_auth"` and its resource authorization server issuer
// inside the oauth table, and reloads identically.
func TestMCPConfigValueRoundTripsEMAAuthorizationServerIssuer(t *testing.T) {
	server := &mcpCLIServer{
		Name:                           "enterprise",
		Type:                           "streamable_http",
		Enabled:                        true,
		URL:                            "https://resource.example/mcp",
		Auth:                           "ema_auth",
		OAuthClientID:                  "resource-client",
		OAuthAuthorizationServerIssuer: "https://as.example",
		OAuthResource:                  "https://resource.example",
		Scopes:                         []string{"tools"},
	}
	value := mcpServerToConfigValue(server)
	if value["auth"] != "ema_auth" {
		t.Fatalf("auth = %#v, want ema_auth", value["auth"])
	}
	oauth, ok := value["oauth"].(map[string]any)
	if !ok {
		t.Fatalf("oauth = %#v", value["oauth"])
	}
	if oauth["client_id"] != "resource-client" || oauth["authorization_server_issuer"] != "https://as.example" {
		t.Fatalf("oauth = %#v", oauth)
	}
	parsed := mcpServerFromConfigValue("enterprise", value)
	if parsed.Auth != "ema_auth" || parsed.OAuthAuthorizationServerIssuer != "https://as.example" || parsed.OAuthClientID != "resource-client" {
		t.Fatalf("parsed = %#v", parsed)
	}
}
