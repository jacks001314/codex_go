package appserver

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	contextfrag "codex_go/context"
	"codex_go/rollout"
	"codex_go/session"
)

// Persists explicit user goal mutations with user provenance before goal work
// resumes. Unloaded threads receive the same item on replay; tool-created goals
// never use this path.
//
// Mirrors Rust #49598
// (`app-server/src/request_processors/thread_goal_user_context.rs`).
//
// recordUserGoalUpdate records one `thread/goal/set` or `thread/goal/clear`
// instruction as a model-visible user message before the goal state changes. A
// recording failure leaves goal state unchanged.
func (r *RuntimeRouter) recordUserGoalUpdate(threadID string, rolloutPath string, update *contextfrag.UserGoalUpdate) error {
	if r == nil || update == nil {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	item, ok := userGoalInstructionItem(update)
	if !ok {
		return nil
	}
	// Rust records the canonical item through the writer-owned rollout recorder
	// before touching the thread's model history; the same order is kept here so
	// a failed history write cannot leave a durable instruction behind a changed
	// goal, and a recorded intent stays valid if the state write later fails.
	if err := r.appendUserGoalInstructionToRollout(threadID, rolloutPath, item); err != nil {
		return fmt.Errorf("failed to record goal instruction: %w", err)
	}
	if _, err := r.services.ThreadRouter.appendThreadItems(session.ThreadID(threadID), []session.Item{item}); err != nil {
		return fmt.Errorf("failed to record goal instruction: %w", err)
	}
	return nil
}

// recordUserGoalSetInstruction records an explicit user `thread/goal/set` when
// the request carries user provenance and an accepted objective or status.
// Automatic lifecycle mutations never supply user authorization.
func (r *RuntimeRouter) recordUserGoalSetInstruction(params *GoalSetParams, rolloutPath string) error {
	if params == nil || params.Origin == nil || *params.Origin != ThreadGoalMutationOriginUser {
		return nil
	}
	if params.Objective == nil && params.Status == nil {
		return nil
	}
	var objective *string
	if params.Objective != nil {
		trimmed := strings.TrimSpace(*params.Objective)
		objective = &trimmed
	}
	var status *string
	if params.Status != nil {
		value := string(*params.Status)
		status = &value
	}
	return r.recordUserGoalUpdate(params.ThreadID, rolloutPath, contextfrag.NewUserGoalSet(objective, status))
}

// recordUserGoalClearInstruction records an explicit user `thread/goal/clear`
// before the goal row is deleted. Like Rust, the clear instruction is recorded
// even when no goal exists, because a failed set can leave authorization in
// history without a goal row.
func (r *RuntimeRouter) recordUserGoalClearInstruction(params *GoalClearParams, rolloutPath string) error {
	if params == nil || params.Origin == nil || *params.Origin != ThreadGoalMutationOriginUser {
		return nil
	}
	return r.recordUserGoalUpdate(params.ThreadID, rolloutPath, contextfrag.NewUserGoalClear())
}

// appendUserGoalInstructionToRollout writes the instruction to the thread's
// rollout log. A goal-first thread whose rollout has not materialized yet is
// skipped: the goal handler materializes it, and the record append already makes
// the instruction model-visible.
func (r *RuntimeRouter) appendUserGoalInstructionToRollout(threadID string, rolloutPath string, item session.Item) error {
	if r == nil || r.services.ThreadRouter == nil {
		return nil
	}
	path := strings.TrimSpace(rolloutPath)
	if path == "" {
		resolved, err := r.services.ThreadRouter.findThreadRolloutPath(session.ThreadID(threadID), false)
		if err != nil {
			return nil
		}
		path = strings.TrimSpace(resolved)
	}
	if path == "" {
		return nil
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	recorder, err := rollout.Resume(path)
	if err != nil {
		return err
	}
	r.services.ThreadRouter.configureThreadHistoryRecorder(recorder, session.ThreadID(threadID))
	if err := recorder.AppendUserGoalInstruction(item, time.Now().UTC()); err != nil {
		_ = recorder.Close()
		return err
	}
	return recorder.Close()
}

// userGoalInstructionItem builds the stored model-visible item for a recorded
// goal instruction. The payload mirrors the ResponseItem that Rust's
// `ContextualUserFragment::into` produces: a user-role message whose single
// input_text carries the fragment markers, annotated with the host-owned
// content kind. The text is kept off the item body so appending the instruction
// does not overwrite the thread preview.
func userGoalInstructionItem(update *contextfrag.UserGoalUpdate) (session.Item, bool) {
	rendered := contextfrag.RenderStandalone(update)
	if rendered == nil || strings.TrimSpace(rendered.Content) == "" {
		return session.Item{}, false
	}
	raw, err := json.Marshal(map[string]any{
		"type": "message",
		"role": rendered.Role,
		"content": []map[string]any{
			{"type": "input_text", "text": rendered.Content},
		},
		"internal_chat_message_metadata_passthrough": map[string]any{
			"content_item_kinds": []string{rendered.ContentKind},
		},
	})
	if err != nil {
		return session.Item{}, false
	}
	return session.Item{
		ID:        newUserGoalInstructionID(),
		Type:      "message",
		Role:      rendered.Role,
		CreatedAt: time.Now().UTC(),
		Raw:       raw,
		Data:      map[string]any{"kind": rendered.ContentKind},
	}, true
}

func newUserGoalInstructionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("user-goal-%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("user-goal-%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
