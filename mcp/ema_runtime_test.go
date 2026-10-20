package mcp

import (
	"net/http"
	"testing"

	managedconfig "codex_go/config"
)

func TestRuntimeConfigAdmitsEnterpriseMCPServer(t *testing.T) {
	home := t.TempDir()
	runtime := RuntimeConfigFromValues(map[string]any{
		"features": map[string]any{"use_xaa": true},
		"mcp_enterprise_managed_auth": map[string]any{
			"idp": map[string]any{"issuer": "https://idp.example", "client_id": "idp-client"},
		},
		"mcp_servers": map[string]any{
			"enterprise": map[string]any{
				"url":  "https://resource.example/mcp",
				"auth": ServerAuthEMAAuth,
				"oauth": map[string]any{
					"client_id":                   "mcp-client",
					"authorization_server_issuer": "https://as.example",
				},
			},
		},
	}, home)
	config := runtime.Servers["enterprise"].Config
	if !runtime.XAAEnabled || runtime.EnterpriseManagedAuth == nil || !config.EMAConfigured {
		t.Fatalf("enterprise admission = %+v profile=%+v", config, runtime.EnterpriseManagedAuth)
	}
	if config.ProtocolModeOverride == nil || *config.ProtocolModeOverride != MCPProtocol20260728 || !config.EMAAuthExtension {
		t.Fatalf("protocol mode/extension = %+v/%v", config.ProtocolModeOverride, config.EMAAuthExtension)
	}
	AttachEnterpriseMCPAuthorization(runtime, home, &RuntimeAuth{ChatGPTUserID: "user-1", AccountID: "account-1"}, nil)
	attached := runtime.Servers["enterprise"].Config
	if attached.ApplyHTTPRequest == nil {
		t.Fatal("enterprise server has no authorization applier")
	}
	if attached.EMACredentialsAvailable {
		t.Fatal("credentials should be absent before enterprise sign-in")
	}
}

func TestEMAAuthManagerReportsStoredGrantAndRejectsChangedOwner(t *testing.T) {
	home := t.TempDir()
	profile := managedconfig.MCPEnterpriseManagedAuthConfig{IDP: managedconfig.MCPServerIdPOAuthConfig{
		Issuer: "https://idp.example", ClientID: "idp-client",
	}}
	scope := EMAAuthScope{UserID: "user-1", WorkspaceID: "account-1"}
	credentialName := EMAAuthCredentialName(profile.IDP, scope)
	store := NewOAuthStoreWithMode(home, OAuthCredentialsStoreFile)
	if err := store.Save(&OAuthTokenSet{
		ServerName:   credentialName,
		ServerURL:    profile.IDP.Issuer,
		ClientID:     profile.IDP.ClientID,
		RefreshToken: "refresh-token",
		AccessToken:  "idp-access",
	}); err != nil {
		t.Fatal(err)
	}
	config := &ServerConfig{URL: "https://resource.example/mcp", Auth: ServerAuthEMAAuth, OAuthClientID: "mcp-client", OAuthResource: "https://resource.example/mcp"}
	current := scope
	manager, err := NewEMAAuthManager("enterprise", config, EMAAuthOptions{
		CodexHome: home,
		StoreMode: OAuthCredentialsStoreFile,
		Profile:   profile,
		Scope:     scope,
		CurrentScope: func() (EMAAuthScope, bool) {
			return current, true
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !manager.CredentialsAvailable() {
		t.Fatal("stored enterprise grant not found")
	}
	current = EMAAuthScope{UserID: "other-user", WorkspaceID: "account-1"}
	request, _ := http.NewRequest(http.MethodPost, "https://resource.example/mcp", nil)
	if err := manager.ApplyHTTPRequest(request, nil); err == nil {
		t.Fatal("changed account owner must revoke enterprise authorization")
	}
}
