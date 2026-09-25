package appserver

import (
	"strings"

	"codex_go/retainedctx"
	"codex_go/rollout"
	"codex_go/session"
)

// Rust parity: codex-history's retained context as the host owns it
// (history/src/lib.rs and retained_context.rs), consumed by the Guardian review
// prompt's retained user-instruction section (guardian-context's
// `retained_instructions.rs`).
//
// Rust records the evidence when the host accepts it and checkpoints the
// snapshot with every compaction. Go re-derives the same evidence from the
// durable history instead: the thread's newest checkpoint supplies everything
// that predates it, the thread's remaining accepted user messages follow in
// order, and re-recording an identical message is idempotent (Rust's id-based
// dedup). A restarted process therefore rebuilds the same evidence, and a
// compaction can never drop it from the reviewer's view.

// retainedContextForThread resolves one thread's retained evidence, or nil when
// the thread has none. The returned snapshot is a copy: a concurrent review
// renders it while later resolutions keep recording new messages.
func (r *RuntimeRouter) retainedContextForThread(threadID string) *retainedctx.RetainedContext {
	if r == nil {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	r.retainedContextsMu.Lock()
	defer r.retainedContextsMu.Unlock()
	context := r.retainedContexts[threadID]
	if context == nil {
		context = r.checkpointRetainedContext(threadID)
		if context == nil {
			context = &retainedctx.RetainedContext{}
		}
		if r.retainedContexts == nil {
			r.retainedContexts = map[string]*retainedctx.RetainedContext{}
		}
		r.retainedContexts[threadID] = context
	}
	r.recordRetainedUserMessages(context, threadID)
	if len(context.OrderedEntries()) == 0 {
		return nil
	}
	return context.Clone()
}

// forgetThreadRetainedContext drops a thread's in-memory retained evidence when
// its runtime unloads. The next load rebuilds it from the durable checkpoint and
// the thread's history.
func (r *RuntimeRouter) forgetThreadRetainedContext(threadID string) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	r.retainedContextsMu.Lock()
	delete(r.retainedContexts, threadID)
	r.retainedContextsMu.Unlock()
}

// checkpointRetainedContext reads the newest compaction checkpoint's retained
// snapshot, which is what a resumed thread restores before later evidence is
// replayed.
func (r *RuntimeRouter) checkpointRetainedContext(threadID string) *retainedctx.RetainedContext {
	record, err := r.threadRecord(session.ThreadID(threadID), false, false)
	if err != nil || record == nil {
		return nil
	}
	path := r.services.ThreadRouter.threadRolloutPath(record)
	if strings.TrimSpace(path) == "" {
		return nil
	}
	lines, _, err := rollout.Load(path)
	if err != nil {
		return nil
	}
	return rollout.CompactedRetainedContext(lines)
}

// recordRetainedUserMessages records every accepted user message the thread's
// history still holds. Identical messages dedupe by id, so repeated calls only
// add what arrived since the last one.
func (r *RuntimeRouter) recordRetainedUserMessages(context *retainedctx.RetainedContext, threadID string) {
	record, err := r.threadRecord(session.ThreadID(threadID), true, true)
	if err != nil || record == nil {
		return
	}
	for index := range record.Items {
		item := &record.Items[index]
		if !sessionItemIsUserMessage(item) {
			continue
		}
		text := strings.TrimSpace(firstNonEmpty(item.Text, stringValueFromMap(item.Data, "text")))
		if text == "" {
			continue
		}
		message := retainedctx.RetainedUserMessage{
			TurnID:   runtimeSessionItemTurnID(item, index),
			Text:     text,
			Complete: true,
		}
		if id := strings.TrimSpace(item.ID); id != "" {
			message.MessageID = &id
		}
		context.RecordUserMessage(message, retainedctx.LocalInputSource(nil))
	}
}
