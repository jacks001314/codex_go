package mcp

import "testing"

// Mirrors Rust #46019: a hosted Apps registration may override the protocol
// mode, and that override keeps the host-owned Apps authority — the native user
// verification capability — that an ordinary extension registration never gets.
func TestHostedAppsProtocolOverridePreservesUserVerificationLikeRust(t *testing.T) {
	auth := &RuntimeAuth{UsesCodexBackend: true, HTTPHeaders: map[string]string{"Authorization": "Bearer token"}}
	runtime := RuntimeConfigFromValuesWithAuth(map[string]any{
		"chatgpt_base_url": "https://chatgpt.com",
	}, "", auth)
	registration, ok := runtime.Servers[CodexAppsServerName]
	if !ok {
		t.Fatalf("hosted apps registration missing: %#v", runtime.Servers)
	}
	if registration.Config.ProtocolModeOverride != nil {
		t.Fatalf("hosted apps default carried a protocol override: %#v", registration.Config.ProtocolModeOverride)
	}
	if apps, ok := NewMCPService(runtime).serverConfig(CodexAppsServerName); !ok || apps.ProtocolMode != MCPProtocolLegacy {
		t.Fatalf("apps protocol mode = %#v, ok=%v, want the legacy hosted Apps default", apps, ok)
	}

	override := MCPProtocol20260728
	registration.Config.ProtocolModeOverride = &override
	runtime.Servers[CodexAppsServerName] = registration

	service := NewMCPService(runtime)
	service.SetOpenAIFormElicitationEnabled(true)
	apps, ok := service.serverConfig(CodexAppsServerName)
	if !ok || apps.ProtocolMode != MCPProtocol20260728 {
		t.Fatalf("apps protocol mode = %#v, ok=%v, want the registration override", apps, ok)
	}
	if !service.serverAllowsUserVerification(CodexAppsServerName) {
		t.Fatal("hosted apps protocol override lost native user verification")
	}

	// The same override on an ordinary extension registration still selects the
	// protocol mode but does not grant host-owned Apps authority.
	extension := ServerRegistration{
		Name:          CodexAppsServerName,
		Source:        string(CatalogSourceExtension),
		ContributorID: "ordinary",
		Config: ServerConfig{
			Enabled:              true,
			URL:                  "https://apps.example/mcp",
			EnvironmentID:        DefaultMCPServerEnvironmentID,
			ProtocolModeOverride: &override,
		},
	}
	extensionService := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{CodexAppsServerName: extension}})
	extensionService.SetOpenAIFormElicitationEnabled(true)
	extensionApps, ok := extensionService.serverConfig(CodexAppsServerName)
	if !ok || extensionApps.ProtocolMode != MCPProtocol20260728 {
		t.Fatalf("extension apps protocol mode = %#v, ok=%v, want the registration override", extensionApps, ok)
	}
	if extensionService.serverAllowsUserVerification(CodexAppsServerName) {
		t.Fatal("ordinary extension registration gained native user verification")
	}
}
