package appserver

import (
	"strings"

	"codex_go/session"
)

// moderation returns the final-answer moderation result attached to this item.
// The structured field wins; a value restored from the wire without the typed
// field (an older rollout record) is read back from the item data so a replay
// does not silently drop a verdict that was recorded before this field existed.
func (i *ThreadItem) moderation() any {
	if i == nil {
		return nil
	}
	if i.Moderation != nil {
		return i.Moderation
	}
	return threadItemAnyFromData(i.Data, "moderation", "finalAnswerModeration", "final_answer_moderation")
}

// finalAnswerModerationKey names the per-turn slot that keeps the moderation
// metadata of a turn's final answer.
func finalAnswerModerationKey(threadID string, turnID string) string {
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return ""
	}
	return threadID + "\x00" + turnID
}

// recordFinalAnswerModeration keeps the moderation metadata the model stream
// reported for this turn (#59842). The turn-scoped turn/moderationMetadata
// notification still fires exactly as before; this copy only exists so the
// completed answer item can carry the verdict that judged it.
func (r *RuntimeRouter) recordFinalAnswerModeration(threadID string, turnID string, metadata any) {
	if r == nil || metadata == nil {
		return
	}
	key := finalAnswerModerationKey(threadID, turnID)
	if key == "" {
		return
	}
	r.finalAnswerModerationMu.Lock()
	defer r.finalAnswerModerationMu.Unlock()
	if r.finalAnswerModeration == nil {
		r.finalAnswerModeration = map[string]any{}
	}
	r.finalAnswerModeration[key] = metadata
}

// takeFinalAnswerModeration returns the moderation metadata recorded for one
// turn and forgets it, so a reused turn id in another thread cannot inherit a
// stale verdict.
func (r *RuntimeRouter) takeFinalAnswerModeration(threadID string, turnID string) any {
	if r == nil {
		return nil
	}
	key := finalAnswerModerationKey(threadID, turnID)
	if key == "" {
		return nil
	}
	r.finalAnswerModerationMu.Lock()
	defer r.finalAnswerModerationMu.Unlock()
	metadata := r.finalAnswerModeration[key]
	delete(r.finalAnswerModeration, key)
	return metadata
}

// finalAnswerThreadItemID names the item a turn's moderation verdict belongs to:
// the newest agent message that was actually delivered to the user. Async
// sub-agent deliveries and empty messages are not the turn's answer, matching
// finalAgentMessageSummary's selection.
func finalAnswerThreadItemID(items []session.Item) string {
	for index := len(items) - 1; index >= 0; index-- {
		threadItem := BuildThreadItem(items[index])
		if normalizeThreadItemType(threadItem.Type) != "agentMessage" {
			continue
		}
		if strings.TrimSpace(threadItem.Text) == "" || strings.TrimSpace(threadItem.Delivery) == "async" {
			continue
		}
		return threadItem.ID
	}
	return ""
}

// applyFinalAnswerModeration attaches the turn's moderation metadata to the
// completed final answer item and to nothing else, so a client can read the
// verdict straight off the answer it judged instead of correlating it with a
// separate turn-scoped notification.
func applyFinalAnswerModeration(threadItem *ThreadItem, finalAnswerID string, moderation any) {
	if threadItem == nil || moderation == nil {
		return
	}
	if strings.TrimSpace(finalAnswerID) == "" || threadItem.ID != finalAnswerID {
		return
	}
	if normalizeThreadItemType(threadItem.Type) != "agentMessage" {
		return
	}
	threadItem.Moderation = moderation
}
