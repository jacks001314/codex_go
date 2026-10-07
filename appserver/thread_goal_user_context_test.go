package appserver

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"codex_go/session"
)

// Mirrors Rust #49598 `user_goal_updates_survive_resume_and_clear`
// (codex-rs/app-server/tests/suite/v2/guardian_goal_tests.rs): explicit user
// goal edits and clears enter the model-visible history in order, while
// automatic lifecycle mutations never create user authorization.
func TestUserGoalUpdatesSurviveResumeAndClearLikeRust(t *testing.T) {
	router, _, threadID := newGoalToolTestRouter(t)
	automatic := ThreadGoalMutationOriginAutomatic
	user := ThreadGoalMutationOriginUser
	paused := GoalPaused

	// Automatic lifecycle calls mutate goal state without creating user
	// authorization.
	automaticObjective := "Automatically generated goal."
	if _, _, _, err := router.setStateThreadGoal(&GoalSetParams{
		ThreadID: threadID, Objective: &automaticObjective, Status: &paused, Origin: &automatic,
	}); err != nil {
		t.Fatalf("automatic set error = %v", err)
	}

	for _, objective := range []string{
		"Send the approved report.",
		"Draft the report, but do not send it.",
	} {
		objective := objective
		if _, _, _, err := router.setStateThreadGoal(&GoalSetParams{
			ThreadID: threadID, Objective: &objective, Status: &paused, Origin: &user,
		}); err != nil {
			t.Fatalf("user set %q error = %v", objective, err)
		}
	}

	if _, _, _, err := router.clearStateThreadGoal(&GoalClearParams{ThreadID: threadID, Origin: &user}); err != nil {
		t.Fatalf("user clear error = %v", err)
	}

	history := goalInstructionHistoryText(t, router, threadID)
	if strings.Contains(history, automaticObjective) {
		t.Fatalf("automatic goal leaked into model history: %s", history)
	}
	old := strings.Index(history, `User set the goal: "Send the approved report."`)
	updated := strings.Index(history, `User set the goal: "Draft the report, but do not send it."`)
	if old < 0 {
		t.Fatalf("original user goal missing from model history: %s", history)
	}
	if updated < 0 {
		t.Fatalf("updated user goal missing from model history: %s", history)
	}
	if old >= updated {
		t.Fatalf("user goal edits out of order: %s", history)
	}
	if !strings.Contains(history, `User set goal status: "paused".`) {
		t.Fatalf("status instruction missing from model history: %s", history)
	}
	if !strings.Contains(history, "User cleared the goal.") {
		t.Fatalf("clear instruction missing from model history: %s", history)
	}
}

// The recorded instruction is annotated with the host-owned content kind so the
// model-visible item is recognizable as a goal instruction (Rust
// `UserGoalUpdate::message_text`).
func TestUserGoalInstructionCarriesHostAnnotationLikeRust(t *testing.T) {
	router, _, threadID := newGoalToolTestRouter(t)
	user := ThreadGoalMutationOriginUser
	objective := "Annotated goal."
	if _, _, _, err := router.setStateThreadGoal(&GoalSetParams{
		ThreadID: threadID, Objective: &objective, Origin: &user,
	}); err != nil {
		t.Fatalf("user set error = %v", err)
	}
	items, _ := router.historyInputItemsForTurn(threadID)
	found := false
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok || strings.TrimSpace(stringFromAny(item["type"])) != "message" {
			continue
		}
		if !strings.Contains(textFromInputItemContent(item["content"]), "Annotated goal.") {
			continue
		}
		found = true
		passthrough, _ := item["internal_chat_message_metadata_passthrough"].(map[string]any)
		kinds, _ := passthrough["content_item_kinds"].([]any)
		if len(kinds) != 1 || strings.TrimSpace(stringFromAny(kinds[0])) != "user.goal" {
			t.Fatalf("goal instruction content kinds = %#v, want [user.goal]", passthrough)
		}
	}
	if !found {
		t.Fatalf("annotated goal instruction missing from model history")
	}

	// The instruction is also persisted to the rollout log: Rust records the
	// canonical item through the writer-owned rollout recorder before the thread
	// history write.
	path, err := router.services.ThreadRouter.findThreadRolloutPath(session.ThreadID(threadID), false)
	if err != nil {
		t.Fatalf("findThreadRolloutPath error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read rollout error = %v", err)
	}
	if !strings.Contains(string(data), "user_goal") || !strings.Contains(string(data), "Annotated goal.") {
		t.Fatalf("goal instruction missing from rollout log: %s", string(data))
	}
}

// goalInstructionHistoryText joins the model-visible user message texts for a
// thread, in order.
func goalInstructionHistoryText(t *testing.T, router *RuntimeRouter, threadID string) string {
	t.Helper()
	items, _ := router.historyInputItemsForTurn(threadID)
	texts := make([]string, 0, len(items))
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok || strings.TrimSpace(stringFromAny(item["role"])) != "user" {
			continue
		}
		texts = append(texts, textFromInputItemContent(item["content"]))
	}
	return strings.Join(texts, "\n")
}

// A recording failure must surface as an error so the goal handler leaves goal
// state unchanged (Rust #49598 `set_thread_goal`/`clear_thread_goal` propagate
// the recording future's error before touching goal state).
func TestUserGoalRecordingFailurePropagatesLikeRust(t *testing.T) {
	router, _, _ := newGoalToolTestRouter(t)
	objective := "unrecordable goal"
	err := router.recordUserGoalSetInstruction(&GoalSetParams{
		ThreadID: "missing-thread", Objective: &objective, Origin: originPtrForTest(ThreadGoalMutationOriginUser),
	}, "")
	if err == nil {
		t.Fatal("recordUserGoalSetInstruction for a missing thread returned nil error")
	}
	if !strings.Contains(err.Error(), "failed to record goal instruction") {
		t.Fatalf("recording failure error = %v", err)
	}
}

func originPtrForTest(origin ThreadGoalMutationOrigin) *ThreadGoalMutationOrigin {
	return &origin
}

// Rust #49598 adds the optional `origin` provenance to thread/goal/set and
// thread/goal/clear. A missing origin does not supply user authorization.
func TestGoalOriginWireDecodingLikeRust(t *testing.T) {
	var set GoalSetParams
	if err := json.Unmarshal([]byte(`{"threadId":"t","origin":"user"}`), &set); err != nil {
		t.Fatalf("set unmarshal error = %v", err)
	}
	if set.Origin == nil || *set.Origin != ThreadGoalMutationOriginUser {
		t.Fatalf("set origin = %#v, want user", set.Origin)
	}

	var automatic GoalSetParams
	if err := json.Unmarshal([]byte(`{"threadId":"t","origin":"automatic"}`), &automatic); err != nil {
		t.Fatalf("automatic unmarshal error = %v", err)
	}
	if automatic.Origin == nil || *automatic.Origin != ThreadGoalMutationOriginAutomatic {
		t.Fatalf("automatic origin = %#v, want automatic", automatic.Origin)
	}

	var omitted GoalSetParams
	if err := json.Unmarshal([]byte(`{"threadId":"t"}`), &omitted); err != nil {
		t.Fatalf("omitted unmarshal error = %v", err)
	}
	if omitted.Origin != nil {
		t.Fatalf("omitted origin = %#v, want nil", omitted.Origin)
	}

	var clear GoalClearParams
	if err := json.Unmarshal([]byte(`{"threadId":"t","origin":"user"}`), &clear); err != nil {
		t.Fatalf("clear unmarshal error = %v", err)
	}
	if clear.Origin == nil || *clear.Origin != ThreadGoalMutationOriginUser {
		t.Fatalf("clear origin = %#v, want user", clear.Origin)
	}
}
