package mcp

import (
	"strings"
	"testing"
)

func TestRuntimeConfigFromValuesParsesEMAAuthorizationServerIssuer(t *testing.T) {
	runtime := RuntimeConfigFromValues(map[string]any{
		"mcp_servers": map[string]any{
			"enterprise": map[string]any{
				"url":            "https://resource.example/mcp",
				"auth":           ServerAuthEMAAuth,
				"scopes":         []any{"files.read"},
				"oauth_resource": "https://resource.example/mcp",
				"oauth": map[string]any{
					"client_id":                   "mcp-client",
					"authorization_server_issuer": "https://as.example",
				},
			},
		},
	}, "")
	config := runtime.Servers["enterprise"].Config
	if config.EffectiveAuth() != ServerAuthEMAAuth {
		t.Fatalf("auth = %q, want %q", config.EffectiveAuth(), ServerAuthEMAAuth)
	}
	if config.OAuthClientID != "mcp-client" || config.OAuthAuthorizationServerIssuer != "https://as.example" {
		t.Fatalf("oauth client/issuer = %q/%q", config.OAuthClientID, config.OAuthAuthorizationServerIssuer)
	}
}

func TestValidateEMAAuthTransportRejectsAlternateCredentials(t *testing.T) {
	base := func() *ServerConfig {
		return &ServerConfig{URL: "https://resource.example", Auth: ServerAuthEMAAuth}
	}
	for name, mutate := range map[string]func(*ServerConfig){
		"bearer":             func(c *ServerConfig) { c.BearerTokenEnvVar = "TOKEN" },
		"authorization":      func(c *ServerConfig) { c.HTTPHeaders = map[string]string{"Authorization": "secret"} },
		"accept":             func(c *ServerConfig) { c.HTTPHeaders = map[string]string{"Accept": "application/json"} },
		"env-header":         func(c *ServerConfig) { c.EnvHTTPHeaders = map[string]string{"X-Key": "TOKEN"} },
		"http-header-helper": func(c *ServerConfig) { c.HTTPHeadersHelper = "get-headers" },
		"executor": func(c *ServerConfig) {
			c.EnvironmentID = "remote"
		},
		"stdio": func(c *ServerConfig) {
			c.URL = ""
			c.Command = "server"
		},
	} {
		candidate := base()
		mutate(candidate)
		if err := candidate.ValidateEMAAuthTransport(); err == nil {
			t.Fatalf("%s: expected EMA transport validation error", name)
		}
	}
	ok := base()
	ok.HTTPHeaders = map[string]string{}
	if err := ok.ValidateEMAAuthTransport(); err != nil {
		t.Fatalf("empty http_headers should be accepted: %v", err)
	}
}

func TestValidateServerAuthRejectsEMAConnections(t *testing.T) {
	config := &ServerConfig{URL: "https://resource.example", Auth: ServerAuthEMAAuth}
	err := ValidateServerAuth("enterprise", config)
	if err == nil || !strings.Contains(err.Error(), "not enabled in this version") {
		t.Fatalf("ValidateServerAuth(ema) error = %v", err)
	}
}

func TestAuthStatusForConfigReportsEMAUnsupported(t *testing.T) {
	service := &MCPService{}
	status := service.authStatusForConfig("enterprise", &ServerConfig{URL: "https://resource.example", Auth: ServerAuthEMAAuth})
	if status != MCPAuthUnsupported {
		t.Fatalf("auth status = %q, want %q", status, MCPAuthUnsupported)
	}
}

func TestOauthLoginRejectsEMAConnections(t *testing.T) {
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"enterprise": {
			Name: "enterprise",
			Config: ServerConfig{
				URL:     "https://resource.example/mcp",
				Auth:    ServerAuthEMAAuth,
				Enabled: true,
			},
		},
	}})
	_, err := service.OauthLogin(&MCPServerOauthLoginParams{Name: "enterprise"})
	if err == nil || !strings.Contains(err.Error(), "not enabled in this version") {
		t.Fatalf("OauthLogin(ema) error = %v", err)
	}
}
