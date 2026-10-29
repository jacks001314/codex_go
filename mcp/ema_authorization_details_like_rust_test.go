package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// Rust #40722 relies on the rmcp EMA profile, which rejects a non-empty
// authorization_details list in the ID-JAG claims, the IdP response or the
// resource token response before returning a bearer; an absent or empty list is
// ignored.
func TestEMARejectsAuthorizationDetailsLikeRust(t *testing.T) {
	binding := MCPEMAIDJAGBinding{
		Issuer:   "https://idp.example",
		Audience: "https://as.example",
		ClientID: "mcp-client",
		Resource: "https://mcp.example",
	}
	details := []any{map[string]any{"type": "payment_initiation"}}
	for _, tc := range []struct {
		name       string
		response   map[string]any
		claims     map[string]any
		wantReject bool
	}{
		{name: "absent"},
		{name: "empty list", response: map[string]any{"authorization_details": []any{}}},
		{name: "IdP response", response: map[string]any{"authorization_details": details}, wantReject: true},
		{name: "signed claims", claims: map[string]any{"authorization_details": details}, wantReject: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claims := mcpEMATestClaims(binding.Issuer, binding.Audience, binding.Resource)
			for key, value := range tc.claims {
				claims[key] = value
			}
			response := mcpEMATestJAGResponse(t, mcpEMATestJAGHeader(), claims)
			for key, value := range tc.response {
				response[key] = value
			}
			err := validateEMAJAGResponse(t, response, binding)
			if tc.wantReject {
				if err == nil || !strings.Contains(err.Error(), "authorization_details") {
					t.Fatalf("Validate() error = %v, want an authorization_details rejection", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate() error = %v, want success", err)
			}
		})
	}
}

func TestEMAResourceTokenRejectsAuthorizationDetailsLikeRust(t *testing.T) {
	granted := map[string]struct{}{"files.read": {}}
	for _, tc := range []struct {
		name       string
		details    any
		wantReject bool
	}{
		{name: "absent"},
		{name: "empty list", details: []any{}},
		{name: "non-empty list", details: []any{map[string]any{"type": "payment_initiation"}}, wantReject: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values := mcpEMATestTokenResponse()
			if tc.details != nil {
				values["authorization_details"] = tc.details
			}
			raw, err := json.Marshal(values)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var decoded mcpEMAMCPAccessTokenResponse
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			_, err = decoded.Validate("https://mcp.example", granted)
			if tc.wantReject {
				if err == nil || !strings.Contains(err.Error(), "authorization_details") {
					t.Fatalf("Validate() error = %v, want an authorization_details rejection", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate() error = %v, want success", err)
			}
		})
	}
}

func validateEMAJAGResponse(t *testing.T, values map[string]any, binding MCPEMAIDJAGBinding) error {
	t.Helper()
	raw, err := json.Marshal(values)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	var decoded mcpEMAIDJAGResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	_, err = decoded.Validate(binding)
	return err
}
