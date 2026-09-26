package appserver

import (
	"encoding/json"
	"strings"
	"time"

	"codex_go/features"
	"codex_go/retainedctx"
	"codex_go/rollout"
	"codex_go/session"
	"codex_go/tool"
	"codex_go/utils"
)

// guardianMaxRootMessageTokens mirrors
// `guardian::GUARDIAN_MAX_ROOT_MESSAGE_TOKENS`: the budget the host applies to
// original instruction and assistant evidence before retaining it.
const guardianMaxRootMessageTokens = 900

// retainedOmittedObjectiveKind mirrors `UserGoalUpdate::OMITTED_OBJECTIVE_KIND`:
// a harness-authored goal placeholder cannot prove that the original objective
// text was captured.
const retainedOmittedObjectiveKind = "user.goal.omitted"

// harnessMetadataKey is where a session item carries its persisted harness
// metadata (Rust's CodexHarnessMetadata sidecar of a response item).
const harnessMetadataKey = "harness_metadata"

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
	context := r.retainedLiveContextLocked(threadID)
	if len(context.OrderedEntries()) == 0 {
		return nil
	}
	return context.Clone()
}

// retainedLiveContextLocked returns the thread's live retained evidence, seeded
// from its newest checkpoint and the sparse facts recorded after it. The caller
// must hold retainedContextsMu.
func (r *RuntimeRouter) retainedLiveContextLocked(threadID string) *retainedctx.RetainedContext {
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
	// The thread's recorded messages are derived first, so their orders stay
	// ahead of a fact accepted later; re-recording an identical message is
	// idempotent (Rust's id-based dedup).
	r.recordRetainedUserMessages(context, threadID)
	if created {
		// Rust replays the sparse facts recorded after the newest checkpoint on
		// top of its snapshot, in acceptance order.
		for _, event := range r.retainedContextEvents(threadID) {
			context.Record(event)
		}
	}
	return context
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
		metadata := harnessMetadataFromSessionItem(item)
		record, ok := retainedRecordForSessionItem(item, index, metadata)
		if !ok {
			continue
		}
		recordRetainedSessionItem(context, record, metadata)
	}
}

// retainedSessionItemRecord is the retained record one history item produces.
type retainedSessionItemRecord struct {
	assistant bool
	message   retainedctx.RetainedUserMessage
}

// retainedRecordForSessionItem mirrors Rust's
// `ContextManager::record_retained_message`: the message family, the text bounded
// by the guardian budget, and the completeness the item's own evidence
// establishes. Compaction output is skipped, because a summary is a digest of the
// conversation rather than host-observed evidence.
func retainedRecordForSessionItem(item *session.Item, index int, metadata *retainedctx.HarnessMetadata) (retainedSessionItemRecord, bool) {
	if item == nil || sessionItemIsCompactionOutput(item) {
		return retainedSessionItemRecord{}, false
	}
	userMessage := sessionItemIsUserMessage(item)
	assistantMessage := sessionItemIsAssistantMessage(item)
	if !userMessage && !assistantMessage {
		return retainedSessionItemRecord{}, false
	}
	responseItem, hasResponseItem := sessionItemResponseItemFieldsFromItem(item)
	if userMessage && !assistantMessage && !sessionItemIsUserAuthorizationMessage(responseItem, hasResponseItem) {
		// Rust only retains a user message whose content classifications do not
		// show it to be a contextual fragment rather than genuine user input.
		return retainedSessionItemRecord{}, false
	}
	text := strings.TrimSpace(firstNonEmpty(item.Text, stringValueFromMap(item.Data, "text")))
	if text == "" {
		return retainedSessionItemRecord{}, false
	}
	message := retainedctx.RetainedUserMessage{}
	if metadata != nil && metadata.RetainedSource != nil {
		// A recorded item keeps the identity and turn its source captured, so a
		// replay cannot mint a different version of the same evidence.
		message.TurnID = strings.TrimSpace(metadata.RetainedSource.ID.TurnID)
		if messageID := strings.TrimSpace(metadata.RetainedSource.ID.MessageID); messageID != "" {
			message.MessageID = &messageID
		}
	} else {
		message.TurnID = runtimeSessionItemTurnID(item, index)
		if id := strings.TrimSpace(item.ID); id != "" {
			message.MessageID = &id
		}
	}
	if assistantMessage {
		message.Text = utils.TruncateText(text, utils.TokensPolicy(guardianMaxRootMessageTokens))
		message.Complete = len(text) <= utils.ApproxBytesForTokens(guardianMaxRootMessageTokens)
	} else {
		// Rust bounds every retained original, and an instruction is only complete
		// when the harness classified each content entry as genuine user content.
		message.Text = utils.TruncateText(text, utils.TokensPolicy(guardianMaxRootMessageTokens))
		message.Complete = retainedInstructionComplete(responseItem, hasResponseItem) &&
			len(text) <= utils.ApproxBytesForTokens(guardianMaxRootMessageTokens)
	}
	if metadata != nil && metadata.RetainedSource != nil && !metadata.RetainedSource.Complete {
		// Rust narrows a record's completeness with the captured source.
		message.Complete = false
	}
	return retainedSessionItemRecord{assistant: assistantMessage, message: message}, true
}

// recordRetainedSessionItem records one item's retained evidence, using the
// source its harness metadata carries, restoring a recorded revision so the
// delivery proof survives a replay (Rust's `replay_annotated_item`). The
// returned order is the accepted position the entry now holds, when it has one.
func recordRetainedSessionItem(context *retainedctx.RetainedContext, record retainedSessionItemRecord, metadata *retainedctx.HarnessMetadata) (*retainedctx.RetainedSource, uint64, bool) {
	if context == nil {
		return nil, 0, false
	}
	source := retainedctx.RetainedInputSourceFromMetadata(metadata)
	if record.assistant && !source.Inherited && source.Order == nil {
		// Rust reserves an assistant message's order once and persists it with
		// the item; a re-derivation reuses the recorded order instead of
		// advancing the thread's counter again.
		order, recorded := context.AssistantMessageOrder(record.message.MessageID)
		if !recorded {
			order = context.ReserveOrder()
		}
		source = retainedctx.LocalInputSource(&order)
	}
	var captured *retainedctx.RetainedSource
	if record.assistant {
		captured = context.RecordAssistantMessage(record.message, source)
	} else {
		captured = context.RecordUserMessage(record.message, source)
	}
	if captured != nil && metadata != nil && metadata.RetainedSource != nil &&
		captured.ID == metadata.RetainedSource.ID && captured.Complete == metadata.RetainedSource.Complete {
		context.RestoreSourceRevision(metadata.RetainedSource)
	}
	if captured == nil {
		return nil, 0, false
	}
	if source.Inherited {
		return captured, 0, false
	}
	var order uint64
	var ok bool
	if record.assistant {
		order, ok = context.AssistantMessageOrder(record.message.MessageID)
	} else {
		order, ok = context.UserMessageOrder(record.message.MessageID)
	}
	return captured, order, ok
}

// sessionItemIsCompactionOutput reports whether the history item is a compaction
// summary. Rust marks it with `CodexHarnessMetadata::compaction_output`; the
// session item carries the same fact as its compacted-history kind.
func sessionItemIsCompactionOutput(item *session.Item) bool {
	if item == nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(stringValueFromMap(item.Metadata, "kind")), "compaction_summary") {
		return true
	}
	if flag, ok := item.Metadata["compaction_output"].(bool); ok && flag {
		return true
	}
	return false
}

// harnessMetadataRawFromItem returns the item's persisted harness metadata JSON,
// whichever form the session carried it in.
func harnessMetadataRawFromItem(item *session.Item) json.RawMessage {
	if item == nil || item.Data == nil {
		return nil
	}
	switch value := item.Data[harnessMetadataKey].(type) {
	case json.RawMessage:
		return value
	case string:
		if strings.TrimSpace(value) == "" {
			return nil
		}
		return json.RawMessage(value)
	case map[string]any:
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil
		}
		return encoded
	default:
		return nil
	}
}

// harnessMetadataFromSessionItem parses the item's persisted harness metadata
// into the retained model's view of Rust's CodexHarnessMetadata.
func harnessMetadataFromSessionItem(item *session.Item) *retainedctx.HarnessMetadata {
	raw := harnessMetadataRawFromItem(item)
	if len(raw) == 0 {
		return nil
	}
	var metadata retainedctx.HarnessMetadata
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return nil
	}
	return &metadata
}

// annotateSessionItemHarnessMetadata merges the captured source and acceptance
// order into the item's harness metadata, preserving whatever else the item
// already carried (Rust writes both when the item is recorded).
func annotateSessionItemHarnessMetadata(item *session.Item, source *retainedctx.RetainedSource, order uint64, hasOrder bool) {
	if item == nil || source == nil {
		return
	}
	merged := map[string]any{}
	if raw := harnessMetadataRawFromItem(item); len(raw) > 0 {
		_ = json.Unmarshal(raw, &merged)
	}
	encodedSource, err := json.Marshal(source)
	if err != nil {
		return
	}
	merged["retained_source"] = json.RawMessage(encodedSource)
	if hasOrder {
		merged["user_input_order"] = order
	}
	encoded, err := json.Marshal(merged)
	if err != nil {
		return
	}
	if item.Data == nil {
		item.Data = map[string]any{}
	}
	item.Data[harnessMetadataKey] = json.RawMessage(encoded)
}

// annotateRetainedHarnessMetadata records the retained evidence of the items
// about to be appended and writes the host-observed source and acceptance order
// into their harness metadata, mirroring Rust's
// `ContextManager::record_annotated_items`. A resumed thread then restores the
// same delivery proof and order instead of minting a new revision.
func (r *RuntimeRouter) annotateRetainedHarnessMetadata(threadID session.ThreadID, items []session.Item) {
	if r == nil || len(items) == 0 {
		return
	}
	threadKey := strings.TrimSpace(string(threadID))
	if threadKey == "" || !retainedHarnessCandidates(items) {
		return
	}
	r.retainedContextsMu.Lock()
	defer r.retainedContextsMu.Unlock()
	context := r.retainedLiveContextLocked(threadKey)
	for index := range items {
		item := &items[index]
		metadata := harnessMetadataFromSessionItem(item)
		if metadata != nil && metadata.RetainedSource != nil {
			// The item already carries its recorded version.
			continue
		}
		record, ok := retainedRecordForSessionItem(item, index, metadata)
		if !ok {
			continue
		}
		captured, order, hasOrder := recordRetainedSessionItem(context, record, metadata)
		annotateSessionItemHarnessMetadata(item, captured, order, hasOrder)
	}
}

// retainedHarnessCandidates reports whether the batch holds a message the
// retained model can record, so appending unrelated items never resolves a
// thread's retained evidence.
func retainedHarnessCandidates(items []session.Item) bool {
	for index := range items {
		item := &items[index]
		if sessionItemIsCompactionOutput(item) {
			continue
		}
		if sessionItemIsUserMessage(item) || sessionItemIsAssistantMessage(item) {
			return true
		}
	}
	return false
}

// sessionItemResponseItemFields is the retained model's view of the response item
// a session item persisted: the harness-owned content classifications Rust reads
// from `internal_chat_message_metadata_passthrough`.
type sessionItemResponseItemFields struct {
	ContentTypes []string
	ContentKinds []string
}

// sessionItemResponseItemFields parses the item's persisted response item. ok is
// false for a legacy item without one, which Rust treats conservatively.
func sessionItemResponseItemFieldsFromItem(item *session.Item) (sessionItemResponseItemFields, bool) {
	if item == nil || len(item.Raw) == 0 {
		return sessionItemResponseItemFields{}, false
	}
	var payload struct {
		Content []struct {
			Type string `json:"type"`
		} `json:"content"`
		Metadata struct {
			Kinds []string `json:"content_item_kinds"`
		} `json:"internal_chat_message_metadata_passthrough"`
	}
	if err := json.Unmarshal(item.Raw, &payload); err != nil {
		return sessionItemResponseItemFields{}, false
	}
	fields := sessionItemResponseItemFields{
		ContentTypes: make([]string, 0, len(payload.Content)),
		ContentKinds: payload.Metadata.Kinds,
	}
	for _, content := range payload.Content {
		fields.ContentTypes = append(fields.ContentTypes, strings.TrimSpace(content.Type))
	}
	return fields, true
}

// sessionItemIsUserAuthorizationMessage mirrors Rust's
// `is_user_authorization_message`: unknown, incomplete and legacy classifications
// stay conservative, and a message whose classifications show contextual or media
// content is not authorization evidence.
func sessionItemIsUserAuthorizationMessage(fields sessionItemResponseItemFields, ok bool) bool {
	if !ok {
		// A legacy item carries no classification to doubt.
		return true
	}
	kinds := fields.ContentKinds
	if len(kinds) == 0 || len(kinds) != len(fields.ContentTypes) {
		return true
	}
	for _, kind := range kinds {
		switch kind {
		case "", "unknown", "images.preparation_error", "images.unsupported", "audio.unsupported":
			return true
		}
		if strings.HasPrefix(kind, "user.") {
			return true
		}
	}
	return false
}

// retainedInstructionComplete mirrors Rust's completeness proof for an original
// instruction: every content entry must carry a classification and all of them
// must be genuine user content, and every entry must be text.
func retainedInstructionComplete(fields sessionItemResponseItemFields, ok bool) bool {
	if !ok {
		return false
	}
	kinds := fields.ContentKinds
	if len(kinds) == 0 || len(kinds) != len(fields.ContentTypes) {
		return false
	}
	for _, kind := range kinds {
		if !strings.HasPrefix(kind, "user.") || kind == retainedOmittedObjectiveKind {
			return false
		}
	}
	for _, contentType := range fields.ContentTypes {
		if contentType != "input_text" && contentType != "output_text" {
			return false
		}
	}
	return true
}
