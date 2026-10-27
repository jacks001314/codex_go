package mcp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	managedconfig "codex_go/config"
)

// TestEMATokenSourceErrorClassificationLikeRust mirrors #55691: guidance and
// sign-in classification depend on the credential kind and exchange stage and
// never include provider text.
func TestEMATokenSourceErrorClassificationLikeRust(t *testing.T) {
	enterpriseInvalidGrant := &EMAAuthFailure{Code: EMAAuthFailureInvalidGrant, GrantSource: EMAInvalidGrantSourceEnterpriseIdentity}
	resourceInvalidGrant := &EMAAuthFailure{Code: EMAAuthFailureInvalidGrant, GrantSource: EMAInvalidGrantSourceResourceAuthorization}
	enterpriseInsufficient := &EMAAuthFailure{Code: EMAAuthFailureInsufficientUserAuthn, GrantSource: EMAInvalidGrantSourceEnterpriseIdentity}
	resourceInsufficient := &EMAAuthFailure{Code: EMAAuthFailureInsufficientUserAuthn, GrantSource: EMAInvalidGrantSourceResourceAuthorization}
	rejected := &EMAAuthFailure{Code: EMAAuthFailureIdentityClientRejected}

	for _, test := range []struct {
		name       string
		credential EMACredentialKind
		failure    *EMAAuthFailure
		message    string
		signIn     bool
	}{
		{"account reauthentication", EMACredentialKindAccount, &EMAAuthFailure{Code: EMAAuthFailureReauthenticationRequired}, "Sign in to your Codex account to restore enterprise MCP access", true},
		{"account identity grant", EMACredentialKindAccount, enterpriseInvalidGrant, "Complete enterprise sign-in for this MCP server", true},
		{"account identity challenge", EMACredentialKindAccount, enterpriseInsufficient, "Complete enterprise sign-in for this MCP server", true},
		{"account client rejection", EMACredentialKindAccount, rejected, "Complete enterprise sign-in for this MCP server", true},
		{"account resource grant", EMACredentialKindAccount, resourceInvalidGrant, "The MCP authorization server rejected the enterprise grant: invalid_grant", false},
		{"keyring identity grant", EMACredentialKindKeyring, enterpriseInvalidGrant, "Complete enterprise sign-in to restore MCP access", true},
		{"keyring reauthentication", EMACredentialKindKeyring, &EMAAuthFailure{Code: EMAAuthFailureReauthenticationRequired}, "Complete enterprise sign-in to restore MCP access", true},
		{"keyring client rejection", EMACredentialKindKeyring, rejected, "The enterprise IdP rejected the configured MCP OAuth client; contact your administrator", false},
		{"keyring resource grant", EMACredentialKindKeyring, resourceInvalidGrant, "The MCP authorization server rejected the enterprise grant: invalid_grant", false},
		{"resource challenge", EMACredentialKindKeyring, resourceInsufficient, "The MCP authorization server requires additional user authentication; contact your administrator", false},
		{"unexpected", EMACredentialKindKeyring, nil, "enterprise MCP token exchange failed", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var err error
			if test.failure == nil {
				err = UnexpectedEMATokenSourceError(test.credential)
			} else {
				err = NewEMATokenSourceError(test.credential, test.failure)
			}
			if got := err.Error(); got != test.message {
				t.Fatalf("Error() = %q, want %q", got, test.message)
			}
			var tokenSourceErr *EMATokenSourceError
			if !errors.As(err, &tokenSourceErr) {
				t.Fatalf("error is not an EMATokenSourceError: %v", err)
			}
			if got := tokenSourceErr.IsAuthenticationRequired(); got != test.signIn {
				t.Fatalf("IsAuthenticationRequired() = %v, want %v", got, test.signIn)
			}
			if !test.signIn && containsProviderCredentialText(err.Error()) {
				t.Fatalf("guidance leaked provider text: %q", err.Error())
			}
		})
	}
}

func containsProviderCredentialText(text string) bool {
	for _, marker := range []string{"refresh-token", "idp-secret", "resource-secret", "account-refresh"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func mcpEMAAuthTestServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

// TestMCPEMAExchangeClassifiesAuthFailuresByStageLikeRust mirrors #55691: an
// unauthorized or invalid client is the identity registration's failure, an
// insufficient user authentication keeps its exchange stage, and the resource
// stage stays a provider failure.
func TestMCPEMAExchangeClassifiesAuthFailuresByStageLikeRust(t *testing.T) {
	now := time.Now().Unix()
	for _, test := range []struct {
		name           string
		idpError       string
		resourceError  string
		wantCode       EMAAuthFailureCode
		wantSource     EMAInvalidGrantSource
		wantPlainError bool
	}{
		{"idp unauthorized client", "unauthorized_client", "", EMAAuthFailureIdentityClientRejected, "", false},
		{"idp invalid client", "invalid_client", "", EMAAuthFailureIdentityClientRejected, "", false},
		{"idp insufficient user authentication", "insufficient_user_authentication", "", EMAAuthFailureInsufficientUserAuthn, EMAInvalidGrantSourceEnterpriseIdentity, false},
		{"idp invalid grant", "invalid_grant", "", EMAAuthFailureInvalidGrant, EMAInvalidGrantSourceEnterpriseIdentity, false},
		{"resource unauthorized client", "", "unauthorized_client", "", "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := mcpEMAAuthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				issuer := "http://" + r.Host + "/idp"
				audience := "http://" + r.Host + "/as"
				resource := "http://" + r.Host + "/mcp"
				switch r.URL.Path {
				case "/idp/token":
					if test.idpError != "" {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusBadRequest)
						_, _ = w.Write([]byte(`{"error":"` + test.idpError + `"}`))
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
					if test.resourceError != "" {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusBadRequest)
						_, _ = w.Write([]byte(`{"error":"` + test.resourceError + `"}`))
						return
					}
					writeJSON(t, w, mcpEMATestTokenResponse())
				default:
					http.NotFound(w, r)
				}
			})
			_, err := ExchangeMCPEMAIDJAG(context.Background(), MCPEMAIDJAGExchangeRequest{
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
			})
			var failure *EMAAuthFailure
			if test.wantPlainError {
				if err == nil || errors.As(err, &failure) || !strings.Contains(err.Error(), test.resourceError) {
					t.Fatalf("resource-stage error = %v, want an unclassified provider failure", err)
				}
				if containsProviderCredentialText(err.Error()) {
					t.Fatalf("provider failure leaked credentials: %v", err)
				}
				return
			}
			if !errors.As(err, &failure) || failure.Code != test.wantCode || failure.GrantSource != test.wantSource {
				t.Fatalf("failure = %#v (err=%v), want code %q source %q", failure, err, test.wantCode, test.wantSource)
			}
		})
	}
}

func mcpEMAPolicyTestBundle(id string) managedconfig.CloudConfigBundle {
	return managedconfig.CloudConfigBundle{ConfigTOML: managedconfig.CloudConfigTOMLBundle{EnterpriseManaged: []managedconfig.CloudConfigFragment{{
		ID: id, Name: id, Contents: `model = "gpt-5"`,
	}}}}
}

// TestEMAAuthManagerHoldsRequestsWhilePolicyStaleLikeRust mirrors #55691: a
// suspended or superseded enterprise policy stops a new dispatch before any
// credential or network work, and a manager built from the freshly published
// revision admits the request again.
func TestEMAAuthManagerHoldsRequestsWhilePolicyStaleLikeRust(t *testing.T) {
	var requests atomic.Int64
	server := mcpEMAAuthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.NotFound(w, r)
	})
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
	serverConfig := &ServerConfig{
		URL:           server.URL + "/mcp",
		Auth:          ServerAuthEMAAuth,
		OAuthClientID: "mcp-client",
		OAuthResource: server.URL + "/mcp",
		Scopes:        []string{"files.read"},
	}
	policy := managedconfig.NewCloudConfigBundlePolicy()
	first := mcpEMAPolicyTestBundle("first")
	if policy.ObserveRemoteBundle(&first) == nil {
		t.Fatal("first observation returned no revision")
	}
	firstSnapshot := managedconfig.CloudConfigBundleSnapshot{Bundle: &first}
	policy.PublishSnapshot(&firstSnapshot)
	if firstSnapshot.Binding == nil || firstSnapshot.Binding.Status() != managedconfig.CloudConfigBundleBindingCurrent {
		t.Fatalf("first snapshot binding = %#v", firstSnapshot.Binding)
	}
	manager, err := NewEMAAuthManager("enterprise", serverConfig, EMAAuthOptions{
		CodexHome:     home,
		StoreMode:     OAuthCredentialsStoreFile,
		Profile:       profile,
		Scope:         scope,
		PolicyBinding: firstSnapshot.Binding,
		HTTPClient:    server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	newRequest := func() *http.Request {
		request, err := http.NewRequest(http.MethodPost, serverConfig.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		return request
	}
	var notSent *MCPEMARequestNotSentError
	if err := manager.ApplyHTTPRequest(newRequest(), nil); err == nil || errors.As(err, &notSent) {
		t.Fatalf("current binding ApplyHTTPRequest() error = %v, want a dispatched request", err)
	}
	if requests.Load() == 0 {
		t.Fatal("current binding did not dispatch the request")
	}

	second := mcpEMAPolicyTestBundle("second")
	if policy.ObserveRemoteBundle(&second) == nil {
		t.Fatal("changed observation returned no revision")
	}
	if got := firstSnapshot.Binding.Status(); got != managedconfig.CloudConfigBundleBindingSuspended {
		t.Fatalf("binding status = %v, want suspended", got)
	}
	before := requests.Load()
	held := newRequest()
	if err := manager.ApplyHTTPRequest(held, nil); !errors.As(err, &notSent) {
		t.Fatalf("suspended policy ApplyHTTPRequest() error = %v, want a held request", err)
	}
	if value := held.Header.Get("Authorization"); value != "" {
		t.Fatalf("held request received Authorization %q", value)
	}
	if got := requests.Load(); got != before {
		t.Fatalf("held request reached the network: requests %d -> %d", before, got)
	}

	secondSnapshot := managedconfig.CloudConfigBundleSnapshot{Bundle: &second}
	policy.PublishSnapshot(&secondSnapshot)
	if secondSnapshot.Binding == nil || secondSnapshot.Binding.Status() != managedconfig.CloudConfigBundleBindingCurrent {
		t.Fatalf("second snapshot binding = %#v", secondSnapshot.Binding)
	}
	if got := firstSnapshot.Binding.Status(); got != managedconfig.CloudConfigBundleBindingStale {
		t.Fatalf("superseded binding status = %v, want stale", got)
	}
	if err := manager.ApplyHTTPRequest(newRequest(), nil); !errors.As(err, &notSent) {
		t.Fatalf("stale binding ApplyHTTPRequest() error = %v, want a held request", err)
	}
	recovered, err := NewEMAAuthManager("enterprise", serverConfig, EMAAuthOptions{
		CodexHome:     home,
		StoreMode:     OAuthCredentialsStoreFile,
		Profile:       profile,
		Scope:         scope,
		PolicyBinding: secondSnapshot.Binding,
		HTTPClient:    server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	before = requests.Load()
	if err := recovered.ApplyHTTPRequest(newRequest(), nil); err == nil || errors.As(err, &notSent) {
		t.Fatalf("recovered ApplyHTTPRequest() error = %v, want a dispatched request", err)
	}
	if got := requests.Load(); got == before {
		t.Fatalf("recovered binding did not dispatch the request (requests %d)", got)
	}
}

// TestEMAAuthManagerPrefersPolicyChangeOverRenewalFailureLikeRust mirrors
// #55691: a policy revision delivered while the token is being renewed
// supersedes the credential failure, because the request was never admitted.
func TestEMAAuthManagerPrefersPolicyChangeOverRenewalFailureLikeRust(t *testing.T) {
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
	policy := managedconfig.NewCloudConfigBundlePolicy()
	first := mcpEMAPolicyTestBundle("first")
	if policy.ObserveRemoteBundle(&first) == nil {
		t.Fatal("first observation returned no revision")
	}
	firstSnapshot := managedconfig.CloudConfigBundleSnapshot{Bundle: &first}
	policy.PublishSnapshot(&firstSnapshot)
	if firstSnapshot.Binding == nil {
		t.Fatal("first snapshot carries no binding")
	}
	second := mcpEMAPolicyTestBundle("second")
	client := &http.Client{Transport: oauthRoundTripFunc(func(*http.Request) (*http.Response, error) {
		if policy.ObserveRemoteBundle(&second) == nil {
			t.Error("changed observation returned no revision")
		}
		return nil, errors.New("credential refresh transport unavailable")
	})}
	manager, err := NewEMAAuthManager("enterprise", &ServerConfig{
		URL:           "https://resource.example/mcp",
		Auth:          ServerAuthEMAAuth,
		OAuthClientID: "mcp-client",
		OAuthResource: "https://resource.example/mcp",
		Scopes:        []string{"files.read"},
	}, EMAAuthOptions{
		CodexHome:     home,
		StoreMode:     OAuthCredentialsStoreFile,
		Profile:       profile,
		Scope:         scope,
		PolicyBinding: firstSnapshot.Binding,
		HTTPClient:    client,
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, "https://resource.example/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	var notSent *MCPEMARequestNotSentError
	applyErr := manager.ApplyHTTPRequest(request, nil)
	if !errors.As(applyErr, &notSent) {
		t.Fatalf("ApplyHTTPRequest() error = %v, want a held request", applyErr)
	}
	if strings.Contains(applyErr.Error(), "credential refresh transport unavailable") {
		t.Fatalf("held request leaked the renewal failure: %v", applyErr)
	}
	if err := manager.ApplyHTTPRequest(request, nil); !errors.As(err, &notSent) {
		t.Fatalf("revoked policy ApplyHTTPRequest() error = %v, want a held request", err)
	}
}

// TestEMAAuthManagerInvalidatesOnlyRejectedEnterpriseGrantsLikeRust mirrors
// #55691: an enterprise IdP invalid_grant retires the stored grant, while a
// client rejection or a resource authorization rejection leaves the login
// intact so an administrator can repair it without a new sign-in.
func TestEMAAuthManagerInvalidatesOnlyRejectedEnterpriseGrantsLikeRust(t *testing.T) {
	now := time.Now().Unix()
	for _, test := range []struct {
		name          string
		idpError      string
		resourceError string
		wantDeleted   bool
		wantSignIn    bool
		wantMessage   string
	}{
		{"idp invalid grant", "invalid_grant", "", true, true, "Complete enterprise sign-in to restore MCP access"},
		{"idp client rejected", "invalid_client", "", false, false, "The enterprise IdP rejected the configured MCP OAuth client; contact your administrator"},
		{"resource invalid grant", "", "invalid_grant", false, false, "The MCP authorization server rejected the enterprise grant: invalid_grant"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := mcpEMAAuthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/.well-known/oauth-authorization-server/mcp":
					writeJSON(t, w, map[string]any{
						"authorization_endpoint":                "http://" + r.Host + "/as/authorize",
						"token_endpoint":                        "http://" + r.Host + "/as/token",
						"token_endpoint_auth_methods_supported": []string{"none"},
					})
				case "/.well-known/oauth-authorization-server/idp":
					writeJSON(t, w, map[string]any{
						"issuer":                                "http://" + r.Host + "/idp",
						"authorization_endpoint":                "http://" + r.Host + "/idp/authorize",
						"token_endpoint":                        "http://" + r.Host + "/idp/token",
						"token_endpoint_auth_methods_supported": []string{"none"},
					})
				case "/idp/token":
					if test.idpError != "" {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusBadRequest)
						_, _ = w.Write([]byte(`{"error":"` + test.idpError + `"}`))
						return
					}
					claims := mcpEMATestClaimsAt("http://"+r.Host+"/idp", "http://"+r.Host+"/mcp", "http://"+r.Host+"/mcp", now)
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
					if test.resourceError != "" {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusBadRequest)
						_, _ = w.Write([]byte(`{"error":"` + test.resourceError + `"}`))
						return
					}
					writeJSON(t, w, mcpEMATestTokenResponse())
				default:
					http.NotFound(w, r)
				}
			})
			home := t.TempDir()
			profile := managedconfig.MCPEnterpriseManagedAuthConfig{IDP: managedconfig.MCPServerIdPOAuthConfig{
				Issuer: server.URL + "/idp", ClientID: "idp-client",
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
			manager, err := NewEMAAuthManager("enterprise", &ServerConfig{
				URL:           server.URL + "/mcp",
				Auth:          ServerAuthEMAAuth,
				OAuthClientID: "mcp-client",
				OAuthResource: server.URL + "/mcp",
				Scopes:        []string{"files.read"},
			}, EMAAuthOptions{
				CodexHome:  home,
				StoreMode:  OAuthCredentialsStoreFile,
				Profile:    profile,
				Scope:      scope,
				HTTPClient: server.Client(),
			})
			if err != nil {
				t.Fatal(err)
			}
			request, err := http.NewRequest(http.MethodPost, server.URL+"/mcp", nil)
			if err != nil {
				t.Fatal(err)
			}
			err = manager.ApplyHTTPRequest(request, nil)
			var tokenSourceErr *EMATokenSourceError
			if !errors.As(err, &tokenSourceErr) {
				t.Fatalf("ApplyHTTPRequest() error = %v, want a token-source error", err)
			}
			if got := tokenSourceErr.Error(); got != test.wantMessage {
				t.Fatalf("guidance = %q, want %q", got, test.wantMessage)
			}
			if got := tokenSourceErr.IsAuthenticationRequired(); got != test.wantSignIn {
				t.Fatalf("IsAuthenticationRequired() = %v, want %v", got, test.wantSignIn)
			}
			stored, err := store.Load(credentialName, profile.IDP.Issuer)
			if err != nil {
				t.Fatal(err)
			}
			deleted := stored == nil || strings.TrimSpace(stored.RefreshToken) == ""
			if deleted != test.wantDeleted {
				t.Fatalf("stored grant deleted = %v, want %v (stored=%#v)", deleted, test.wantDeleted, stored)
			}
		})
	}
}

// countingMCPHTTPDoer records dispatches for the scope/policy boundary test.
type countingMCPHTTPDoer struct {
	calls atomic.Int64
}

func (d *countingMCPHTTPDoer) Do(*http.Request) (*http.Response, error) {
	d.calls.Add(1)
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: http.NoBody}, nil
}

// TestEMAAuthManagerPolicyCheckedHTTPClientHoldsExchangesLikeRust mirrors
// #55696: discovery and token-exchange requests are refused before they leave
// the process once the bound policy revision stops authorizing the manager.
func TestEMAAuthManagerPolicyCheckedHTTPClientHoldsExchangesLikeRust(t *testing.T) {
	policy := managedconfig.NewCloudConfigBundlePolicy()
	first := mcpEMAPolicyTestBundle("first")
	if policy.ObserveRemoteBundle(&first) == nil {
		t.Fatal("first observation returned no revision")
	}
	firstSnapshot := managedconfig.CloudConfigBundleSnapshot{Bundle: &first}
	policy.PublishSnapshot(&firstSnapshot)
	if firstSnapshot.Binding == nil {
		t.Fatal("first snapshot carries no binding")
	}
	var calls atomic.Int64
	client := &http.Client{Transport: oauthRoundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("exchange transport reached")
	})}
	manager := &EMAAuthManager{policyBinding: firstSnapshot.Binding}
	checked := manager.policyCheckedHTTPClient(client)
	request, err := http.NewRequest(http.MethodPost, "https://idp.example/token", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checked.Do(request); err == nil || !strings.Contains(err.Error(), "exchange transport reached") {
		t.Fatalf("current policy Do() error = %v, want the exchange transport", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("current policy transport calls = %d, want 1", calls.Load())
	}

	second := mcpEMAPolicyTestBundle("second")
	if policy.ObserveRemoteBundle(&second) == nil {
		t.Fatal("changed observation returned no revision")
	}
	request, err = http.NewRequest(http.MethodPost, "https://idp.example/token", nil)
	if err != nil {
		t.Fatal(err)
	}
	var notSent *MCPEMARequestNotSentError
	if _, err := checked.Do(request); !errors.As(err, &notSent) {
		t.Fatalf("suspended policy Do() error = %v, want a held request", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("held exchange reached the transport: calls = %d", calls.Load())
	}
}

// TestEMAScopeCheckingDoerChecksPolicyLikeRust mirrors #55696: the retained
// enterprise connection checks its authority at the HTTP boundary, so a policy
// revision or account change after ApplyHTTPRequest still stops the dispatch.
func TestEMAScopeCheckingDoerChecksPolicyLikeRust(t *testing.T) {
	policy := managedconfig.NewCloudConfigBundlePolicy()
	first := mcpEMAPolicyTestBundle("first")
	if policy.ObserveRemoteBundle(&first) == nil {
		t.Fatal("first observation returned no revision")
	}
	firstSnapshot := managedconfig.CloudConfigBundleSnapshot{Bundle: &first}
	policy.PublishSnapshot(&firstSnapshot)
	if firstSnapshot.Binding == nil {
		t.Fatal("first snapshot carries no binding")
	}
	scope := EMAAuthScope{UserID: "user-1", WorkspaceID: "account-1"}
	manager := &EMAAuthManager{
		scope:         scope,
		currentScope:  func() (EMAAuthScope, bool) { return scope, true },
		policyBinding: firstSnapshot.Binding,
	}
	next := &countingMCPHTTPDoer{}
	doer := emaScopeCheckingDoer{next: next, manager: manager}
	request, err := http.NewRequest(http.MethodPost, "https://resource.example/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doer.Do(request); err != nil {
		t.Fatalf("current authority Do() error = %v", err)
	}
	if next.calls.Load() != 1 {
		t.Fatalf("current authority dispatches = %d, want 1", next.calls.Load())
	}

	second := mcpEMAPolicyTestBundle("second")
	if policy.ObserveRemoteBundle(&second) == nil {
		t.Fatal("changed observation returned no revision")
	}
	var notSent *MCPEMARequestNotSentError
	if _, err := doer.Do(request); !errors.As(err, &notSent) {
		t.Fatalf("suspended policy Do() error = %v, want a held request", err)
	}
	if next.calls.Load() != 1 {
		t.Fatalf("held request reached the transport: dispatches = %d", next.calls.Load())
	}

	manager.revoked.Store(true)
	if _, err := doer.Do(request); err == nil || !strings.Contains(err.Error(), "revoked") {
		t.Fatalf("revoked authority Do() error = %v", err)
	}
	if next.calls.Load() != 1 {
		t.Fatalf("revoked request reached the transport: dispatches = %d", next.calls.Load())
	}
}
