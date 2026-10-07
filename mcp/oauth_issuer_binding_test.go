package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// Mirrors Rust #39935 (`rmcp-client/src/oauth/issuer_binding.rs`):
// `validate_authorization_server_endpoints` rejects an authorization endpoint
// that cannot be bound to the issuer it advertises. Go resolves authorization
// servers by iterating the protected-resource metadata's `authorization_servers`
// list, so this is the multi-authorization-server surface the check protects.
func TestMCPOAuthDiscoveryRejectsCrossOriginAuthorizationEndpointLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		metadata map[string]any
	}{
		{
			name: "authorization endpoint on a different origin than the issuer",
			metadata: map[string]any{
				"issuer":                 "https://issuer.example",
				"authorization_endpoint": "https://evil.example/authorize",
				"token_endpoint":         "https://issuer.example/token",
			},
		},
		{
			name: "authorization endpoint on a lookalike issuer host",
			metadata: map[string]any{
				"issuer":                 "https://issuer.example",
				"authorization_endpoint": "https://issuer.example.evil.example/authorize",
				"token_endpoint":         "https://issuer.example/token",
			},
		},
		{
			name: "authorization endpoint downgraded to http",
			metadata: map[string]any{
				"issuer":                 "https://issuer.example",
				"authorization_endpoint": "http://issuer.example/authorize",
				"token_endpoint":         "https://issuer.example/token",
			},
		},
		{
			// The endpoint is on the token origin, not the issuer origin: Rust
			// only accepts the token origin as a fallback when it is the same
			// origin as the issuer, which this is not.
			name: "authorization endpoint on an unrelated third origin",
			metadata: map[string]any{
				"issuer":                 "https://issuer.example",
				"authorization_endpoint": "https://elsewhere.example/authorize",
				"token_endpoint":         "https://tokens.example/token",
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			server := newMCPOAuthAuthorizationServerFixture(t, testCase.metadata)
			defer server.Close()

			discovery, err := DiscoverStreamableHTTPOAuth(context.Background(), server.URL+"/mcp", server.Client())
			if err == nil {
				t.Fatalf("DiscoverStreamableHTTPOAuth() = %#v, want the cross-origin authorization endpoint rejected", discovery)
			}
			if !strings.Contains(err.Error(), "OAuth authorization endpoint origin does not match the authorization server origin without issuer-bound callbacks") {
				t.Fatalf("discovery error = %v, want the issuer-binding rejection", err)
			}
		})
	}
}

// The same rejection must apply to a candidate resolved through the
// protected-resource metadata's `authorization_servers` list.
func TestMCPOAuthDiscoveryRejectsCrossOriginAuthorizationServerFromResourceMetadata(t *testing.T) {
	authServer := newMCPOAuthAuthorizationServerFixture(t, map[string]any{
		"issuer":                 "https://issuer.example",
		"authorization_endpoint": "https://evil.example/authorize",
		"token_endpoint":         "https://issuer.example/token",
	})
	defer authServer.Close()

	resourceServer := newMCPOAuthResourceMetadataFixture(t, []string{authServer.URL + "/mcp"})
	defer resourceServer.Close()

	discovery, err := DiscoverStreamableHTTPOAuth(context.Background(), resourceServer.URL+"/mcp", resourceServer.Client())
	if err == nil {
		t.Fatalf("DiscoverStreamableHTTPOAuth() = %#v, want the listed cross-origin authorization server rejected", discovery)
	}
	if !strings.Contains(err.Error(), "OAuth authorization endpoint origin does not match") {
		t.Fatalf("discovery error = %v, want the issuer-binding rejection", err)
	}
}

// Rust's narrow compatibility exceptions must keep working, and only for the
// exact (issuer, authorization origin, token origin) triple.
func TestMCPOAuthDiscoveryKeepsIssuerBindingExceptionsAndRejectsLookalikes(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		metadata map[string]any
		ok       bool
	}{
		{
			name: "mercadopago exception",
			metadata: map[string]any{
				"issuer":                 "https://mcp.mercadopago.com/mcp",
				"authorization_endpoint": "https://auth.mercadopago.com/authorize",
				"token_endpoint":         "https://mcp.mercadopago.com/token",
			},
			ok: true,
		},
		{
			name: "robinhood exception",
			metadata: map[string]any{
				"issuer":                 "https://agent.robinhood.com/mcp/trading",
				"authorization_endpoint": "https://robinhood.com/authorize",
				"token_endpoint":         "https://api.robinhood.com/token",
			},
			ok: true,
		},
		{
			name: "mercadopago lookalike authorization host",
			metadata: map[string]any{
				"issuer":                 "https://mcp.mercadopago.com/mcp",
				"authorization_endpoint": "https://auth.mercadopago.com.evil.example/authorize",
				"token_endpoint":         "https://mcp.mercadopago.com/token",
			},
		},
		{
			name: "mercadopago unrelated token host",
			metadata: map[string]any{
				"issuer":                 "https://mcp.mercadopago.com/mcp",
				"authorization_endpoint": "https://auth.mercadopago.com/authorize",
				"token_endpoint":         "https://tokens.example/token",
			},
		},
		{
			name: "mercadopago http issuer",
			metadata: map[string]any{
				"issuer":                 "http://mcp.mercadopago.com/mcp",
				"authorization_endpoint": "https://auth.mercadopago.com/authorize",
				"token_endpoint":         "https://mcp.mercadopago.com/token",
			},
		},
		{
			name: "mercadopago authorization endpoint downgraded to http",
			metadata: map[string]any{
				"issuer":                 "https://mcp.mercadopago.com/mcp",
				"authorization_endpoint": "http://auth.mercadopago.com/authorize",
				"token_endpoint":         "https://mcp.mercadopago.com/token",
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			server := newMCPOAuthAuthorizationServerFixture(t, testCase.metadata)
			defer server.Close()

			discovery, err := DiscoverStreamableHTTPOAuth(context.Background(), server.URL+"/mcp", server.Client())
			if testCase.ok {
				if err != nil {
					t.Fatalf("DiscoverStreamableHTTPOAuth() error = %v, want the exception accepted", err)
				}
				if discovery == nil || discovery.AuthorizationEndpoint != testCase.metadata["authorization_endpoint"] {
					t.Fatalf("discovery = %#v, want the exception's authorization endpoint", discovery)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "OAuth authorization endpoint origin does not match") {
				t.Fatalf("discovery = %#v, err = %v, want the lookalike rejected", discovery, err)
			}
		})
	}
}

// The issuer-bound-callbacks arm (RFC 9207) requires an issuer and otherwise
// trusts the advertised endpoint, exactly like Rust.
func TestMCPOAuthDiscoveryIssuerBoundCallbacksRequireIssuer(t *testing.T) {
	withoutIssuer := newMCPOAuthAuthorizationServerFixture(t, map[string]any{
		"authorization_endpoint":                         "https://evil.example/authorize",
		"token_endpoint":                                 "https://issuer.example/token",
		"authorization_response_iss_parameter_supported": true,
	})
	defer withoutIssuer.Close()
	_, err := DiscoverStreamableHTTPOAuth(context.Background(), withoutIssuer.URL+"/mcp", withoutIssuer.Client())
	if err == nil || !strings.Contains(err.Error(), "OAuth issuer-bound callbacks require an authorization server issuer") {
		t.Fatalf("issuer-bound callbacks without an issuer error = %v", err)
	}

	withIssuer := newMCPOAuthAuthorizationServerFixture(t, map[string]any{
		"issuer":                 "https://issuer.example",
		"authorization_endpoint": "https://elsewhere.example/authorize",
		"token_endpoint":         "https://issuer.example/token",
		"authorization_response_iss_parameter_supported": true,
	})
	defer withIssuer.Close()
	discovery, err := DiscoverStreamableHTTPOAuth(context.Background(), withIssuer.URL+"/mcp", withIssuer.Client())
	if err != nil {
		t.Fatalf("issuer-bound callbacks with an issuer error = %v", err)
	}
	if discovery == nil || discovery.AuthorizationEndpoint != "https://elsewhere.example/authorize" {
		t.Fatalf("discovery = %#v, want the advertised endpoint accepted under issuer-bound callbacks", discovery)
	}
	if discovery.CallbackMode != MCPOAuthCallbackIssuerBound {
		t.Fatalf("CallbackMode = %q, want %q", discovery.CallbackMode, MCPOAuthCallbackIssuerBound)
	}
}

// Issuer-less metadata must still bind the token endpoint to the authorization
// endpoint's origin, matching Rust's fallback arm in
// `validate_authorization_server_endpoints` (#39935). Without it an
// issuer-less authorization server could point the authorization hand-off at
// one origin and the code exchange at another.
func TestMCPOAuthDiscoveryBindsIssuerLessTokenEndpointLikeRust(t *testing.T) {
	sameOrigin := newMCPOAuthAuthorizationServerFixture(t, map[string]any{
		"authorization_endpoint": "https://issuer.example/authorize",
		"token_endpoint":         "https://issuer.example/token",
	})
	defer sameOrigin.Close()
	discovery, err := DiscoverStreamableHTTPOAuth(context.Background(), sameOrigin.URL+"/mcp", sameOrigin.Client())
	if err != nil {
		t.Fatalf("DiscoverStreamableHTTPOAuth() error = %v, want issuer-less same-origin metadata accepted", err)
	}
	if discovery == nil || discovery.AuthorizationEndpoint != "https://issuer.example/authorize" {
		t.Fatalf("discovery = %#v, want the issuer-less same-origin endpoint accepted", discovery)
	}

	crossOrigin := newMCPOAuthAuthorizationServerFixture(t, map[string]any{
		"authorization_endpoint": "https://issuer.example/authorize",
		"token_endpoint":         "https://tokens.example/token",
	})
	defer crossOrigin.Close()
	discovery, err = DiscoverStreamableHTTPOAuth(context.Background(), crossOrigin.URL+"/mcp", crossOrigin.Client())
	if err == nil || !strings.Contains(err.Error(), "OAuth token endpoint origin does not match the authorization server origin without issuer-bound callbacks") {
		t.Fatalf("DiscoverStreamableHTTPOAuth() error = %v, want the cross-origin token endpoint rejected", err)
	}
	if discovery != nil {
		t.Fatalf("discovery = %#v, want the cross-origin token endpoint rejected", discovery)
	}
}

func TestMCPOAuthOriginSerializationAndExceptionTableMatchRust(t *testing.T) {
	for _, testCase := range []struct {
		raw  string
		want string
	}{
		{"https://Issuer.Example/authorize", "https://issuer.example"},
		{"https://issuer.example:443/authorize", "https://issuer.example"},
		{"http://issuer.example:80/authorize", "http://issuer.example"},
		{"https://issuer.example:8443/authorize", "https://issuer.example:8443"},
		{"http://127.0.0.1:43210/callback", "http://127.0.0.1:43210"},
		{"/relative", ""},
	} {
		parsed, err := url.Parse(testCase.raw)
		if err != nil {
			t.Fatalf("url.Parse(%q): %v", testCase.raw, err)
		}
		if got := mcpOAuthOriginSerialization(parsed); got != testCase.want {
			t.Fatalf("mcpOAuthOriginSerialization(%q) = %q, want %q", testCase.raw, got, testCase.want)
		}
	}

	if len(mcpOAuthIssuerBindingExceptions) != 2 {
		t.Fatalf("mcpOAuthIssuerBindingExceptions = %d entries, want the 2 ported from Rust", len(mcpOAuthIssuerBindingExceptions))
	}
	if !mcpOAuthIssuerBindingExceptionMatches("https://mcp.mercadopago.com/mcp", "https://auth.mercadopago.com", "https://mcp.mercadopago.com") {
		t.Fatal("mercadopago exception did not match the Rust triple")
	}
	if !mcpOAuthIssuerBindingExceptionMatches("https://agent.robinhood.com/mcp/trading", "https://robinhood.com", "https://api.robinhood.com") {
		t.Fatal("robinhood exception did not match the Rust triple")
	}
	if mcpOAuthIssuerBindingExceptionMatches("https://mcp.mercadopago.com/mcp", "https://auth.mercadopago.com", "https://tokens.example") {
		t.Fatal("exception matched with an unrelated token origin")
	}
	if mcpOAuthIssuerBindingExceptionMatches("https://mcp.mercadopago.com/other", "https://auth.mercadopago.com", "https://mcp.mercadopago.com") {
		t.Fatal("exception matched with a different issuer path")
	}
}

func newMCPOAuthAuthorizationServerFixture(t *testing.T, metadata map[string]any) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/.well-known/oauth-authorization-server/mcp") {
			http.NotFound(w, r)
			return
		}
		writeJSON(t, w, metadata)
	}))
	return server
}

func newMCPOAuthResourceMetadataFixture(t *testing.T, authorizationServers []string) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/mcp":
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+server.URL+`/oauth-resource"`)
			w.WriteHeader(http.StatusUnauthorized)
		case "/oauth-resource":
			writeJSON(t, w, map[string]any{
				"resource":              server.URL + "/mcp",
				"authorization_servers": authorizationServers,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	return server
}

// A metadata document that was fetched and decoded but refused by the
// authorization-server binding rules must fail the interactive login instead of
// falling back to the guessed `<server>/oauth/authorize` URL. Rust propagates
// `validate_authorization_server_endpoints` out of `perform_oauth_login`
// (#39935), so hiding the rejection behind a fallback would diverge from it.
func TestMCPServiceOauthLoginFailsForUnboundAuthorizationServerLikeRust(t *testing.T) {
	newLoginService := func(t *testing.T, serverURL string) *MCPService {
		t.Helper()
		service := NewMCPService(&RuntimeConfig{
			CodexHome: t.TempDir(),
			Servers: map[string]ServerRegistration{
				"docs": {Config: ServerConfig{URL: serverURL, OAuthClientID: "client-1", Enabled: true}},
			},
		})
		t.Cleanup(func() { _ = service.Close() })
		return service
	}

	unbound := newMCPOAuthAuthorizationServerFixture(t, map[string]any{
		"authorization_endpoint": "https://issuer.example/authorize",
		"token_endpoint":         "https://tokens.example/token",
	})
	defer unbound.Close()
	service := newLoginService(t, unbound.URL+"/mcp")
	response, err := service.OauthLogin(&MCPServerOauthLoginParams{Name: "docs", Scopes: []string{"read"}})
	if err == nil || !strings.Contains(err.Error(), "OAuth token endpoint origin does not match the authorization server origin without issuer-bound callbacks") {
		t.Fatalf("OauthLogin() error = %v, want the unbound authorization server rejected", err)
	}
	if !mcpOAuthIssuerBindingRejected(err) {
		t.Fatalf("OauthLogin() error = %v, want a binding rejection rather than a generic discovery failure", err)
	}
	if response != nil {
		t.Fatalf("OauthLogin() = %#v, want no guessed fallback authorization URL", response)
	}

	// The same rejection must also surface on the legacy URL-building path that
	// runs when the interactive login server cannot start at all (no credential
	// store: the service has no CodexHome), instead of returning a guessed URL.
	timeoutSecs := uint64(5)
	stateless := NewMCPService(&RuntimeConfig{
		Servers: map[string]ServerRegistration{
			"docs": {Config: ServerConfig{URL: unbound.URL + "/mcp", OAuthClientID: "client-1", Enabled: true}},
		},
	})
	t.Cleanup(func() { _ = stateless.Close() })
	response, err = stateless.OauthLogin(&MCPServerOauthLoginParams{Name: "docs", Scopes: []string{"read"}, TimeoutSecs: &timeoutSecs})
	if err == nil || response != nil {
		t.Fatalf("OauthLogin() = (%#v, %v), want the unbound authorization server rejected without a fallback URL", response, err)
	}

	// Positive control: metadata that satisfies the binding rules still starts
	// the interactive login against the discovered endpoint.
	bound := newMCPOAuthAuthorizationServerFixture(t, map[string]any{
		"authorization_endpoint": "https://issuer.example/authorize",
		"token_endpoint":         "https://issuer.example/token",
	})
	defer bound.Close()
	boundService := newLoginService(t, bound.URL+"/mcp")
	login, err := boundService.OauthLogin(&MCPServerOauthLoginParams{Name: "docs", Scopes: []string{"read"}})
	if err != nil {
		t.Fatalf("OauthLogin() error = %v, want the bound authorization server accepted", err)
	}
	if login == nil || !strings.Contains(login.AuthorizationURL, "https://issuer.example/authorize") {
		t.Fatalf("login = %#v, want the discovered authorization endpoint used", login)
	}
}
