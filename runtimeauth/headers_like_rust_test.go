package runtimeauth

import (
	"net/http"
	"testing"
)

// TestRuntimeAuthHeadersReplaceActorCredentialLikeRust pins Rust
// replace_auth_headers (runtime-agent-instance/src/auth.rs:203-213): switching
// modes must delete the actor credential headers, not extend them.
func TestRuntimeAuthHeadersReplaceActorCredentialLikeRust(t *testing.T) {
	existing := http.Header{
		"Authorization":      []string{"Bearer actor-token"},
		AssertionHeader:      []string{"1.old.sig"},
		"ChatGPT-Account-ID": []string{"acct-old"},
		"x-openai-fedramp":   []string{"true"},
		"X-Keep":             []string{"keep"},
	}
	assertionOnly := http.Header{"Authorization": []string{"AgentAssertion 1.new.sig"}}
	ReplaceAuthHeaders(existing, assertionOnly)
	if got := existing.Get("Authorization"); got != "AgentAssertion 1.new.sig" {
		t.Fatalf("Authorization = %q, want the assertion-only credential", got)
	}
	for _, name := range []string{AssertionHeader, "ChatGPT-Account-ID", "x-openai-fedramp"} {
		if got := existing.Get(name); got != "" {
			t.Fatalf("%s = %q, want the previous mode's header removed", name, got)
		}
	}
	if got := existing.Get("X-Keep"); got != "keep" {
		t.Fatalf("X-Keep = %q, an unrelated header must survive", got)
	}

	// Switching back to dual headers must not keep the assertion-only form.
	dual := http.Header{
		"Authorization":      []string{"Bearer actor-token"},
		AssertionHeader:      []string{"1.newer.sig"},
		"ChatGPT-Account-ID": []string{"acct-new"},
	}
	ReplaceAuthHeaders(existing, dual)
	if got := existing.Get("Authorization"); got != "Bearer actor-token" {
		t.Fatalf("Authorization = %q, want the dual-header bearer", got)
	}
	if got := existing.Get(AssertionHeader); got != "1.newer.sig" {
		t.Fatalf("assertion = %q", got)
	}
	if got := existing.Get("ChatGPT-Account-ID"); got != "acct-new" {
		t.Fatalf("account id = %q", got)
	}
}
