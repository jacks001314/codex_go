package mcp

import (
	"reflect"
	"strings"
	"testing"
)

func TestManagerRuntimeConfigAppliesBuiltInsAndOverlays(t *testing.T) {
	manager := NewManager(map[string]ServerRegistration{
		"base": {Name: "base", Config: ServerConfig{Command: "base", Enabled: true}},
	})
	runtime := manager.RuntimeConfig(RuntimeConfig{
		AppsEnabled:    true,
		ChatGPTBaseURL: "https://chatgpt.test",
		Servers: map[string]ServerRegistration{
			"configured": {Config: ServerConfig{Command: "configured", Enabled: true}},
		},
	}, []ConfigOverlay{
		{Name: "configured", Remove: true, ContributionOrder: 2},
		{Name: "extension", Config: ServerConfig{Command: "extension", Enabled: true}, ContributorID: "ext", ContributionOrder: 1},
	})
	if _, ok := runtime.Servers["base"]; !ok {
		t.Fatalf("base server missing")
	}
	if _, ok := runtime.Servers["configured"]; ok {
		t.Fatalf("configured server present after remove overlay")
	}
	if runtime.Servers["extension"].ContributorID != "ext" {
		t.Fatalf("extension registration = %#v", runtime.Servers["extension"])
	}
	if runtime.Servers[CodexAppsServerName].Config.Env["CHATGPT_BASE_URL"] != "https://chatgpt.test" {
		t.Fatalf("codex apps server env = %#v", runtime.Servers[CodexAppsServerName].Config.Env)
	}
}

func TestRuntimeConfigFromValuesParsesMCPServersAndAppsFeature(t *testing.T) {
	runtime := RuntimeConfigFromValues(map[string]any{
		"chatgpt_base_url":     "https://chatgpt.test",
		"apps_mcp_product_sku": "tpp",
		"features": map[string]any{
			"apps": false,
		},
		"mcp_servers": map[string]any{
			"docs": map[string]any{
				"command": "mcp-docs",
				"args":    []any{"--repo", "codex"},
				"env": map[string]any{
					"DOCS_TOKEN": "secret",
				},
				"required": true,
			},
			"remote": map[string]any{
				"url":                  "https://mcp.example.test",
				"bearer_token_env_var": "MCP_TOKEN",
				"oauth_client_id":      "client-1",
				"oauth_resource":       "https://mcp.example.test",
				"enabled":              false,
			},
			"nested-oauth": map[string]any{
				"url": "https://nested.example.test",
				"oauth": map[string]any{
					"client_id": "client-nested",
				},
			},
		},
	}, "/codex/home")
	if runtime.AppsEnabled {
		t.Fatalf("AppsEnabled = true, want false")
	}
	if runtime.ChatGPTBaseURL != "https://chatgpt.test" || runtime.AppsMCPProductSKU != "tpp" || runtime.CodexHome != "/codex/home" {
		t.Fatalf("runtime config = %#v", runtime)
	}
	docs := runtime.Servers["docs"].Config
	if docs.Command != "mcp-docs" || len(docs.Args) != 2 || docs.Env["DOCS_TOKEN"] != "secret" || !docs.Required || !docs.Enabled {
		t.Fatalf("docs config = %#v", docs)
	}
	remote := runtime.Servers["remote"].Config
	if remote.URL != "https://mcp.example.test" || remote.BearerTokenEnvVar != "MCP_TOKEN" || remote.OAuthClientID != "client-1" || remote.Enabled {
		t.Fatalf("remote config = %#v", remote)
	}
	nested := runtime.Servers["nested-oauth"].Config
	if nested.URL != "https://nested.example.test" || nested.OAuthClientID != "client-nested" {
		t.Fatalf("nested oauth config = %#v", nested)
	}
}

func TestRuntimeConfigFromValuesAcceptsCamelCaseAliases(t *testing.T) {
	runtime := RuntimeConfigFromValues(map[string]any{
		"chatgptBaseUrl":       "https://chatgpt-camel.test",
		"appsMcpProductSku":    "enterprise",
		"connectorIds":         []any{"calendar"},
		"availableEnvironment": []any{"env-1"},
		"mcpServers": map[string]any{
			"remote": map[string]any{
				"url":               "https://mcp-camel.example.test",
				"bearerTokenEnvVar": "MCP_TOKEN",
				"oauthClientId":     "client-camel",
				"oauthResource":     "https://resource.example.test",
				"environmentId":     "env-remote",
			},
			"stdio": map[string]any{
				"command": "mcp-stdio",
				"env":     map[string]string{" DOCS_TOKEN ": " secret "},
			},
		},
	}, "/codex/home")
	if runtime.ChatGPTBaseURL != "https://chatgpt-camel.test" || runtime.AppsMCPProductSKU != "enterprise" {
		t.Fatalf("runtime aliases = %#v", runtime)
	}
	if !reflect.DeepEqual(runtime.ConnectorIDs, []string{"calendar"}) || !reflect.DeepEqual(runtime.AvailableEnvironment, []string{"env-1"}) {
		t.Fatalf("runtime alias slices = connectors %#v env %#v", runtime.ConnectorIDs, runtime.AvailableEnvironment)
	}
	remote := runtime.Servers["remote"].Config
	if remote.URL != "https://mcp-camel.example.test" || remote.BearerTokenEnvVar != "MCP_TOKEN" || remote.OAuthClientID != "client-camel" || remote.OAuthResource != "https://resource.example.test" || remote.EnvironmentID != "env-remote" {
		t.Fatalf("remote aliases = %#v", remote)
	}
	stdio := runtime.Servers["stdio"].Config
	if stdio.Command != "mcp-stdio" || stdio.Env["DOCS_TOKEN"] != "secret" {
		t.Fatalf("stdio env aliases = %#v", stdio)
	}
}

func TestRuntimeConfigFromValuesDefaultsAppsEnabled(t *testing.T) {
	runtime := RuntimeConfigFromValues(map[string]any{}, "")
	if !runtime.AppsEnabled {
		t.Fatalf("AppsEnabled = false, want true")
	}
	if _, ok := runtime.Servers[CodexAppsServerName]; !ok {
		t.Fatalf("codex apps server missing from runtime config")
	}
}

func TestManagerConfiguredAndEffectiveServers(t *testing.T) {
	manager := NewManager(nil)
	config := &RuntimeConfig{Servers: map[string]ServerRegistration{
		"enabled":  {Config: ServerConfig{Command: "ok", Enabled: true}},
		"disabled": {Config: ServerConfig{Command: "off", Enabled: false}},
		"auth":     {Config: ServerConfig{Command: "requires-auth-tool", Enabled: true}},
	}}
	configured := manager.ConfiguredServers(config)
	if _, ok := configured["disabled"]; ok {
		t.Fatalf("ConfiguredServers() included disabled server")
	}
	effective := manager.EffectiveServers(config, false)
	if _, ok := effective["auth"]; ok {
		t.Fatalf("EffectiveServers(unauthenticated) included auth-gated server")
	}
	if _, ok := effective["enabled"]; !ok {
		t.Fatalf("EffectiveServers(unauthenticated) omitted enabled server")
	}
}

func TestBuildToolExposure(t *testing.T) {
	tools := []Tool{
		{ServerName: "fs", Name: "read", ModelVisible: true},
		{ServerName: "hidden", Name: "secret", ModelVisible: false},
		{ServerName: CodexAppsServerName, Name: "calendar_create", ConnectorID: "calendar", ModelVisible: true},
		{ServerName: CodexAppsServerName, Name: "calendar_lookup", ConnectorID: " calendar ", ModelVisible: true},
		{ServerName: CodexAppsServerName, Name: "mail_send", ConnectorID: "mail", ModelVisible: true},
	}
	exposure := BuildToolExposure(tools, []AppConnector{{ID: " calendar ", Enabled: true}}, &AppToolPolicy{
		DisabledTools: map[string]bool{"calendar/calendar_create": false},
	}, true)
	if len(exposure.DirectTools) != 0 {
		t.Fatalf("DirectTools len = %d, want 0 with search enabled", len(exposure.DirectTools))
	}
	if got := toolNames(exposure.DeferredTools); !reflect.DeepEqual(got, []string{"read", "calendar_create", "calendar_lookup"}) {
		t.Fatalf("DeferredTools = %v, want read calendar_create calendar_lookup", got)
	}
	direct := BuildToolExposure(tools, []AppConnector{{ID: "calendar", Enabled: true}}, nil, false)
	if got := toolNames(direct.DirectTools); !reflect.DeepEqual(got, []string{"read", "calendar_create", "calendar_lookup"}) {
		t.Fatalf("DirectTools = %v, want read calendar_create calendar_lookup", got)
	}
}

func TestRenderApprovalTemplate(t *testing.T) {
	templates := []ApprovalTemplate{{
		ConnectorID: "calendar",
		ServerName:  CodexAppsServerName,
		ToolTitle:   "create_event",
		Template:    "Allow {connector_name} to create an event?",
		TemplateParams: []ApprovalTemplateParam{
			{Name: "calendar_id", Label: "Calendar"},
			{Name: "title", Label: "Title"},
		},
	}}
	rendered, ok := RenderApprovalTemplate(templates, CodexAppsServerName, "calendar", "Calendar", "create_event", map[string]any{
		"title":       "Roadmap",
		"calendar_id": "primary",
		"timezone":    "UTC",
	})
	if !ok {
		t.Fatalf("RenderApprovalTemplate() ok = false, want true")
	}
	if rendered.Question != "Allow Calendar to create an event?" {
		t.Fatalf("Question = %q", rendered.Question)
	}
	if got := approvalParamNames(rendered.ToolParamsDisplay); !reflect.DeepEqual(got, []string{"Calendar", "Title", "timezone"}) {
		t.Fatalf("ToolParamsDisplay = %v, want Calendar Title timezone", got)
	}
}

func TestRenderApprovalTemplateRejectsInvalidTemplates(t *testing.T) {
	templates := []ApprovalTemplate{{
		ConnectorID: "calendar",
		ServerName:  CodexAppsServerName,
		ToolTitle:   "create_event",
		Template:    "Allow {connector_name}?",
	}}
	if _, ok := RenderApprovalTemplate(templates, CodexAppsServerName, "calendar", "", "create_event", nil); ok {
		t.Fatalf("RenderApprovalTemplate(empty connector name) ok = true, want false")
	}
	templates[0].Template = "   "
	if _, ok := RenderApprovalTemplate(templates, CodexAppsServerName, "calendar", "Calendar", "create_event", nil); ok {
		t.Fatalf("RenderApprovalTemplate(empty template) ok = true, want false")
	}
}

func TestLoadApprovalTemplatesJSON(t *testing.T) {
	templates, err := LoadApprovalTemplatesJSON([]byte(`{
		"schema_version": 4,
		"templates": [{"connector_id":"calendar","server_name":"codex_apps","tool_title":"create_event","template":"Allow?","template_params":[]}]
	}`))
	if err != nil {
		t.Fatalf("LoadApprovalTemplatesJSON() error = %v", err)
	}
	if len(templates) != 1 {
		t.Fatalf("LoadApprovalTemplatesJSON() len = %d, want 1", len(templates))
	}
	_, err = LoadApprovalTemplatesJSON([]byte(`{"schema_version":3,"templates":[]}`))
	if err == nil || !strings.Contains(err.Error(), "schema version") {
		t.Fatalf("LoadApprovalTemplatesJSON(old schema) error = %v", err)
	}
}

func toolNames(tools []Tool) []string {
	out := make([]string, len(tools))
	for i := range tools {
		out[i] = tools[i].Name
	}
	return out
}

func approvalParamNames(params []RenderedApprovalParam) []string {
	out := make([]string, len(params))
	for i := range params {
		out[i] = params[i].DisplayName
	}
	return out
}
