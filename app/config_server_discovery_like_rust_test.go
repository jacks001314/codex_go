package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"codex_go/config"
)

func discoveredConfigServer(t *testing.T, requests *atomic.Int32, explicit bool) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/.well-known/oauth-authorization-server" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                        server.URL,
			"authorization_endpoint":        server.URL + "/authorize",
			"token_endpoint":                server.URL + "/token",
			"device_authorization_endpoint": server.URL + "/device",
		})
	}))
	return server
}

// Rust #62824: a config server that omits oauth now resolves its published
// authorization metadata instead of requiring administrators to repeat the
// endpoints in managed requirements.
func TestConfigServerDiscoversOAuthLikeRust(t *testing.T) {
	var requests atomic.Int32
	server := discoveredConfigServer(t, &requests, false)
	defer server.Close()

	resolved, err := resolveConfigServerOAuth(context.Background(), &config.ConfigServer{BaseURL: server.URL}, server.Client(), nil)
	if err != nil {
		t.Fatalf("resolveConfigServerOAuth() error = %v", err)
	}
	if resolved == nil || resolved.OAuth == nil {
		t.Fatalf("resolved = %#v, want discovered OAuth", resolved)
	}
	oauth := resolved.OAuth
	if oauth.AuthorizationURL != server.URL+"/authorize" || oauth.TokenURL != server.URL+"/token" || oauth.ClientID != "codex" {
		t.Fatalf("oauth = %#v", oauth)
	}
	if oauth.DeviceAuthorizationURL == nil || *oauth.DeviceAuthorizationURL != server.URL+"/device" {
		t.Fatalf("device endpoint = %#v", oauth.DeviceAuthorizationURL)
	}
	if requests.Load() == 0 {
		t.Fatal("discovery made no request")
	}
}

// Explicit config_server.oauth settings win and never touch the network.
func TestConfigServerExplicitOAuthSkipsDiscoveryLikeRust(t *testing.T) {
	var requests atomic.Int32
	server := discoveredConfigServer(t, &requests, true)
	defer server.Close()

	explicit := &config.ConfigServerOAuth{AuthorizationURL: server.URL + "/explicit", TokenURL: server.URL + "/explicit-token", ClientID: "company"}
	resolved, err := resolveConfigServerOAuth(context.Background(), &config.ConfigServer{BaseURL: server.URL, OAuth: explicit}, server.Client(), nil)
	if err != nil {
		t.Fatalf("resolveConfigServerOAuth() error = %v", err)
	}
	if resolved.OAuth != explicit {
		t.Fatalf("resolved oauth = %#v, want the explicit settings", resolved.OAuth)
	}
	if requests.Load() != 0 {
		t.Fatalf("requests = %d, want no discovery", requests.Load())
	}
}

// Rust #62824: the Git credential helper resolves the config server without
// OAuth so it can apply workspace policy without opening the OS keyring.
func TestAnonymousConfigServerLoaderRefusesOAuthLikeRust(t *testing.T) {
	var requests atomic.Int32
	server := discoveredConfigServer(t, &requests, false)
	defer server.Close()

	if _, err := anonymousConfigServerBundleLoaderWithClient(context.Background(), t.TempDir(), configServerTestBootstrap(), &config.ConfigServer{BaseURL: server.URL}, server.Client()); !errors.Is(err, errGitCredentialProtectedLogin) {
		t.Fatalf("discovered OAuth error = %v, want the protected-login refusal", err)
	}

	var explicitRequests atomic.Int32
	explicitServer := discoveredConfigServer(t, &explicitRequests, true)
	defer explicitServer.Close()
	explicit := &config.ConfigServerOAuth{AuthorizationURL: explicitServer.URL + "/authorize", TokenURL: explicitServer.URL + "/token", ClientID: "company"}
	if _, err := anonymousConfigServerBundleLoaderWithClient(context.Background(), t.TempDir(), configServerTestBootstrap(), &config.ConfigServer{BaseURL: explicitServer.URL, OAuth: explicit}, explicitServer.Client()); !errors.Is(err, errGitCredentialProtectedLogin) {
		t.Fatalf("explicit OAuth error = %v, want the protected-login refusal", err)
	}
	if explicitRequests.Load() != 0 {
		t.Fatalf("explicit OAuth discovery requests = %d, want none", explicitRequests.Load())
	}
}

// Rust #62824: logout discovery honors the local application network policy.
func TestConfigServerLogoutDiscoveryHonorsNetworkPolicyLikeRust(t *testing.T) {
	var requests atomic.Int32
	server := discoveredConfigServer(t, &requests, false)
	defer server.Close()

	bootstrap := &config.Config{
		Values: map[string]any{},
		Requirements: &config.ConfigRequirements{
			Application: &config.ApplicationRequirements{Network: &config.ApplicationNetworkRequirements{
				Enabled: true,
				Domains: map[string]config.NetworkPermission{"127.0.0.1": config.NetworkDeny},
			}},
		},
	}
	policy := publishApplicationNetworkPolicy(bootstrap)
	if _, err := resolveConfigServerOAuth(context.Background(), &config.ConfigServer{BaseURL: server.URL}, server.Client(), &policy); err == nil {
		t.Fatal("denied discovery returned no error")
	}
	if requests.Load() != 0 {
		t.Fatalf("requests = %d, want the deny policy to block discovery", requests.Load())
	}
}
