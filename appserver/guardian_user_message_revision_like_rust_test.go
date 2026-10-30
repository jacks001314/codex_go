package appserver

import (
	"encoding/json"
	"testing"

	"codex_go/session"
	"codex_go/state"
)

func revisionTestSessionItem(t *testing.T, role string, text string, kinds []string) session.Item {
	t.Helper()
	payload := map[string]any{
		"type":    "message",
		"role":    role,
		"content": []any{map[string]any{"type": "input_text", "text": text}},
	}
	if kinds != nil {
		payload["internal_chat_message_metadata_passthrough"] = map[string]any{"content_item_kinds": kinds}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal revision item: %v", err)
	}
	return session.Item{Type: "message", Role: role, Raw: encoded}
}

// Mirrors Rust #41660: the host-owned revision advances for genuine user input
// only, and history resets/rollbacks advance it even when nothing is trimmed.
func TestGuardianUserMessageRevisionLikeRust(t *testing.T) {
	const threadID = "thread-revision"
	router := NewRuntimeRouter(RuntimeServices{})
	if got := router.guardianUserMessageRevision(threadID); got != 0 {
		t.Fatalf("initial revision = %d, want 0", got)
	}

	router.recordGuardianReviewHistoryAppend(threadID, []session.Item{
		revisionTestSessionItem(t, "user", "<goal>Inspect.</goal>", []string{"context.goal"}),
	})
	if got := router.guardianUserMessageRevision(threadID); got != 0 {
		t.Fatalf("internal context advanced revision to %d, want 0", got)
	}

	router.recordGuardianReviewHistoryAppend(threadID, []session.Item{
		revisionTestSessionItem(t, "user", "Inspect the deployment.", []string{"user.text"}),
		revisionTestSessionItem(t, "assistant", "Inspecting.", []string{"assistant.text"}),
	})
	if got := router.guardianUserMessageRevision(threadID); got != 1 {
		t.Fatalf("revision after user turn = %d, want 1", got)
	}

	// Legacy evidence without a persisted response body stays conservative.
	legacy := session.Item{Type: "message", Role: "user", Text: "Ship it."}
	router.recordGuardianReviewHistoryAppend(threadID, []session.Item{legacy})
	if got := router.guardianUserMessageRevision(threadID); got != 2 {
		t.Fatalf("revision after legacy user turn = %d, want 2", got)
	}

	// Compaction captures the review history without advancing the revision.
	router.captureGuardianReviewHistory(threadID, []session.Item{
		revisionTestSessionItem(t, "user", "summary", nil),
	})
	if got := router.guardianUserMessageRevision(threadID); got != 2 {
		t.Fatalf("compaction advanced revision to %d, want 2", got)
	}

	// Rollback is a history reset.
	router.truncateGuardianReviewHistoryBefore(threadID, "turn-1")
	if got := router.guardianUserMessageRevision(threadID); got != 3 {
		t.Fatalf("revision after rollback = %d, want 3", got)
	}

	router.forgetGuardianReviewHistory(threadID)
	if got := router.guardianUserMessageRevision(threadID); got != 0 {
		t.Fatalf("revision after forgetting the thread = %d, want 0", got)
	}
}

// Mirrors Rust #41660: a retained serving assessment stops applying when new
// user input advances the revision, while compaction leaves it usable.
func TestServingAuthorizationTracksUserMessageRevisionLikeRust(t *testing.T) {
	current := state.ServingAuthorization{RetainedContextComplete: true, PolicyFingerprint: "model|policy", UserMessageRevision: 4}
	if !current.Matches(current) {
		t.Fatal("identical authorizations must match")
	}
	compacted := current
	if !compacted.Matches(current) {
		t.Fatal("compaction must preserve a matching authorization")
	}
	afterUserInput := current
	afterUserInput.UserMessageRevision++
	if afterUserInput.Matches(current) {
		t.Fatal("new user input must invalidate the retained authorization")
	}
	assessment := state.ServingAssessment{Authorization: current}
	if !assessment.AllowsSynchronousReviewSkip(current) {
		t.Fatal("a current low-risk assessment must skip the synchronous review")
	}
	if assessment.AllowsSynchronousReviewSkip(afterUserInput) {
		t.Fatal("a stale user-message revision must force a fresh review")
	}
}

// Mirrors Rust #41660: the live serving authorization carries the thread's
// user-message revision, so the reviewer compares authorization rather than the
// rewritten conversation.
func TestGuardianServingAuthorizationCarriesRevisionLikeRust(t *testing.T) {
	const threadID, turnID = "thread-serving-revision", "turn-serving-revision"
	router := servingAssessmentTestRouter(t, threadID, turnID)
	router.bumpGuardianUserMessageRevision(threadID)
	router.bumpGuardianUserMessageRevision(threadID)
	if got := router.guardianServingAuthorization(threadID, turnID).UserMessageRevision; got != 2 {
		t.Fatalf("serving authorization revision = %d, want 2", got)
	}
}
