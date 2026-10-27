package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	managedconfig "codex_go/config"
)

func mcpEMATestOIDCToken(t *testing.T, issuer string, audience string, expiresAt int64) string {
	t.Helper()
	return mcpEMATestJWT(t, mcpEMATestJAGHeader(), map[string]any{
		"iss": issuer,
		"sub": "account-user",
		"aud": audience,
		"exp": expiresAt,
	})
}

// TestMCPEMAOIDCIdentityRequiresNonEmptyExpectationsLikeRust mirrors #55685:
// an empty expected issuer or client can never bind an assertion, even when
// the token carries matching empty claims.
func TestMCPEMAOIDCIdentityRequiresNonEmptyExpectationsLikeRust(t *testing.T) {
	const issuer = "https://idp.example"
	const clientID = "idp-client"
	assertion := mcpEMATestOIDCToken(t, issuer, clientID, time.Now().Add(time.Hour).Unix())
	if _, err := mcpEMAOIDCIdentity(assertion, " 	", clientID); err == nil ||
		!strings.Contains(err.Error(), "non-empty expected issuer and client") {
		t.Fatalf("whitespace issuer error = %v", err)
	}
	if _, err := mcpEMAOIDCIdentity(assertion, issuer, ""); err == nil ||
		!strings.Contains(err.Error(), "non-empty expected issuer and client") {
		t.Fatalf("empty client error = %v", err)
	}
	if _, err := mcpEMAOIDCIdentity(assertion, issuer, clientID); err != nil {
		t.Fatalf("matching expectations error = %v", err)
	}
	if MCPEMAAccountOIDCIdentityIsUsable(assertion, "", "") {
		t.Fatal("an empty expected issuer and client must never be usable")
	}
}

// TestMCPEMAAccountOIDCIdentityIgnoresExpiryLikeRust mirrors #55685: an
// expired ID token still corroborates the refresh grant's provenance, so only
// a strict assertion validation treats it as expired.
func TestMCPEMAAccountOIDCIdentityIgnoresExpiryLikeRust(t *testing.T) {
	const issuer = "https://idp.example"
	const clientID = "idp-client"
	expired := mcpEMATestOIDCToken(t, issuer, clientID, time.Now().Add(-time.Hour).Unix())
	if !MCPEMAAccountOIDCIdentityIsUsable(expired, issuer, clientID) {
		t.Fatal("an expired ID token must not invalidate the refresh grant")
	}
	if err := ValidateMCPEMAOIDCIdentityAssertion(expired, issuer, clientID); err == nil ||
		!strings.Contains(err.Error(), "expired") {
		t.Fatalf("ValidateMCPEMAOIDCIdentityAssertion() error = %v, want expired", err)
	}
	if MCPEMAAccountOIDCIdentityIsUsable(expired, "https://other.example", clientID) {
		t.Fatal("a token from another issuer must not be usable")
	}
	if MCPEMAAccountOIDCIdentityIsUsable(expired, issuer, "other-client") {
		t.Fatal("a token issued to another client must not be usable")
	}

	if !MCPEMAAccountOIDCIssuerHint(expired, issuer) {
		t.Fatal("the unverified issuer hint should see the matching issuer")
	}
	if MCPEMAAccountOIDCIssuerHint(expired, "https://other.example") {
		t.Fatal("the issuer hint must compare the issuer claim")
	}
	unsignedHeader, err := json.Marshal(map[string]any{"alg": "none", "typ": "JWT"})
	if err != nil {
		t.Fatalf("marshal unsigned header: %v", err)
	}
	claims, err := json.Marshal(map[string]any{"iss": issuer, "sub": "account-user", "aud": clientID, "exp": time.Now().Unix()})
	if err != nil {
		t.Fatalf("marshal unsigned claims: %v", err)
	}
	unsigned := base64.RawURLEncoding.EncodeToString(unsignedHeader) + "." +
		base64.RawURLEncoding.EncodeToString(claims) + ".signature"
	if MCPEMAAccountOIDCIssuerHint(unsigned, issuer) {
		t.Fatal("an unsigned token must not produce an issuer hint")
	}
}

// TestResolveMCPEMAAccountIDPIdentityLikeRust mirrors #55685: the account IdP
// identity resolver binds the account record to trusted issuer and client
// provenance, then discovers the refresh-token endpoint.
func TestResolveMCPEMAAccountIDPIdentityLikeRust(t *testing.T) {
	ctx := context.Background()
	capableIdP := func(t *testing.T) *httptest.Server {
		t.Helper()
		return enterpriseTestIdP(t, func(metadata map[string]any) {
			metadata["identity_chaining_requested_token_types_supported"] = []string{idJAGTokenType}
			metadata["grant_types_supported"] = []string{mcpEMATokenExchangeGrantType}
		}, func() map[string]any { return map[string]any{} })
	}

	t.Run("resolves the discovered token endpoint", func(t *testing.T) {
		server := capableIdP(t)
		// An expired ID token must still corroborate the refresh grant.
		idToken := mcpEMATestOIDCToken(t, server.URL, "idp-client", time.Now().Add(-time.Minute).Unix())
		lease := &struct{ name string }{name: "account-grant"}
		identity, err := ResolveMCPEMAAccountIDPIdentity(ctx, MCPEMAAccountIDPIdentityRequest[*struct{ name string }]{
			IdP:          managedconfig.MCPServerIdPOAuthConfig{Issuer: server.URL, ClientID: "idp-client"},
			ClientID:     "idp-client",
			IDToken:      idToken,
			RefreshToken: "account-refresh-token",
			Credentials:  lease,
			HTTPClient:   server.Client(),
		})
		if err != nil {
			t.Fatalf("ResolveMCPEMAAccountIDPIdentity() error = %v", err)
		}
		if identity.TokenEndpoint != server.URL+"/token" ||
			identity.RefreshToken != "account-refresh-token" ||
			identity.Credentials != lease {
			t.Fatalf("identity = %#v", identity)
		}
		if identity.ClientAuthentication.Method != "" {
			t.Fatalf("public-client authentication = %#v, want the public form", identity.ClientAuthentication)
		}
		mapped := MapMCPEMAIDPIdentityCredentials(identity, func(credentials *struct{ name string }) string {
			return credentials.name
		})
		if mapped.Credentials != "account-grant" || mapped.TokenEndpoint != identity.TokenEndpoint ||
			mapped.RefreshToken != identity.RefreshToken {
			t.Fatalf("mapped identity = %#v", mapped)
		}
	})

	t.Run("requires an account refresh token", func(t *testing.T) {
		server := capableIdP(t)
		_, err := ResolveMCPEMAAccountIDPIdentity(ctx, MCPEMAAccountIDPIdentityRequest[struct{}]{
			IdP:          managedconfig.MCPServerIdPOAuthConfig{Issuer: server.URL, ClientID: "idp-client"},
			ClientID:     "idp-client",
			IDToken:      mcpEMATestOIDCToken(t, server.URL, "idp-client", time.Now().Add(time.Hour).Unix()),
			RefreshToken: "  ",
			HTTPClient:   server.Client(),
		})
		var failure *EMAAuthFailure
		if !errors.As(err, &failure) || failure.Code != EMAAuthFailureReauthenticationRequired ||
			!strings.Contains(err.Error(), "sign in again") {
			t.Fatalf("empty refresh token error = %v", err)
		}
	})

	t.Run("rejects an ID token bound to another issuer", func(t *testing.T) {
		server := capableIdP(t)
		_, err := ResolveMCPEMAAccountIDPIdentity(ctx, MCPEMAAccountIDPIdentityRequest[struct{}]{
			IdP:          managedconfig.MCPServerIdPOAuthConfig{Issuer: server.URL, ClientID: "idp-client"},
			ClientID:     "idp-client",
			IDToken:      mcpEMATestOIDCToken(t, "https://other.example", "idp-client", time.Now().Add(time.Hour).Unix()),
			RefreshToken: "account-refresh-token",
			HTTPClient:   server.Client(),
		})
		if err == nil || !strings.Contains(err.Error(), "issuer or subject") {
			t.Fatalf("mismatched issuer error = %v", err)
		}
	})

	t.Run("rejects a provisioned secret for another account client", func(t *testing.T) {
		server := capableIdP(t)
		_, err := ResolveMCPEMAAccountIDPIdentity(ctx, MCPEMAAccountIDPIdentityRequest[struct{}]{
			IdP: managedconfig.MCPServerIdPOAuthConfig{
				Issuer:       server.URL,
				ClientID:     "idp-client",
				ClientSecret: "idp-secret",
			},
			ClientID:     "other-client",
			IDToken:      mcpEMATestOIDCToken(t, server.URL, "other-client", time.Now().Add(time.Hour).Unix()),
			RefreshToken: "account-refresh-token",
			HTTPClient:   server.Client(),
		})
		if err == nil || !strings.Contains(err.Error(), "does not belong to the account OAuth client") {
			t.Fatalf("confidential mismatch error = %v", err)
		}
	})

	t.Run("rejects an IdP that omits the required capability", func(t *testing.T) {
		server := enterpriseTestIdP(t, func(metadata map[string]any) {
			metadata["grant_types_supported"] = []string{"authorization_code"}
		}, func() map[string]any { return map[string]any{} })
		_, err := ResolveMCPEMAAccountIDPIdentity(ctx, MCPEMAAccountIDPIdentityRequest[struct{}]{
			IdP:          managedconfig.MCPServerIdPOAuthConfig{Issuer: server.URL, ClientID: "idp-client"},
			ClientID:     "idp-client",
			IDToken:      mcpEMATestOIDCToken(t, server.URL, "idp-client", time.Now().Add(time.Hour).Unix()),
			RefreshToken: "account-refresh-token",
			HTTPClient:   server.Client(),
		})
		if err == nil || !strings.Contains(err.Error(), "does not advertise the required ID-JAG token exchange capability") {
			t.Fatalf("capability error = %v", err)
		}
	})

	t.Run("rejects a malformed capability advertisement", func(t *testing.T) {
		server := enterpriseTestIdP(t, func(metadata map[string]any) {
			metadata["identity_chaining_requested_token_types_supported"] = nil
		}, func() map[string]any { return map[string]any{} })
		_, err := ResolveMCPEMAAccountIDPIdentity(ctx, MCPEMAAccountIDPIdentityRequest[struct{}]{
			IdP:          managedconfig.MCPServerIdPOAuthConfig{Issuer: server.URL, ClientID: "idp-client"},
			ClientID:     "idp-client",
			IDToken:      mcpEMATestOIDCToken(t, server.URL, "idp-client", time.Now().Add(time.Hour).Unix()),
			RefreshToken: "account-refresh-token",
			HTTPClient:   server.Client(),
		})
		if err == nil || !strings.Contains(err.Error(), "is malformed") {
			t.Fatalf("malformed capability error = %v", err)
		}
	})

	t.Run("tolerates an IdP that publishes no capability lists", func(t *testing.T) {
		server := enterpriseTestIdP(t, nil, func() map[string]any { return map[string]any{} })
		identity, err := ResolveMCPEMAAccountIDPIdentity(ctx, MCPEMAAccountIDPIdentityRequest[struct{}]{
			IdP:          managedconfig.MCPServerIdPOAuthConfig{Issuer: server.URL, ClientID: "idp-client"},
			ClientID:     "idp-client",
			IDToken:      mcpEMATestOIDCToken(t, server.URL, "idp-client", time.Now().Add(time.Hour).Unix()),
			RefreshToken: "account-refresh-token",
			HTTPClient:   server.Client(),
		})
		if err != nil || identity.TokenEndpoint != server.URL+"/token" {
			t.Fatalf("absent capability identity = (%#v, %v)", identity, err)
		}
	})
}

// TestExchangeMCPEMAIDJAGReleasesCredentialsBeforeResourceIOLikeRust mirrors
// #55685: the IdP exchange consumes the refresh token, so the caller's
// credential lease is released before the resource authorization server sees
// any request and never when the IdP exchange fails.
func TestExchangeMCPEMAIDJAGReleasesCredentialsBeforeResourceIOLikeRust(t *testing.T) {
	newServer := func(t *testing.T, idpStatus int, stages *[]string, mu *sync.Mutex) *httptest.Server {
		t.Helper()
		now := time.Now().Unix()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			*stages = append(*stages, strings.TrimPrefix(r.URL.Path, "/"))
			mu.Unlock()
			issuer := "http://" + r.Host + "/idp"
			audience := "http://" + r.Host + "/as"
			resource := "http://" + r.Host + "/mcp"
			switch r.URL.Path {
			case "/idp/token":
				if idpStatus != http.StatusOK {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(idpStatus)
					_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
					return
				}
				claims := mcpEMATestClaimsAt(issuer, audience, resource, now)
				accessToken, err := mcpEMATestJWTValue(mcpEMATestJAGHeader(), claims)
				if err != nil {
					http.Error(w, "jwt", http.StatusInternalServerError)
					return
				}
				writeJSON(t, w, map[string]any{
					"access_token":      accessToken,
					"issued_token_type": idJAGTokenType,
					"token_type":        "N_A",
					"resource":          claims["resource"],
					"scope":             claims["scope"],
				})
			case "/as/token":
				writeJSON(t, w, mcpEMATestTokenResponse())
			default:
				http.NotFound(w, r)
			}
		}))
		t.Cleanup(server.Close)
		return server
	}

	request := func(server *httptest.Server) MCPEMAIDJAGExchangeRequest {
		return MCPEMAIDJAGExchangeRequest{
			Resource:                         server.URL + "/mcp",
			MCPClientID:                      "mcp-client",
			AuthorizationServerIssuer:        server.URL + "/as",
			AuthorizationServerTokenEndpoint: server.URL + "/as/token",
			IDPTokenEndpoint:                 server.URL + "/idp/token",
			IDPIssuer:                        server.URL + "/idp",
			IDPClientID:                      "idp-client",
			RefreshToken:                     "opaque-refresh-token",
			IDPHTTPClient:                    server.Client(),
			ResourceHTTPClient:               server.Client(),
		}
	}

	t.Run("releases before the resource request", func(t *testing.T) {
		var (
			mu     sync.Mutex
			stages []string
		)
		server := newServer(t, http.StatusOK, &stages, &mu)
		token, receipt, err := ExchangeMCPEMAIDJAGWithCredentials(context.Background(), request(server), "account-lease", func(lease string) string {
			mu.Lock()
			stages = append(stages, "release")
			mu.Unlock()
			return lease + "-receipt"
		})
		if err != nil {
			t.Fatalf("ExchangeMCPEMAIDJAGWithCredentials() error = %v", err)
		}
		if token == nil || token.AccessToken != "resource-token" || receipt != "account-lease-receipt" {
			t.Fatalf("token = %v, receipt = %q", token, receipt)
		}
		mu.Lock()
		defer mu.Unlock()
		if strings.Join(stages, ",") != "idp/token,release,as/token" {
			t.Fatalf("stages = %v, want idp exchange then release then resource exchange", stages)
		}
	})

	t.Run("keeps the lease when the IdP exchange fails", func(t *testing.T) {
		var (
			mu     sync.Mutex
			stages []string
		)
		server := newServer(t, http.StatusBadRequest, &stages, &mu)
		released := false
		_, _, err := ExchangeMCPEMAIDJAGWithCredentials(context.Background(), request(server), "account-lease", func(string) string {
			released = true
			return "unused"
		})
		var failure *EMAAuthFailure
		if !errors.As(err, &failure) || failure.Code != EMAAuthFailureInvalidGrant ||
			failure.GrantSource != EMAInvalidGrantSourceEnterpriseIdentity {
			t.Fatalf("IdP failure = %v", err)
		}
		if released {
			t.Fatal("a failed IdP exchange must not release the credential")
		}
		mu.Lock()
		defer mu.Unlock()
		if strings.Join(stages, ",") != "idp/token" {
			t.Fatalf("stages = %v, want only the IdP exchange", stages)
		}
	})

	t.Run("rejects a missing release callback", func(t *testing.T) {
		var (
			mu     sync.Mutex
			stages []string
		)
		server := newServer(t, http.StatusOK, &stages, &mu)
		if _, _, err := ExchangeMCPEMAIDJAGWithCredentials[string, struct{}](context.Background(), request(server), "lease", nil); err == nil {
			t.Fatal("a missing credential release callback must be rejected")
		}
	})
}
