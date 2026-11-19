package mcp

import "testing"

func TestServerConfigPermitsResultBodyLikeRust(t *testing.T) {
	apps := CodexAppsServerConfig("https://chatgpt.com/backend-api/", "sku-1", &RuntimeAuth{UsesCodexBackend: true})
	apps.Auth = McpServerAuthChatGPT
	runtimeAuth := &RuntimeAuth{UsesCodexBackend: true}
	if !apps.PermitsResultBody(SessionAuthChatGPT, runtimeAuth) {
		t.Fatal("host-owned apps server did not permit its result body")
	}
	if !apps.PermitsResultBody(SessionAuthChatGPTAuthTokens, runtimeAuth) {
		t.Fatal("chatgpt auth tokens did not permit the result body")
	}
	if apps.PermitsResultBody(SessionAuthAPIKey, runtimeAuth) {
		t.Fatal("an API-key session permitted an Apps result body")
	}
	if apps.PermitsResultBody(SessionAuthChatGPT, nil) {
		t.Fatal("a session without runtime credentials permitted an Apps result body")
	}
	if apps.PermitsResultBody(SessionAuthChatGPT, &RuntimeAuth{}) {
		t.Fatal("a non-backend runtime credential permitted an Apps result body")
	}

	cases := map[string]func(*ServerConfig){
		"stdio command":        func(c *ServerConfig) { c.Command = "node"; c.URL = "" },
		"empty url":            func(c *ServerConfig) { c.URL = "" },
		"static bearer":        func(c *ServerConfig) { c.BearerTokenEnvVar = "APPS_TOKEN" },
		"header helper":        func(c *ServerConfig) { c.HTTPHeadersHelper = "/opt/headers" },
		"api key auth":         func(c *ServerConfig) { c.Auth = "apikey" },
		"non attribution head": func(c *ServerConfig) { c.HTTPHeaders = map[string]string{"X-Custom": "1"} },
		"non attribution env":  func(c *ServerConfig) { c.EnvHTTPHeaders = map[string]string{"Authorization": "TOKEN"} },
	}
	for name, mutate := range cases {
		server := apps
		mutate(&server)
		if server.PermitsResultBody(SessionAuthChatGPT, runtimeAuth) {
			t.Fatalf("%s: PermitsResultBody = true, want false", name)
		}
	}

	attributionOnly := apps
	attributionOnly.HTTPHeaders = map[string]string{"originator": "codex_cli_rs", "X-OpenAI-Product-SKU": "sku-1"}
	attributionOnly.EnvHTTPHeaders = map[string]string{"ORIGINATOR": "ORIGINATOR_ENV"}
	if !attributionOnly.PermitsResultBody(SessionAuthChatGPT, runtimeAuth) {
		t.Fatal("attribution-only headers rejected")
	}
}
