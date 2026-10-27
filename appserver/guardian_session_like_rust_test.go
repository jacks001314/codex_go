package appserver

import (
	"context"
	"testing"

	"codex_go/model"
	"codex_go/state"
)

// Mirror of Rust's ext/guardian-reviewer/src/session_tests.rs: the guardian
// review prompt cache key is scoped to the parent thread, stays within the
// Responses API limit, and is absent for inapplicable inputs.
func TestGuardianPromptCacheKeyScopedToParentThreadLikeRust(t *testing.T) {
	const parentThreadID = "0190f77c-6b1e-7a3f-9f0c-4b8b8b8b8b8b"
	key := guardianPromptCacheKeyOverride(guardianReviewerName, parentThreadID)
	if key != "guardian:"+parentThreadID {
		t.Fatalf("guardian prompt cache key = %q, want %q", key, "guardian:"+parentThreadID)
	}
	if len(key) > 64 {
		t.Fatalf("guardian prompt cache key length = %d, want <= 64", len(key))
	}
	if got := guardianPromptCacheKeyOverride(guardianReviewerName, parentThreadID); got != key {
		t.Fatalf("same parent key = %q, want %q", got, key)
	}
	if got := guardianPromptCacheKeyOverride(guardianReviewerName, "0190f77c-0000-7a3f-9f0c-000000000000"); got == key {
		t.Fatalf("different parent reused guardian prompt cache key %q", got)
	}
	if got := guardianPromptCacheKeyOverride("cli", parentThreadID); got != "" {
		t.Fatalf("non-guardian source key = %q, want no override", got)
	}
	if got := guardianPromptCacheKeyOverride(guardianReviewerName, ""); got != "" {
		t.Fatalf("missing parent key = %q, want no override", got)
	}
	if got := guardianPromptCacheKeyOverride(guardianReviewerName, "   "); got != "" {
		t.Fatalf("blank parent key = %q, want no override", got)
	}
}

// The reviewer request carries the parent-scoped cache key, matching Rust's
// session_id_override for a guardian review session.
func TestGuardianReviewRequestUsesParentScopedPromptCacheKeyLikeRust(t *testing.T) {
	var captured *model.AgentRequest
	reviewer := &modelGuardianReviewer{agent: guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
		captured = request
		return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"reviewed"}`}, nil
	})}
	if _, _, err := reviewer.Review(context.Background(), "thread-cache-key", "turn-cache-key", "call-cache-key", state.Action{Type: "mcp_tool_call", Server: "apps", ToolName: "calendar"}); err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if captured == nil {
		t.Fatal("guardian review did not issue a request")
	}
	if captured.PromptCacheKey != "guardian:thread-cache-key" {
		t.Fatalf("PromptCacheKey = %q, want %q", captured.PromptCacheKey, "guardian:thread-cache-key")
	}
}
