package appserver

import (
	"testing"

	"codex_go/turn"
)

// Mirrors Rust #62072: a child spawned from a yielded Code Mode cell inherits the
// attribution of the turn that requested the spawn rather than whatever the
// parent thread is doing now, and never inherits the parent's review or routing
// intent.
func TestSpawnInheritsRequestingTurnClientMetadataLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	threadID := "parent-thread"
	router.threads.RegisterTurn(threadID, "requesting-turn", func() {}, 0, &turn.TurnStartParams{ThreadID: threadID})
	router.recordTurnClientMetadata("requesting-turn", map[string]string{
		"cost_center":           "cc-requesting",
		"parent_response_id":    "parent-response",
		"misalignment_override": `{"review_target":"parent-only"}`,
	})
	// The thread's live turn carries different attribution.
	liveTurnID := router.threads.ActiveTurnID(threadID)
	if liveTurnID == "" {
		t.Fatal("the parent thread has no live turn")
	}
	router.threads.UpdateTurn(threadID, liveTurnID, func(active *activeRuntimeTurn) {
		active.TurnClientMetadata = map[string]string{"cost_center": "cc-live"}
	})

	controller := &runtimeAgentController{router: router, parentID: threadID, parentTurnID: "requesting-turn", cwd: t.TempDir()}
	inherited := controller.inheritedClientMetadata()
	if len(inherited) != 1 || inherited["cost_center"] != "cc-requesting" {
		t.Fatalf("inherited metadata = %#v, want the requesting turn's attribution only", inherited)
	}

	// A controller without a captured turn follows the parent thread's live turn.
	untracked := &runtimeAgentController{router: router, parentID: threadID, cwd: t.TempDir()}
	if got := untracked.inheritedClientMetadata(); len(got) != 1 || got["cost_center"] != "cc-live" {
		t.Fatalf("fallback inherited metadata = %#v, want the live turn's attribution", got)
	}
	if got := (&runtimeAgentController{}).inheritedClientMetadata(); got != nil {
		t.Fatalf("controller without a router inherited %#v", got)
	}
}

// The inherited snapshot replaces the child's initial-turn document, so the
// child's own identifiers survive and the parent's intent is already gone.
func TestChildInitialTurnUsesInheritedClientMetadataLikeRust(t *testing.T) {
	params := &turn.TurnStartParams{ThreadID: "child-thread", ResponsesAPIMetadata: map[string]string{"stale": "1"}}
	applyInheritedClientMetadata(params, map[string]string{"cost_center": "cc-1"})
	if len(params.ResponsesAPIMetadata) != 1 || params.ResponsesAPIMetadata["cost_center"] != "cc-1" {
		t.Fatalf("child turn metadata = %#v, want the inherited snapshot only", params.ResponsesAPIMetadata)
	}
	unchanged := &turn.TurnStartParams{ThreadID: "child-thread", ResponsesAPIMetadata: map[string]string{"client": "own"}}
	applyInheritedClientMetadata(unchanged, nil)
	if len(unchanged.ResponsesAPIMetadata) != 1 || unchanged.ResponsesAPIMetadata["client"] != "own" {
		t.Fatalf("child turn metadata = %#v, want the request's own document", unchanged.ResponsesAPIMetadata)
	}
}
