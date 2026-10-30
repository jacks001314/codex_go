package mcp

import (
	"encoding/json"
	"testing"
)

func testAgentMetadataRegistration() (AgentMCPMetadataRecipient, ResolvedServer) {
	config := ServerConfig{URL: "https://coordinator.example/mcp", Enabled: true}
	recipient := AgentMCPMetadataRecipient{
		PluginID:   "agents@local",
		PluginRoot: "file:///plugins/agents",
		ServerName: "agents",
		Transport:  config,
	}
	catalog := ResolveCatalog([]CatalogAction{{
		Name:   "agents",
		Source: CatalogSourcePlugin,
		PluginAttribution: &PluginAttribution{
			PluginID: "agents@local",
			HostRoot: "file:///plugins/agents",
		},
		Config: config,
	}})
	return recipient, catalog.Servers["agents"]
}

// Mirrors Rust agent_context_requires_the_exact_captured_recipient (#58378).
func TestAgentMCPMetadataRecipientRequiresExactRegistrationLikeRust(t *testing.T) {
	tests := []struct {
		name      string
		mismatch  string
		wantMatch bool
	}{
		{name: "exact", wantMatch: true},
		{name: "plugin_id", mismatch: "plugin_id"},
		{name: "plugin_root", mismatch: "plugin_root"},
		{name: "server_name", mismatch: "server_name"},
		{name: "endpoint", mismatch: "endpoint"},
		{name: "environment", mismatch: "environment"},
		{name: "config_source", mismatch: "config_source"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			recipient, registration := testAgentMetadataRegistration()
			serverName := recipient.ServerName
			switch testCase.mismatch {
			case "plugin_id":
				registration.PluginAttribution = &PluginAttribution{PluginID: "unrelated@local", HostRoot: recipient.PluginRoot}
			case "plugin_root":
				registration.PluginAttribution = &PluginAttribution{PluginID: recipient.PluginID, HostRoot: "file:///plugins/replaced"}
			case "server_name":
				serverName = "another-server"
			case "endpoint":
				registration.Config = ServerConfig{URL: "https://unrelated.example/mcp"}
			case "environment":
				registration.Config.EnvironmentID = "remote"
			case "config_source":
				registration.Source = CatalogSourceConfig
			}
			if got := recipient.Matches(serverName, registration); got != testCase.wantMatch {
				t.Fatalf("Matches() = %v, want %v", got, testCase.wantMatch)
			}
		})
	}
}

func TestAgentMCPMetadataRecipientRejectsSameNameUserOverrideLikeRust(t *testing.T) {
	recipient, _ := testAgentMetadataRegistration()
	catalog := ResolveCatalog([]CatalogAction{
		{
			Name:   "agents",
			Source: CatalogSourcePlugin,
			PluginAttribution: &PluginAttribution{
				PluginID: recipient.PluginID,
				HostRoot: recipient.PluginRoot,
			},
			Config: recipient.Transport,
		},
		{
			Name:   "agents",
			Source: CatalogSourceConfig,
			Config: recipient.Transport,
		},
	})
	if recipient.Matches(recipient.ServerName, catalog.Servers["agents"]) {
		t.Fatal("same-name user override matched the plugin recipient")
	}
}

func TestMCPServiceResolvedServerCarriesPluginAttributionLikeRust(t *testing.T) {
	recipient, _ := testAgentMetadataRegistration()
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		recipient.ServerName: {
			Name:           recipient.ServerName,
			Source:         "plugin",
			PluginID:       recipient.PluginID,
			PluginHostRoot: recipient.PluginRoot,
			Config:         recipient.Transport,
		},
	}})
	registration, ok := service.ResolvedServerForServer(recipient.ServerName)
	if !ok {
		t.Fatal("ResolvedServerForServer() missed the plugin registration")
	}
	if !recipient.Matches(recipient.ServerName, registration) {
		t.Fatalf("resolved registration = %#v, want a matching plugin registration", registration)
	}
}

func TestAgentMetadataMetaOnlyReachesMatchingRegistrationLikeRust(t *testing.T) {
	recipient, registration := testAgentMetadataRegistration()
	executor := NewToolExecutor(&ToolExecutorOptions{
		ServerName:             recipient.ServerName,
		AgentMetadataRecipient: &recipient,
		ResolvedServer:         &registration,
		TurnRootTurnID:         func() string { return "original-root" },
		TurnCyberAccessProgram: func() string { return "" },
	})
	meta, ok := executor.requestMetaForCall("call-agent-context").(map[string]any)
	if !ok {
		t.Fatalf("request meta = %#v, want object", executor.requestMetaForCall("call-agent-context"))
	}
	if got := meta[MCPAgentRootTurnIDMetaKey]; got != "original-root" {
		t.Fatalf("%s = %#v", MCPAgentRootTurnIDMetaKey, got)
	}
	value, present := meta[MCPAgentCyberAccessProgramMetaKey]
	if !present || value != nil {
		t.Fatalf("%s = %#v (present %v), want explicit null", MCPAgentCyberAccessProgramMetaKey, value, present)
	}
	data, err := json.Marshal(meta[MCPAgentCyberAccessProgramMetaKey])
	if err != nil || string(data) != "null" {
		t.Fatalf("marshal %s = %s, %v; want null", MCPAgentCyberAccessProgramMetaKey, data, err)
	}
}

func TestAgentMetadataMetaOmittedWithoutMatchLikeRust(t *testing.T) {
	recipient, registration := testAgentMetadataRegistration()
	override := registration
	override.Source = CatalogSourceConfig
	tests := []struct {
		name    string
		options *ToolExecutorOptions
	}{
		{
			name: "no host opt in",
			options: &ToolExecutorOptions{
				ServerName:     recipient.ServerName,
				ResolvedServer: &registration,
				TurnRootTurnID: func() string { return "original-root" },
			},
		},
		{
			name: "same name user override",
			options: &ToolExecutorOptions{
				ServerName:             recipient.ServerName,
				AgentMetadataRecipient: &recipient,
				ResolvedServer:         &override,
				TurnRootTurnID:         func() string { return "original-root" },
			},
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			executor := NewToolExecutor(testCase.options)
			meta, _ := executor.requestMetaForCall("call-agent-context").(map[string]any)
			if _, ok := meta[MCPAgentRootTurnIDMetaKey]; ok {
				t.Fatalf("%s sent without an exact registration", MCPAgentRootTurnIDMetaKey)
			}
			if _, ok := meta[MCPAgentCyberAccessProgramMetaKey]; ok {
				t.Fatalf("%s sent without an exact registration", MCPAgentCyberAccessProgramMetaKey)
			}
		})
	}
}
