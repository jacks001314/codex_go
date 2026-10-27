package mcp

import (
	"net/http"
	"strings"
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
	AttachEnterpriseMCPAuthorization(runtime, home, &RuntimeAuth{ChatGPTUserID: "user-1", AccountID: "account-1"}, nil, nil, nil)
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
	// Rust #56127: inspection is owner-checked, so a manager for the previous
	// account stops reporting a grant once the account changes.
	if manager.CredentialsAvailable() {
		t.Fatal("changed account owner must not report the previous grant")
	}
	if _, err := store.Delete(credentialName, profile.IDP.Issuer); err != nil {
		t.Fatal(err)
	}
	current = scope
	if manager.CredentialsAvailable() {
		t.Fatal("logged-out account must not report a grant")
	}
}

func TestEMAAuthManagerRevokesOnLoginGenerationChangeLikeRust(t *testing.T) {
	home := t.TempDir()
	profile := managedconfig.MCPEnterpriseManagedAuthConfig{IDP: managedconfig.MCPServerIdPOAuthConfig{
		Issuer: "https://idp.example", ClientID: "idp-client",
	}}
	scope := EMAAuthScope{UserID: "user-1", WorkspaceID: "account-1"}
	if err := NewOAuthStoreWithMode(home, OAuthCredentialsStoreFile).Save(&OAuthTokenSet{
		ServerName:   EMAAuthCredentialName(profile.IDP, scope),
		ServerURL:    profile.IDP.Issuer,
		ClientID:     profile.IDP.ClientID,
		RefreshToken: "refresh-token",
		AccessToken:  "idp-access",
	}); err != nil {
		t.Fatal(err)
	}
	config := &ServerConfig{URL: "https://resource.example/mcp", Auth: ServerAuthEMAAuth, OAuthClientID: "mcp-client", OAuthResource: "https://resource.example/mcp"}
	var loginGeneration uint64
	manager, err := NewEMAAuthManager("enterprise", config, EMAAuthOptions{
		CodexHome:              home,
		StoreMode:              OAuthCredentialsStoreFile,
		Profile:                profile,
		Scope:                  scope,
		CurrentScope:           func() (EMAAuthScope, bool) { return scope, true },
		CurrentLoginGeneration: func() uint64 { return loginGeneration },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !manager.CredentialsAvailable() {
		t.Fatal("stored enterprise grant not found")
	}
	loginGeneration = 1
	if manager.CredentialsAvailable() {
		t.Fatal("a newer login must revoke the enterprise grant status")
	}
	request, _ := http.NewRequest(http.MethodPost, "https://resource.example/mcp", nil)
	if err := manager.ApplyHTTPRequest(request, nil); err == nil {
		t.Fatal("a newer login must revoke enterprise authorization")
	}
}

func TestEMACredentialStatusSharesOneReadLikeRust(t *testing.T) {
	status := &emaCredentialStatus{}
	calls := 0
	loadTrue := func() bool { calls++; return true }
	first := emaCredentialKey{storeMode: OAuthCredentialsStoreFile, credentialName: "grant", issuer: "https://idp.example"}
	second := emaCredentialKey{storeMode: OAuthCredentialsStoreKeyring, credentialName: "grant", issuer: "https://idp.example"}

	if !status.available(first, loadTrue) || !status.available(first, loadTrue) {
		t.Fatal("grant was not reported as available")
	}
	if calls != 1 {
		t.Fatalf("loads for one grant = %d, want 1", calls)
	}
	// A different store resolves a different credential read.
	if !status.available(second, loadTrue) || calls != 2 {
		t.Fatalf("distinct store loads = %d, want 2", calls)
	}
	// A missing grant or read error is cached too, so every server reports the
	// same status without repeating the failed read.
	missing := emaCredentialKey{storeMode: OAuthCredentialsStoreFile, credentialName: "missing", issuer: "https://idp.example"}
	loadFalse := func() bool { calls++; return false }
	for range 3 {
		if status.available(missing, loadFalse) {
			t.Fatal("missing grant reported as available")
		}
	}
	if calls != 3 {
		t.Fatalf("loads after missing grant = %d, want 3", calls)
	}
}

func TestAttachEnterpriseMCPAuthorizationSharesCredentialStatusAcrossServers(t *testing.T) {
	home := t.TempDir()
	runtime := RuntimeConfigFromValues(map[string]any{
		"mcp_oauth_credentials_store": "file",
		"features":                    map[string]any{"use_xaa": true},
		"mcp_enterprise_managed_auth": map[string]any{
			"idp": map[string]any{"issuer": "https://idp.example", "client_id": "idp-client"},
		},
		"mcp_servers": map[string]any{
			"first": map[string]any{
				"url":  "https://first.example/mcp",
				"auth": ServerAuthEMAAuth,
				"oauth": map[string]any{
					"client_id":                   "mcp-client",
					"authorization_server_issuer": "https://as.example",
				},
			},
			"second": map[string]any{
				"url":  "https://second.example/mcp",
				"auth": ServerAuthEMAAuth,
				"oauth": map[string]any{
					"client_id":                   "mcp-client",
					"authorization_server_issuer": "https://as.example",
				},
			},
		},
	}, home)
	scope := EMAAuthScope{UserID: "user-1", WorkspaceID: "account-1"}
	profile := managedconfig.MCPEnterpriseManagedAuthConfig{IDP: managedconfig.MCPServerIdPOAuthConfig{
		Issuer: "https://idp.example", ClientID: "idp-client",
	}}
	store := NewOAuthStoreWithMode(home, OAuthCredentialsStoreFile)
	if err := store.Save(&OAuthTokenSet{
		ServerName:   EMAAuthCredentialName(profile.IDP, scope),
		ServerURL:    profile.IDP.Issuer,
		ClientID:     profile.IDP.ClientID,
		RefreshToken: "refresh-token",
		AccessToken:  "idp-access",
	}); err != nil {
		t.Fatal(err)
	}

	AttachEnterpriseMCPAuthorization(runtime, home, &RuntimeAuth{ChatGPTUserID: scope.UserID, AccountID: scope.WorkspaceID}, nil, nil, nil)
	for _, name := range []string{"first", "second"} {
		config := runtime.Servers[name].Config
		if !config.EMAConfigured || !config.EMACredentialsAvailable || config.ApplyHTTPRequest == nil {
			t.Fatalf("%s config = %+v, want configured EMA with an available grant", name, config)
		}
	}
}

// TestEMAAuthManagerRevokesWhenAccountLoginChangesLikeRust mirrors #55680: an
// account login replacement revokes enterprise authorization before a token
// exchange, and the manager stays revoked.
func TestEMAAuthManagerRevokesWhenAccountLoginChangesLikeRust(t *testing.T) {
	home := t.TempDir()
	profile := managedconfig.MCPEnterpriseManagedAuthConfig{IDP: managedconfig.MCPServerIdPOAuthConfig{
		Issuer: "https://idp.example", ClientID: "idp-client",
	}}
	scope := EMAAuthScope{UserID: "user-1", WorkspaceID: "account-1"}
	if err := NewOAuthStoreWithMode(home, OAuthCredentialsStoreFile).Save(&OAuthTokenSet{
		ServerName:   EMAAuthCredentialName(profile.IDP, scope),
		ServerURL:    profile.IDP.Issuer,
		ClientID:     profile.IDP.ClientID,
		RefreshToken: "refresh-token",
		AccessToken:  "idp-access",
	}); err != nil {
		t.Fatal(err)
	}
	accountCurrent := false
	manager, err := NewEMAAuthManager("enterprise", &ServerConfig{
		URL: "https://resource.example/mcp", Auth: ServerAuthEMAAuth, OAuthClientID: "mcp-client",
	}, EMAAuthOptions{
		CodexHome:           home,
		StoreMode:           OAuthCredentialsStoreFile,
		Profile:             profile,
		Scope:               scope,
		AccountLoginCurrent: func() (bool, error) { return accountCurrent, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest(http.MethodPost, "https://resource.example/mcp", nil)
	err = manager.ApplyHTTPRequest(request, nil)
	if err == nil || !strings.Contains(err.Error(), "enterprise identity requires authentication") {
		t.Fatalf("ApplyHTTPRequest() error = %v, want reauthentication required", err)
	}
	// A later successful check does not restore a revoked manager.
	accountCurrent = true
	if err := manager.ApplyHTTPRequest(request, nil); err == nil || !strings.Contains(err.Error(), "revoked") {
		t.Fatalf("revoked manager error = %v", err)
	}
	if manager.CredentialsAvailable() {
		t.Fatal("revoked manager must not report enterprise credentials")
	}
}

// TestEMAAuthManagerRejectsGrantFromAnotherIdPClientLikeRust mirrors
// ema_identity's stored-credential guard: a grant whose client does not match
// the configured IdP client is never exchanged.
func TestEMAAuthManagerRejectsGrantFromAnotherIdPClientLikeRust(t *testing.T) {
	home := t.TempDir()
	profile := managedconfig.MCPEnterpriseManagedAuthConfig{IDP: managedconfig.MCPServerIdPOAuthConfig{
		Issuer: "https://idp.example", ClientID: "idp-client", ClientSecret: "s3cret",
	}}
	scope := EMAAuthScope{UserID: "user-1", WorkspaceID: "account-1"}
	if err := NewOAuthStoreWithMode(home, OAuthCredentialsStoreFile).Save(&OAuthTokenSet{
		ServerName:   EMAAuthCredentialName(profile.IDP, scope),
		ServerURL:    profile.IDP.Issuer,
		ClientID:     "another-client",
		RefreshToken: "refresh-token",
		AccessToken:  "idp-access",
	}); err != nil {
		t.Fatal(err)
	}
	config := &ServerConfig{
		URL:                            "https://resource.example/mcp",
		Auth:                           ServerAuthEMAAuth,
		OAuthClientID:                  "mcp-client",
		OAuthResource:                  "https://resource.example/mcp",
		OAuthAuthorizationServerIssuer: "https://as.example",
	}
	manager, err := NewEMAAuthManager("enterprise", config, EMAAuthOptions{
		CodexHome: home,
		StoreMode: OAuthCredentialsStoreFile,
		Profile:   profile,
		Scope:     scope,
	})
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest(http.MethodPost, config.URL, nil)
	err = manager.ApplyHTTPRequest(request, nil)
	if err == nil || !strings.Contains(err.Error(), "stored enterprise IdP credentials do not match") {
		t.Fatalf("ApplyHTTPRequest() error = %v", err)
	}
}
