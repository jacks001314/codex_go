package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

// Mirrors Rust #43524: userVerification is advertised only to the host-owned
// apps server, never merely because a configured server reuses its name.
func TestUserVerificationCapabilityRestrictedToHostOwnedAppsLikeRust(t *testing.T) {
	elicitation := func(userVerification bool) map[string]any {
		capabilities := mcpClientCapabilitiesWithFeatures(true, false, userVerification)
		extensions, _ := capabilities["extensions"].(map[string]any)
		elicitation, _ := extensions["openai/elicitation"].(map[string]any)
		return elicitation
	}
	if _, ok := elicitation(false)["userVerification"]; ok {
		t.Fatal("configured MCP server was advertised userVerification")
	}
	if _, ok := elicitation(true)["userVerification"]; !ok {
		t.Fatal("host-owned apps server was not advertised userVerification")
	}
}

func TestConfiguredMCPServerCannotRequestUserVerificationLikeRust(t *testing.T) {
	service := NewMCPService(nil)
	service.SetUserVerificationEnabled(true)
	called := 0
	service.SetElicitationHandler(MCPElicitationHandlerFunc(func(context.Context, *MCPElicitationRequest) (*MCPElicitationResponse, error) {
		called++
		return &MCPElicitationResponse{
			Action: MCPElicitationActionAccept,
			Content: map[string]any{
				"credentialId": "credential-1",
				"signature":    "c2ln",
			},
		}, nil
	}))
	params := json.RawMessage(`{"mode":"openai/userVerification","title":"Confirm","description":"Sign","challenge":"Y2g"}`)

	// Reusing the hosted app name is not enough: ownership is what grants the
	// extension.
	service.SetServer(MCPServerStatus{Name: "codex_apps"})
	result := mcpElicitationResult(context.Background(), "codex_apps", service.elicitationHandlerForServer("codex_apps"), "openai/userVerification", json.RawMessage("1"), params)
	response, ok := result.(*MCPElicitationResponse)
	if !ok || response.Action != MCPElicitationActionCancel {
		t.Fatalf("configured server verification result = %#v, want cancel", result)
	}
	if called != 0 {
		t.Fatalf("configured server reached the client verification handler %d times", called)
	}

	service.SetServer(MCPServerStatus{Name: "codex_apps", hostOwnedApps: true})
	result = mcpElicitationResult(context.Background(), "codex_apps", service.elicitationHandlerForServer("codex_apps"), "openai/userVerification", json.RawMessage("2"), params)
	response, ok = result.(*MCPElicitationResponse)
	if !ok || response.Action != MCPElicitationActionAccept {
		t.Fatalf("host-owned apps verification result = %#v, want accept", result)
	}
	if called != 1 {
		t.Fatalf("host-owned apps handler calls = %d, want 1", called)
	}
}

// Mirrors Rust #44613: app-server activates the verification extension
// separately from the legacy OpenAI form capability.
func TestUserVerificationExtensionIsIndependentOfOpenAIFormLikeRust(t *testing.T) {
	verificationOnly := mcpClientCapabilitiesForFeatures(mcpClientFeatures{UserVerification: true})
	extensions, _ := verificationOnly["extensions"].(map[string]any)
	elicitation, _ := extensions["openai/elicitation"].(map[string]any)
	if _, ok := elicitation["userVerification"]; !ok {
		t.Fatalf("userVerification extension = %#v", extensions)
	}
	if _, ok := elicitation["form"]; ok {
		t.Fatalf("verification-only extension advertised form: %#v", elicitation)
	}
	if _, ok := extensions["openai/form"]; ok {
		t.Fatalf("verification-only capabilities advertised openai/form: %#v", extensions)
	}

	formOnly := mcpClientCapabilitiesForFeatures(mcpClientFeatures{OpenAIForm: true})
	formExtensions, _ := formOnly["extensions"].(map[string]any)
	formElicitation, _ := formExtensions["openai/elicitation"].(map[string]any)
	if _, ok := formElicitation["userVerification"]; ok {
		t.Fatalf("form-only capabilities advertised userVerification: %#v", formElicitation)
	}
}
