package mcp

import (
	"encoding/json"
	"testing"
)

// Mirrors Rust #46952: user-verification elicitation metadata keeps object
// fields for client presentation while the transport progress token is dropped.
func TestUserVerificationElicitationMetaLikeRust(t *testing.T) {
	params := json.RawMessage(`{"mode":"openai/userVerification","title":"Verify action","description":"Verify the requested operation","challenge":"AQID","_meta":{"progressToken":7,"example/display":{"label":"Operation"}}}`)
	request := parseMCPElicitationRequest("verifier", UserVerificationMode, json.RawMessage(`1`), params)
	if !IsUserVerificationElicitation(request) {
		t.Fatal("request was not recognized as user verification")
	}
	meta, ok := request.Meta.(map[string]any)
	if !ok {
		t.Fatalf("meta = %#v, want an object", request.Meta)
	}
	if _, exists := meta["progressToken"]; exists {
		t.Fatalf("forwarded meta kept progressToken: %#v", meta)
	}
	display, ok := meta["example/display"].(map[string]any)
	if !ok || display["label"] != "Operation" {
		t.Fatalf("forwarded meta = %#v, want example/display.label", meta)
	}

	invalid := json.RawMessage(`{"mode":"openai/userVerification","title":"Verify action","description":"Verify the requested operation","challenge":"AQID","_meta":true}`)
	if err := validateMCPUserVerificationRequest(invalid); err == nil {
		t.Fatal("non-object user-verification metadata was accepted")
	}
}
