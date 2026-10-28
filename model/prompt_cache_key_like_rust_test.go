package model

import (
	"net/http"
	"testing"
)

// TestPromptCacheKeyPolicyLikeRust pins the three policies from Rust #55587:
// the default session-derived key, a caller key, and omission.
func TestPromptCacheKeyPolicyLikeRust(t *testing.T) {
	const sessionDerived = "session-derived-key"

	if key, ok := SessionDerivedPromptCacheKey().Resolve(sessionDerived); !ok || key != sessionDerived {
		t.Fatalf("session-derived key = %q, %v", key, ok)
	}
	if key, ok := CustomPromptCacheKey("caller-key").Resolve(sessionDerived); !ok || key != "caller-key" {
		t.Fatalf("custom key = %q, %v", key, ok)
	}
	if key, ok := OmitPromptCacheKey().Resolve(sessionDerived); ok || key != "" {
		t.Fatalf("omitted key = %q, %v", key, ok)
	}

	var zero PromptCacheKey
	if !zero.IsZero() || zero.Mode() != PromptCacheKeySessionDerived {
		t.Fatalf("zero policy = %#v, mode %v", zero, zero.Mode())
	}
	if key, ok := CustomPromptCacheKey("caller-key").CustomKey(); !ok || key != "caller-key" {
		t.Fatalf("custom accessor = %q, %v", key, ok)
	}
	if _, ok := SessionDerivedPromptCacheKey().CustomKey(); ok {
		t.Fatal("session-derived policy must not expose a custom key")
	}
	if key, ok := OmitPromptCacheKey().CustomKey(); ok || key != "" {
		t.Fatalf("omit accessor = %q, %v", key, ok)
	}
}

// TestPromptCacheKeyPolicyKeepsSessionIdentityHeadersLikeRust verifies that the
// request-body policy never rewrites the session-id header.
func TestPromptCacheKeyPolicyKeepsSessionIdentityHeadersLikeRust(t *testing.T) {
	for _, policy := range []PromptCacheKey{
		SessionDerivedPromptCacheKey(),
		CustomPromptCacheKey("caller-key"),
		OmitPromptCacheKey(),
	} {
		key, _ := policy.Resolve("session-derived-key")
		request := &AgentRequest{
			ThreadID:       "thread-1",
			SessionID:      "session-1",
			PromptCacheKey: key,
		}
		headers := http.Header{}
		addSessionIdentityHeaders(headers, request)
		if got := headers.Get("session-id"); got != "session-1" {
			t.Fatalf("policy %v: session-id header = %q", policy.Mode(), got)
		}
		if got := headers.Get("thread-id"); got != "thread-1" {
			t.Fatalf("policy %v: thread-id header = %q", policy.Mode(), got)
		}
	}
}
