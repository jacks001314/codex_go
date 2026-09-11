package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func oidcFallbackMetadataJSON(issuer string) string {
	return `{"issuer":"` + issuer + `","authorization_endpoint":"` + issuer + `/authorize","token_endpoint":"` + issuer + `/token","scopes_supported":["read"]}`
}

func TestMCPOAuthOIDCFallbackURLsOrder(t *testing.T) {
	withPath := mcpOAuthOIDCFallbackURLs("https://example.com/.well-known/oauth-authorization-server/tenant")
	if len(withPath) != 2 ||
		withPath[0] != "https://example.com/.well-known/openid-configuration/tenant" ||
		withPath[1] != "https://example.com/tenant/.well-known/openid-configuration" {
		t.Fatalf("path issuer fallback = %#v", withPath)
	}
	root := mcpOAuthOIDCFallbackURLs("https://example.com/.well-known/oauth-authorization-server")
	if len(root) != 1 || root[0] != "https://example.com/.well-known/openid-configuration" {
		t.Fatalf("root issuer fallback = %#v", root)
	}
	if got := mcpOAuthOIDCFallbackURLs("https://example.com/not-metadata"); len(got) != 0 {
		t.Fatalf("non-metadata URL fallback = %#v", got)
	}
}

// Rust #44636: a 503 on the authorization-server metadata falls back to the
// same issuer's OIDC discovery endpoints.
func TestMCPOAuthDiscoveryFallsBackToOIDCOnServiceUnavailable(t *testing.T) {
	var requested []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = append(requested, r.URL.Path)
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server/mcp":
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
		case "/.well-known/openid-configuration/mcp":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(oidcFallbackMetadataJSON("https://example.com/tenant")))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	discovery, ok, err := discoverMCPOAuthAuthorizationServer(context.Background(), server.Client(), server.URL+"/mcp")
	if err != nil || !ok || discovery == nil {
		t.Fatalf("discovery = %#v ok=%v err=%v", discovery, ok, err)
	}
	if discovery.AuthorizationEndpoint != "https://example.com/tenant/authorize" || discovery.TokenEndpoint != "https://example.com/tenant/token" {
		t.Fatalf("discovery endpoints = %#v", discovery)
	}
	if len(requested) == 0 || requested[0] != "/.well-known/oauth-authorization-server/mcp" || requested[len(requested)-1] != "/.well-known/openid-configuration/mcp" {
		t.Fatalf("request order = %#v", requested)
	}
}

// The OIDC candidates are tried in RMCP's order, and a 404 on the first does
// not stop the path-suffixed candidate.
func TestMCPOAuthDiscoveryOIDCFallbackTriesSuffixedCandidate(t *testing.T) {
	const issuerPath = "/.well-known/oauth-authorization-server/tenant"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case issuerPath, "/.well-known/oauth-authorization-server":
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		case "/.well-known/openid-configuration/tenant":
			http.NotFound(w, r)
		case "/tenant/.well-known/openid-configuration":
			_, _ = w.Write([]byte(oidcFallbackMetadataJSON("https://example.com/tenant")))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	discovery, ok, err := discoverMCPOAuthAuthorizationServer(context.Background(), server.Client(), server.URL+"/tenant")
	if err != nil || !ok || discovery == nil || discovery.TokenEndpoint != "https://example.com/tenant/token" {
		t.Fatalf("discovery = %#v ok=%v err=%v", discovery, ok, err)
	}
}

// When no OIDC candidate is usable the original discovery error is preserved
// instead of enabling a lower-priority endpoint fallback.
func TestMCPOAuthDiscoveryOIDCFallbackPreservesOriginalError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server/mcp", "/.well-known/oauth-authorization-server":
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		case "/.well-known/openid-configuration/mcp":
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	_, ok, err := discoverMCPOAuthAuthorizationServer(context.Background(), server.Client(), server.URL+"/mcp")
	if ok || err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("discovery ok=%v err=%v, want preserved 503 error", ok, err)
	}
}

// Terminal statuses on an OIDC candidate propagate instead of being treated as
// a missing endpoint.
func TestMCPOAuthDiscoveryOIDCFallbackPropagatesTerminalStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server/mcp":
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		case "/.well-known/openid-configuration/mcp":
			http.Error(w, "slow down", http.StatusTooManyRequests)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	_, ok, err := discoverMCPOAuthAuthorizationServer(context.Background(), server.Client(), server.URL+"/mcp")
	if ok || err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("discovery ok=%v err=%v, want propagated 429", ok, err)
	}
}

// Fallback discovery does not follow redirects (RMCP owns discovery redirect
// policy), so a redirect response keeps the original 503 error.
func TestMCPOAuthDiscoveryOIDCFallbackDoesNotFollowRedirects(t *testing.T) {
	var redirected bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server/mcp":
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		case "/.well-known/openid-configuration/mcp":
			http.Redirect(w, r, "/real-metadata", http.StatusFound)
		case "/real-metadata":
			redirected = true
			_, _ = w.Write([]byte(oidcFallbackMetadataJSON("https://example.com/tenant")))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	_, ok, err := discoverMCPOAuthAuthorizationServer(context.Background(), server.Client(), server.URL+"/mcp")
	if ok || err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("discovery ok=%v err=%v, want preserved 503", ok, err)
	}
	if redirected {
		t.Fatal("fallback discovery followed a redirect")
	}
}
