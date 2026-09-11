package appserver

import (
	"testing"

	"codex_go/session"
)

// Mirrors Rust #44862: an ephemeral root fork reuses its parent's session id as
// the Responses cache key, while keeping its own thread/session identity and
// leaving non-ephemeral or sub-agent threads on their own routing.
func TestResponsesPromptCacheKeyInheritsEphemeralForkParentSession(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	if router.threads == nil {
		t.Fatal("router thread manager is nil")
	}
	if !router.threads.SaveEphemeralRecord(&session.Record{
		ID: "parent-thread", SessionID: "parent-session",
		Metadata: session.Metadata{Extra: map[string]any{"ephemeral": true}},
	}) {
		t.Fatal("failed to save parent ephemeral record")
	}
	if !router.threads.SaveEphemeralRecord(&session.Record{
		ID: "child-thread", SessionID: "child-session", ForkedFromID: "parent-thread",
		Metadata: session.Metadata{Extra: map[string]any{"ephemeral": true}},
	}) {
		t.Fatal("failed to save child ephemeral record")
	}
	childLineage := responsesMetadataLineage{SessionID: "child-session", ForkedFromThreadID: "parent-thread"}
	if got := router.responsesPromptCacheKey("child-thread", childLineage, true); got != "parent-session" {
		t.Fatalf("ephemeral fork cache key = %q, want the parent session id", got)
	}
	// A non-ephemeral fork keeps its own routing.
	if got := router.responsesPromptCacheKey("child-thread", childLineage, false); got != "child-thread" {
		t.Fatalf("persistent fork cache key = %q, want the thread id", got)
	}
	// A non-forked thread keeps its own routing.
	if got := router.responsesPromptCacheKey("parent-thread", responsesMetadataLineage{SessionID: "parent-session"}, true); got != "parent-thread" {
		t.Fatalf("non-forked cache key = %q, want the thread id", got)
	}
	// Sub-agent forks keep their own routing.
	if !router.threads.SaveEphemeralRecord(&session.Record{
		ID: "subagent-thread", SessionID: "subagent-session", ForkedFromID: "parent-thread",
		Metadata: session.Metadata{Source: "subagent:review", Extra: map[string]any{"ephemeral": true}},
	}) {
		t.Fatal("failed to save sub-agent ephemeral record")
	}
	subagentLineage := responsesMetadataLineage{SessionID: "subagent-session", ForkedFromThreadID: "parent-thread"}
	if got := router.responsesPromptCacheKey("subagent-thread", subagentLineage, true); got != "subagent-thread" {
		t.Fatalf("sub-agent fork cache key = %q, want the thread id", got)
	}
}
