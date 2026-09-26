package appserver

import (
	"strings"
	"time"

	"codex_go/features"
	"codex_go/retainedctx"
	"codex_go/rollout"
	"codex_go/session"
	"codex_go/tool"
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
	// Rust replays the sparse facts recorded after the newest checkpoint on top
	// of its snapshot, in acceptance order; re-recording a fact the snapshot
	// already holds is idempotent. The thread's user messages are derived first so
	// their orders stay ahead of a fact accepted later.
	for _, event := range r.retainedContextEvents(threadID) {
		context.Record(event)
	}
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

// retainedContextEvents reads the sparse retained-context facts the thread's
// rollout carries (Rust RolloutItem::RetainedContext), in order.
func (r *RuntimeRouter) retainedContextEvents(threadID string) []retainedctx.RetainedContextEvent {
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
	return rollout.RetainedContextEvents(lines)
}

// recordRetainedContextEvent mirrors Rust's `Session::record_retained_context`
// (#44893): bound the host fact, record it into the thread's live retained
// evidence, and persist the sparse rollout line so a resumed thread replays it.
// The thread's already-accepted user messages are refreshed first, so the fact
// keeps the acceptance order Rust captured when the host accepted it.
func (r *RuntimeRouter) recordRetainedContextEvent(threadID string, event retainedctx.RetainedContextEvent) bool {
	if r == nil {
		return false
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return false
	}
	event.Bound()
	recorded := false
	r.retainedContextsMu.Lock()
	context := r.retainedContexts[threadID]
	created := false
	if context == nil {
		context = r.checkpointRetainedContext(threadID)
		if context == nil {
			context = &retainedctx.RetainedContext{}
		}
		if r.retainedContexts == nil {
			r.retainedContexts = map[string]*retainedctx.RetainedContext{}
		}
		r.retainedContexts[threadID] = context
		created = true
	}
	r.recordRetainedUserMessages(context, threadID)
	if created {
		for _, replayed := range r.retainedContextEvents(threadID) {
			context.Record(replayed)
		}
	}
	if event.AcceptanceOrder == nil {
		order := context.ReserveOrder()
		event.AcceptanceOrder = &order
	}
	recorded = context.Record(event)
	r.retainedContextsMu.Unlock()
	if !recorded {
		return false
	}
	now := time.Now().UTC()
	_ = r.withRuntimeRollout(threadID, func(recorder *rollout.Recorder) error {
		return recorder.AppendRetainedContext(event, now)
	})
	return true
}

// retainedVerifiedAnswerRecorder mirrors Rust's request_user_input handler: the
// answers the host accepted for a turn's call are recorded as retained evidence,
// keyed by the turn and the tool call so the reviewer can render them.
func (r *RuntimeRouter) retainedVerifiedAnswerRecorder(threadID string, turnID string) tool.VerifiedAnswerRecorder {
	return r.verifiedAnswerRecorderForTurn(threadID, turnID, nil)
}

// verifiedAnswerRecorderForTurn installs the retained verified-answer recorder
// only when Rust's guardian-approval feature is enabled: the handler records the
// evidence behind `if turn.config.features.enabled(Feature::GuardianApproval)`
// (request_user_input.rs).
func (r *RuntimeRouter) verifiedAnswerRecorderForTurn(threadID string, turnID string, settings map[string]bool) tool.VerifiedAnswerRecorder {
	if !features.Enabled(settings, "guardian_approval") {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	return func(callID string, questions []retainedctx.VerifiedQuestionAnswer) {
		if r == nil || threadID == "" || strings.TrimSpace(callID) == "" || len(questions) == 0 {
			return
		}
		r.recordRetainedContextEvent(threadID, retainedctx.RetainedContextEvent{
			Answer: retainedctx.VerifiedAnswer{
				TurnID:    turnID,
				CallID:    strings.TrimSpace(callID),
				Questions: questions,
			},
		})
	}
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
