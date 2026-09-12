package mcp

import (
	"strings"
	"testing"
)

// Mirrors Rust oauth_callback_tests::resolved_callbacks_follow_the_selected_mix_up_defense.
func TestMCPOAuthResolvedCallbacksFollowMixUpDefense(t *testing.T) {
	serverURL := "https://mcp.example.com/mcp?tenant=one"
	callbackID, err := MCPOAuthCallbackID(serverURL)
	if err != nil {
		t.Fatalf("MCPOAuthCallbackID() error = %v", err)
	}
	distinctCallback := "http://127.0.0.1/callback/" + callbackID
	registered := "http://127.0.0.1:8080/oauth/callback"
	for _, test := range []struct {
		callback *string
		mode     MCPOAuthCallbackMode
		expected string
	}{
		{nil, MCPOAuthCallbackSpecific, distinctCallback},
		{nil, MCPOAuthCallbackIssuerBound, "http://127.0.0.1/callback"},
		{&registered, MCPOAuthCallbackIssuerBound, registered},
	} {
		got, err := ResolveMCPOAuthCallbackURL(serverURL, test.callback, test.mode)
		if err != nil {
			t.Fatalf("ResolveMCPOAuthCallbackURL(%v, %s) error = %v", test.callback, test.mode, err)
		}
		if got != test.expected {
			t.Fatalf("ResolveMCPOAuthCallbackURL(%v, %s) = %q, want %q", test.callback, test.mode, got, test.expected)
		}
	}
}

// Mirrors Rust oauth_callback_tests::callback_redirect_requires_a_server_specific_id_or_issuer_support.
func TestMCPOAuthCallbackRedirectRequiresIDOrIssuerSupport(t *testing.T) {
	for _, test := range []struct {
		redirectURI string
		mode        MCPOAuthCallbackMode
		valid       bool
	}{
		{"http://127.0.0.1/callback/expected-id", MCPOAuthCallbackSpecific, true},
		{"http://127.0.0.1/callback", MCPOAuthCallbackIssuerBound, true},
		{"http://127.0.0.1/callback/wrong-id", MCPOAuthCallbackSpecific, false},
	} {
		if got := ValidateMCPOAuthCallbackRedirect(test.redirectURI, "expected-id", test.mode) == nil; got != test.valid {
			t.Fatalf("ValidateMCPOAuthCallbackRedirect(%q, %s) = %t, want %t", test.redirectURI, test.mode, got, test.valid)
		}
	}
}

func TestMCPOAuthCallbackModeForDiscovery(t *testing.T) {
	if mode, err := MCPOAuthCallbackModeForDiscovery(false, "https://issuer.example"); err != nil || mode != MCPOAuthCallbackSpecific {
		t.Fatalf("unsupported = %q, %v; want callback-specific", mode, err)
	}
	if mode, err := MCPOAuthCallbackModeForDiscovery(true, "https://issuer.example"); err != nil || mode != MCPOAuthCallbackIssuerBound {
		t.Fatalf("supported = %q, %v; want issuer-bound", mode, err)
	}
	if _, err := MCPOAuthCallbackModeForDiscovery(true, "  "); err == nil || !strings.Contains(err.Error(), "without a metadata issuer") {
		t.Fatalf("supported without issuer error = %v", err)
	}
}

func TestAppendMCPOAuthCallbackIDIsIdempotent(t *testing.T) {
	appended, err := AppendMCPOAuthCallbackID("http://127.0.0.1/callback", "abc123")
	if err != nil {
		t.Fatalf("AppendMCPOAuthCallbackID() error = %v", err)
	}
	if appended != "http://127.0.0.1/callback/abc123" {
		t.Fatalf("appended = %q", appended)
	}
	again, err := AppendMCPOAuthCallbackID(appended, "abc123")
	if err != nil || again != appended {
		t.Fatalf("second append = %q, %v; want unchanged", again, err)
	}
}

func TestInsertMCPOAuthListenerPort(t *testing.T) {
	for _, test := range []struct {
		redirect string
		expected string
	}{
		{"http://127.0.0.1/callback", "http://127.0.0.1:41234/callback"},
		{"http://localhost/callback", "http://localhost:41234/callback"},
		{"http://127.0.0.1:8080/callback", "http://127.0.0.1:8080/callback"},
		{"https://callbacks.example.com/callback", "https://callbacks.example.com/callback"},
	} {
		got, err := insertMCPOAuthListenerPort(test.redirect, 41234)
		if err != nil {
			t.Fatalf("insertMCPOAuthListenerPort(%q) error = %v", test.redirect, err)
		}
		if got != test.expected {
			t.Fatalf("insertMCPOAuthListenerPort(%q) = %q, want %q", test.redirect, got, test.expected)
		}
	}
}

func TestMCPOAuthCallbackIssuerValidation(t *testing.T) {
	session := &OAuthLoginSession{Issuer: "https://issuer.example", CallbackMode: MCPOAuthCallbackIssuerBound}
	if err := session.validateCallbackIssuer("/callback?code=c&state=s&iss=https%3A%2F%2Fissuer.example"); err != nil {
		t.Fatalf("matching issuer error = %v", err)
	}
	for _, rawPath := range []string{
		"/callback?code=c&state=s",
		"/callback?code=c&state=s&iss=https%3A%2F%2Fattacker.example",
	} {
		if err := session.validateCallbackIssuer(rawPath); err == nil {
			t.Fatalf("validateCallbackIssuer(%q) should reject", rawPath)
		}
	}
	callbackSpecific := &OAuthLoginSession{Issuer: "https://issuer.example", CallbackMode: MCPOAuthCallbackSpecific}
	if err := callbackSpecific.validateCallbackIssuer("/callback?code=c&state=s"); err != nil {
		t.Fatalf("callback-specific session should not require iss: %v", err)
	}
}
