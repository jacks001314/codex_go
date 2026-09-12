package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func mcpEMATestJWT(t *testing.T, header map[string]any, claims map[string]any) string {
	t.Helper()
	headerBytes, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	claimsBytes, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(headerBytes) + "." +
		base64.RawURLEncoding.EncodeToString(claimsBytes) + ".signature"
}

func mcpEMATestJAGHeader() map[string]any {
	return map[string]any{"alg": "ES256", "typ": "oauth-id-jag+jwt"}
}

func mcpEMATestClaims(issuer string, audience string, resource string) map[string]any {
	return mcpEMATestClaimsAt(issuer, audience, resource, time.Now().Unix())
}

func mcpEMATestClaimsAt(issuer string, audience string, resource string, now int64) map[string]any {
	return map[string]any{
		"iss":       issuer,
		"aud":       audience,
		"sub":       "user",
		"client_id": "mcp-client",
		"jti":       "unique-jag",
		"iat":       now,
		"exp":       now + 3600,
		"resource":  resource,
		"scope":     "files.read",
	}
}

func mcpEMATestJWTValue(header map[string]any, claims map[string]any) (string, error) {
	headerBytes, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	claimsBytes, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(headerBytes) + "." +
		base64.RawURLEncoding.EncodeToString(claimsBytes) + ".signature", nil
}

func mcpEMATestJAGResponse(t *testing.T, header map[string]any, claims map[string]any) map[string]any {
	t.Helper()
	response := map[string]any{
		"access_token":      mcpEMATestJWT(t, header, claims),
		"issued_token_type": idJAGTokenType,
		"token_type":        "N_A",
		"resource":          claims["resource"],
	}
	if scope, ok := claims["scope"]; ok {
		response["scope"] = scope
	}
	return response
}

func mcpEMATestTokenResponse() map[string]any {
	return map[string]any{
		"access_token": "resource-token",
		"token_type":   "Bearer",
		"expires_in":   300,
	}
}

func TestMCPEMAResourceOriginQueryAndPathAreBound(t *testing.T) {
	server := "https://mcp.example/enterprise/tools?tenant=one"
	for _, test := range []struct {
		resource string
		valid    bool
	}{
		{"https://mcp.example/enterprise?tenant=one", true},
		{"https://other.example/enterprise?tenant=one", false},
		{"https://mcp.example/enterprise-admin?tenant=one", false},
		{"https://mcp.example/enterprise?tenant=two", false},
		{"https://mcp.example/enterprise", false},
		{"http://localhost:4000/enterprise?tenant=one", false},
	} {
		resource := test.resource
		if got := ValidateMCPEMAAuthResource(server, &resource) == nil; got != test.valid {
			t.Fatalf("ValidateMCPEMAAuthResource(%q) = %t, want %t", test.resource, got, test.valid)
		}
	}
	for _, test := range []struct {
		server string
		valid  bool
	}{
		{"https://mcp.example/mcp", true},
		{"http://localhost:4000/mcp", true},
		{"http://127.0.0.1:4000/mcp", true},
		{"http://mcp.example/mcp", false},
	} {
		if got := ValidateMCPEMAAuthResource(test.server, nil) == nil; got != test.valid {
			t.Fatalf("ValidateMCPEMAAuthResource(%q, nil) = %t, want %t", test.server, got, test.valid)
		}
	}
}

func TestMCPEMAPublicClientAuthRequiresAdvertisedMethod(t *testing.T) {
	for _, test := range []struct {
		advertised any
		valid      bool
	}{
		{nil, false},
		{[]any{"none"}, true},
		{[]any{"client_secret_basic", "none"}, true},
		{[]any{"private_key_jwt"}, false},
		{"none", false},
	} {
		if got := validateMCPEMAPublicClientAuth(test.advertised, "IdP") == nil; got != test.valid {
			t.Fatalf("validateMCPEMAPublicClientAuth(%#v) = %t, want %t", test.advertised, got, test.valid)
		}
	}
}

func TestMCPEMAExchangePublicClientRoundTripPreservesSignedNarrowing(t *testing.T) {
	requestedScopes := []string{"files.read", "files.write"}
	now := time.Now().Unix()
	for _, test := range []struct {
		scopes       []string
		echoScope    bool
		refreshToken string
	}{
		{requestedScopes, true, "opaque-refresh-token"},
		{nil, true, "opaque-refresh-token"},
		{nil, false, "opaque-refresh-token"},
		{requestedScopes, true, ""},
		{requestedScopes, true, " \t"},
	} {
		var requests []*http.Request
		var bodies []string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			body, _ := io.ReadAll(request.Body)
			requests = append(requests, request)
			bodies = append(bodies, string(body))
			issuer := "http://" + request.Host + "/idp"
			audience := "http://" + request.Host + "/as"
			resource := "http://" + request.Host + "/mcp"
			switch request.URL.Path {
			case "/idp/token":
				claims := mcpEMATestClaimsAt(issuer, audience, resource, now)
				accessToken, err := mcpEMATestJWTValue(mcpEMATestJAGHeader(), claims)
				if err != nil {
					http.Error(w, "jwt", http.StatusInternalServerError)
					return
				}
				jag := map[string]any{
					"access_token":      accessToken,
					"issued_token_type": idJAGTokenType,
					"token_type":        "N_A",
					"resource":          claims["resource"],
				}
				if scope, ok := claims["scope"]; ok {
					jag["scope"] = scope
				}
				if !test.echoScope {
					delete(jag, "scope")
				}
				writeJSON(t, w, jag)
			case "/as/token":
				writeJSON(t, w, mcpEMATestTokenResponse())
			default:
				http.NotFound(w, request)
			}
		}))
		defer server.Close()

		issuer := server.URL + "/idp"
		audience := server.URL + "/as"
		resource := server.URL + "/mcp"
		token, err := ExchangeMCPEMAIDJAG(context.Background(), MCPEMAIDJAGExchangeRequest{
			Resource:                         resource,
			Scopes:                           test.scopes,
			MCPClientID:                      "mcp-client",
			AuthorizationServerIssuer:        audience,
			AuthorizationServerTokenEndpoint: audience + "/token",
			IDPTokenEndpoint:                 issuer + "/token",
			IDPIssuer:                        issuer,
			IDPClientID:                      "idp-client",
			RefreshToken:                     test.refreshToken,
			IDPHTTPClient:                    server.Client(),
			ResourceHTTPClient:               server.Client(),
		})
		if strings.TrimSpace(test.refreshToken) == "" {
			if err == nil {
				t.Fatal("invalid subject must fail before HTTP")
			}
			if len(requests) != 0 {
				t.Fatalf("requests = %d, want 0", len(requests))
			}
			continue
		}
		if err != nil {
			t.Fatalf("ExchangeMCPEMAIDJAG() error = %v", err)
		}
		if token == nil || token.AccessToken != "resource-token" || token.ExpiresIn == nil || *token.ExpiresIn != 300*time.Second {
			t.Fatalf("token = %v", token)
		}
		if len(requests) != 2 {
			t.Fatalf("requests = %d, want 2", len(requests))
		}
		for _, request := range requests {
			if got := request.Header.Get("Authorization"); got != "" {
				t.Fatalf("Authorization = %q, want none", got)
			}
		}
		first := mcpEMATestParseForm(t, bodies[0])
		wantScope := ""
		if len(test.scopes) > 0 {
			wantScope = strings.Join(test.scopes, " ")
		}
		if first["scope"] != wantScope {
			t.Fatalf("idp scope = %q, want %q", first["scope"], wantScope)
		}
		delete(first, "scope")
		wantFirst := map[string]string{
			"grant_type":           mcpEMATokenExchangeGrantType,
			"requested_token_type": idJAGTokenType,
			"subject_token":        test.refreshToken,
			"subject_token_type":   "urn:ietf:params:oauth:token-type:refresh_token",
			"audience":             audience,
			"resource":             resource,
			"client_id":            "idp-client",
		}
		if !mcpEMATestFormEqual(first, wantFirst) {
			t.Fatalf("idp form = %#v, want %#v", first, wantFirst)
		}
		jagToken := mcpEMATestJWT(t, mcpEMATestJAGHeader(), mcpEMATestClaimsAt(issuer, audience, resource, now))
		wantSecond := map[string]string{
			"grant_type": mcpEMAJWTBearerGrantType,
			"assertion":  jagToken,
			"client_id":  "mcp-client",
		}
		if second := mcpEMATestParseForm(t, bodies[1]); !mcpEMATestFormEqual(second, wantSecond) {
			t.Fatalf("as form = %#v, want %#v", second, wantSecond)
		}
	}
}

func mcpEMATestParseForm(t *testing.T, body string) map[string]string {
	t.Helper()
	values, err := url.ParseQuery(body)
	if err != nil {
		t.Fatalf("parse form: %v", err)
	}
	out := map[string]string{}
	for key, list := range values {
		if len(list) != 1 {
			t.Fatalf("form field %q has %d values", key, len(list))
		}
		out[key] = list[0]
	}
	return out
}

func mcpEMATestFormEqual(left map[string]string, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range right {
		if left[key] != value {
			return false
		}
	}
	return true
}

func mcpEMATestStringSet(values ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}

func TestMCPEMASignedClaimsAndResourceTokensCannotWidenAuthority(t *testing.T) {
	requested := mcpEMATestStringSet("files.read", "files.write")
	original := mcpEMATestClaims("https://idp.example", "https://as.example", "https://mcp.example")
	binding := func() MCPEMAIDJAGBinding {
		return MCPEMAIDJAGBinding{
			Issuer:          "https://idp.example",
			Audience:        "https://as.example",
			ClientID:        "mcp-client",
			Resource:        "https://mcp.example",
			RequestedScopes: requested,
		}
	}
	decode := func(t *testing.T, value map[string]any, target any) {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if err := json.Unmarshal(raw, target); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
	}
	var valid mcpEMAIDJAGResponse
	decode(t, mcpEMATestJAGResponse(t, mcpEMATestJAGHeader(), original), &valid)
	granted, err := valid.Validate(binding())
	if err != nil {
		t.Fatalf("valid ID-JAG error = %v", err)
	}
	if len(granted) != 1 {
		t.Fatalf("granted = %#v", granted)
	}

	for _, test := range []struct {
		requestedScopes string
		signedScope     *string
		responseScope   *string
		valid           bool
	}{
		{"", ptrTo("files.read"), ptrTo("files.read"), true},
		{"", ptrTo("files.read"), nil, true},
		{"", nil, nil, true},
		{"files.read", ptrTo("files.read"), nil, true},
		{"files.read files.write", ptrTo("files.read"), nil, false},
		{"files.read", ptrTo("files.read files.write"), ptrTo("files.read files.write"), false},
		{"files.read", nil, nil, false},
		{"", ptrTo("files.read"), ptrTo("files.write"), false},
		{"", ptrTo(" \t"), nil, false},
		{"", ptrTo("files.read files.read"), ptrTo("files.read"), false},
		{"", ptrTo("files.read"), ptrTo(" \t"), false},
		{"", ptrTo("files.read"), ptrTo("files.read files.read"), false},
	} {
		requestedScopes := map[string]struct{}{}
		for _, scope := range strings.Fields(test.requestedScopes) {
			requestedScopes[scope] = struct{}{}
		}
		scopedClaims := map[string]any{}
		for key, value := range original {
			scopedClaims[key] = value
		}
		delete(scopedClaims, "scope")
		if test.signedScope != nil {
			scopedClaims["scope"] = *test.signedScope
		}
		response := mcpEMATestJAGResponse(t, mcpEMATestJAGHeader(), scopedClaims)
		delete(response, "scope")
		if test.responseScope != nil {
			response["scope"] = *test.responseScope
		}
		var decoded mcpEMAIDJAGResponse
		decode(t, response, &decoded)
		grantedScopes, err := decoded.Validate(MCPEMAIDJAGBinding{
			Issuer:          binding().Issuer,
			Audience:        binding().Audience,
			ClientID:        binding().ClientID,
			Resource:        binding().Resource,
			RequestedScopes: requestedScopes,
		})
		if (err == nil) != test.valid {
			t.Fatalf("requested %q signed %v response %v: err = %v, want valid %t", test.requestedScopes, test.signedScope, test.responseScope, err, test.valid)
		}
		if err != nil {
			continue
		}
		wantGranted := map[string]struct{}{}
		if test.signedScope != nil {
			for _, scope := range strings.Fields(*test.signedScope) {
				wantGranted[scope] = struct{}{}
			}
		}
		if !mcpEMAScopeSetEqual(grantedScopes, wantGranted) {
			t.Fatalf("granted = %#v, want %#v", grantedScopes, wantGranted)
		}
		for _, tokenTest := range []struct {
			scope *string
			valid bool
		}{
			{nil, true},
			{ptrTo("files.read"), test.signedScope != nil},
			{ptrTo("files.read files.write"), false},
		} {
			tokenResponse := mcpEMATestTokenResponse()
			if tokenTest.scope != nil {
				tokenResponse["scope"] = *tokenTest.scope
			}
			var decodedToken mcpEMAMCPAccessTokenResponse
			decode(t, tokenResponse, &decodedToken)
			if _, err := decodedToken.Validate("https://mcp.example", grantedScopes); (err == nil) != tokenTest.valid {
				t.Fatalf("ID-JAG %v bearer %v: err = %v, want valid %t", test.signedScope, tokenTest.scope, err, tokenTest.valid)
			}
		}
	}

	rotated := mcpEMATestJAGResponse(t, mcpEMATestJAGHeader(), original)
	rotated["refresh_token"] = "unsupported-jag-refresh-token"
	var rotatedResponse mcpEMAIDJAGResponse
	decode(t, rotated, &rotatedResponse)
	if _, err := rotatedResponse.Validate(binding()); err == nil {
		t.Fatal("ID-JAG refresh token must be rejected")
	}

	for _, header := range []map[string]any{
		{"alg": "ES256", "typ": "JWT"},
		{"alg": "ES256"},
		{"alg": "none", "typ": "oauth-id-jag+jwt"},
	} {
		changed := mcpEMATestJAGResponse(t, header, original)
		var response mcpEMAIDJAGResponse
		decode(t, changed, &response)
		if _, err := response.Validate(binding()); err == nil {
			t.Fatalf("accepted invalid ID-JAG header %#v", header)
		}
	}

	for _, field := range []string{"iss", "aud", "client_id", "sub", "jti", "exp", "iat", "scope", "resource"} {
		changed := map[string]any{}
		for key, value := range original {
			changed[key] = value
		}
		switch field {
		case "iss":
			changed["iss"] = "https://attacker.example"
		case "aud":
			changed["aud"] = "https://attacker.example"
		case "client_id":
			changed["client_id"] = "other-client"
		case "sub":
			changed["sub"] = ""
		case "jti":
			changed["jti"] = ""
		case "exp":
			changed["exp"] = 0
		case "iat":
			changed["iat"] = uint64(math.MaxUint64)
		case "scope":
			changed["scope"] = "files.admin"
		case "resource":
			changed["resource"] = []any{"https://mcp.example", "https://other.example"}
		}
		var response mcpEMAIDJAGResponse
		decode(t, mcpEMATestJAGResponse(t, mcpEMATestJAGHeader(), changed), &response)
		if _, err := response.Validate(binding()); err == nil {
			t.Fatalf("accepted changed %s", field)
		}
	}

	for _, field := range []string{"scope", "scope-dup", "resource", "expires_in", "refresh_token", "token_type", "access_token"} {
		changed := mcpEMATestTokenResponse()
		switch field {
		case "scope":
			changed["scope"] = "files.read files.write"
		case "scope-dup":
			changed["scope"] = "files.read files.read"
		case "resource":
			changed["resource"] = "https://other.example"
		case "expires_in":
			changed["expires_in"] = 0
		case "refresh_token":
			changed["refresh_token"] = "refresh"
		case "token_type":
			changed["token_type"] = "N_A"
		case "access_token":
			changed["access_token"] = ""
		}
		var response mcpEMAMCPAccessTokenResponse
		decode(t, changed, &response)
		if _, err := response.Validate("https://mcp.example", granted); err == nil {
			t.Fatalf("accepted changed %s", field)
		}
	}

	explicitBinding := mcpEMATestTokenResponse()
	explicitBinding["resource"] = "https://mcp.example"
	explicitBinding["scope"] = "files.read"
	var explicitResponse mcpEMAMCPAccessTokenResponse
	decode(t, explicitBinding, &explicitResponse)
	token, err := explicitResponse.Validate("https://mcp.example", granted)
	if err != nil || token == nil || token.AccessToken != "resource-token" {
		t.Fatalf("explicit binding = %#v, %v", token, err)
	}
}

func TestMCPEMAIdentityAndCredentialDestinationsAreBound(t *testing.T) {
	for _, endpoint := range []string{
		"http://idp.example/token",
		"https://user:pass@idp.example/token",
		"https://idp.example/token#fragment",
	} {
		if err := validateMCPEMAOAuthEndpoint(endpoint, "IdP"); err == nil {
			t.Fatalf("accepted %s", endpoint)
		}
	}
	original := mcpEMATestClaims("https://idp.example", "idp-client", "https://mcp.example")
	if err := ValidateMCPEMAOIDCIdentityAssertion(mcpEMATestJWT(t, mcpEMATestJAGHeader(), original), "https://idp.example", "idp-client"); err != nil {
		t.Fatalf("valid identity assertion error = %v", err)
	}
	for _, field := range []string{"iss", "aud", "azp", "exp", "sub"} {
		changed := map[string]any{}
		for key, value := range original {
			changed[key] = value
		}
		switch field {
		case "iss":
			changed["iss"] = "https://other.example"
		case "aud":
			changed["aud"] = []any{"idp-client", "other"}
		case "azp":
			changed["azp"] = "other"
		case "exp":
			changed["exp"] = 0
		case "sub":
			changed["sub"] = ""
		}
		if err := ValidateMCPEMAOIDCIdentityAssertion(mcpEMATestJWT(t, mcpEMATestJAGHeader(), changed), "https://idp.example", "idp-client"); err == nil {
			t.Fatalf("accepted changed %s", field)
		}
	}
}

func TestMCPEMAProviderErrorsCannotReflectCredentials(t *testing.T) {
	const sentinel = "secret-assertion-sentinel"
	for _, test := range []struct {
		code     string
		expected string
	}{
		{sentinel, "OAuth token request rejected"},
		{"invalid_grant", "invalid_grant"},
		{"insufficient_user_authentication", "insufficient_user_authentication"},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error":             test.code,
				"error_description": sentinel,
			})
		}))
		_, err := postMCPEMAForm[map[string]any](
			context.Background(),
			server.Client(),
			server.URL+"/token",
			[][2]string{{"subject_token", sentinel}},
			"test-client",
			EMAInvalidGrantSourceEnterpriseIdentity,
			"test token exchange",
		)
		server.Close()
		if err == nil {
			t.Fatalf("code %q: provider error should fail", test.code)
		}
		var failure *EMAAuthFailure
		switch test.code {
		case "invalid_grant":
			if !errors.As(err, &failure) || failure.Code != EMAAuthFailureInvalidGrant || failure.GrantSource != EMAInvalidGrantSourceEnterpriseIdentity {
				t.Fatalf("invalid_grant failure = %#v", failure)
			}
		case "insufficient_user_authentication":
			if !errors.As(err, &failure) || failure.Code != EMAAuthFailureInsufficientUserAuthn {
				t.Fatalf("insufficient failure = %#v", failure)
			}
		default:
			if errors.As(err, &failure) {
				t.Fatalf("unexpected typed failure = %#v", failure)
			}
		}
		if message := err.Error(); strings.Contains(message, sentinel) || !strings.HasSuffix(message, test.expected) {
			t.Fatalf("error = %q, want no sentinel and suffix %q", message, test.expected)
		}
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		malformed := mcpEMATestTokenResponse()
		malformed["expires_in"] = sentinel
		writeJSON(t, w, malformed)
	}))
	defer server.Close()
	_, err := postMCPEMAForm[mcpEMAMCPAccessTokenResponse](
		context.Background(),
		server.Client(),
		server.URL+"/token",
		nil,
		"test-client",
		EMAInvalidGrantSourceEnterpriseIdentity,
		"test token exchange",
	)
	if err == nil || strings.Contains(err.Error(), sentinel) {
		t.Fatalf("malformed response error = %v", err)
	}
}

func ptrTo[T any](value T) *T {
	return &value
}
