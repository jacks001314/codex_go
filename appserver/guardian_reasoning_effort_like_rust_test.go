package appserver

import (
	"sync"
	"testing"

	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

// TestSyncGuardianKeepsSelectedReasoningEffortLikeRust covers Rust #46292: a
// synchronous Guardian reviewer uses its selected request-level effort even when
// managed requirements enable reasoning_effort_override and its inherited
// history carries trusted effort updates. Both the current internal guardian
// source and the legacy subagent "guardian" source are reviewers, while the
// parent thread keeps the normal override behavior.
func TestSyncGuardianKeepsSelectedReasoningEffortLikeRust(t *testing.T) {
	router := &RuntimeRouter{threads: &ThreadManager{
		ephemeralMu: sync.RWMutex{},
		ephemeral: map[string]*session.Record{
			"internal-guardian": {ID: "internal-guardian", Metadata: session.Metadata{
				Source:       "internal_guardian",
				ThreadSource: string(ThreadSourceGuardianReview),
			}},
			"legacy-guardian": {ID: "legacy-guardian", Metadata: session.Metadata{
				Source:       session.GuardianReviewSource,
				ThreadSource: string(ThreadSourceGuardianReview),
			}},
			"parent": {ID: "parent", Metadata: session.Metadata{Source: "cli"}},
		},
	}}
	supported := &model.ModelInfo{Slug: "gpt-5", SupportsReasoningEffortUpdates: true, DefaultReasoningLevel: "medium"}
	cfg := reasoningEffortTestConfig("high", true)

	for _, threadID := range []string{"internal-guardian", "legacy-guardian"} {
		if !router.reasoningEffortOverrideExempt(threadID) {
			t.Fatalf("thread %q must be exempt from reasoning-effort overrides", threadID)
		}
		if router.reasoningEffortOverrideEnabled(threadID, cfg, supported, "openai") {
			t.Fatalf("thread %q must not enable reasoning-effort overrides", threadID)
		}
		if _, ok := router.effortForConfigurationUpdate(threadID, cfg, &turn.TurnStartParams{}, supported, "openai"); ok {
			t.Fatalf("reviewer %q must not record a trusted update", threadID)
		}
		if items := router.reasoningEffortOverrideInputItems(threadID, "gpt-5", "low", false, nil); len(items) != 0 {
			t.Fatalf("reviewer %q injected updates: %#v", threadID, items)
		}
		// Reviewers follow their selected effort instead of a pin or an
		// inherited trusted update.
		for _, effort := range []string{"low", "high"} {
			if got := router.reasoningEffortForRequest(threadID, "gpt-5", effort, false, "medium", true, requestEffortSampling); got != effort {
				t.Fatalf("reviewer %q request effort = %q, want %q", threadID, got, effort)
			}
			if pin := router.reasoningEffortPinState(threadID); pin.Kind != reasoningEffortPinUnset {
				t.Fatalf("reviewer %q kept a pin after using its selected effort: %#v", threadID, pin)
			}
		}
	}

	// The parent keeps the override: its first request pins the trusted effort
	// and later turns reuse it, which is what the reviewer must not inherit.
	if !router.reasoningEffortOverrideEnabled("parent", cfg, supported, "openai") {
		t.Fatal("parent thread must enable reasoning-effort overrides")
	}
	if items := router.reasoningEffortOverrideInputItems("parent", "gpt-5", "high", true, nil); len(items) != 1 {
		t.Fatalf("parent updates = %#v, want one trusted update", items)
	}
	if got := router.reasoningEffortForRequest("parent", "gpt-5", "low", true, "high", true, requestEffortSampling); got != "high" {
		t.Fatalf("parent request effort = %q, want high", got)
	}
	if got := router.reasoningEffortForRequest("parent", "gpt-5", "low", true, "low", true, requestEffortSampling); got != "high" {
		t.Fatalf("parent pinned effort = %q, want high", got)
	}
	if items := router.reasoningEffortOverrideInputItems("parent", "gpt-5", "high", true, nil); len(items) != 0 {
		t.Fatalf("parent re-recorded its pinned effort: %#v", items)
	}

	// Reviewer requests drop inherited updates while the saved history keeps
	// them: filtering is a request-copy operation.
	inherited := []any{trustedReasoningUpdate("medium"), trustedReasoningUpdate("high")}
	if kept := rejectConfigurationUpdateInputItems(inherited); len(kept) != 0 {
		t.Fatalf("reviewer request kept inherited updates: %#v", kept)
	}
	if len(inherited) != 2 {
		t.Fatalf("saved history changed: %#v", inherited)
	}
}
