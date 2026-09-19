package appserver

import (
	"strings"

	"codex_go/model"
	"codex_go/telemetry"
	"codex_go/tool"
	"codex_go/turn"
)

const (
	// maxSampledToolCallsPerTurn bounds one turn's sampled call-id map, mirroring
	// Rust's MAX_TOOL_RESPONSE_ENTRIES.
	maxSampledToolCallsPerTurn = 256
	// maxSampledToolCallsPerResponse bounds the call ids collected from one
	// sampled response, mirroring
	// MAX_ANALYTICS_TOOL_CALL_IDS_PER_RESPONSE.
	maxSampledToolCallsPerResponse = 256
	// maxPendingToolEventsPerTurn bounds the correlated tool events waiting for a
	// later sampled response, mirroring Rust's MAX_TOOL_RESPONSE_ENTRIES.
	maxPendingToolEventsPerTurn = 256
)

func sampledToolCallsKey(threadID string, turnID string) string {
	return strings.TrimSpace(threadID) + "\x00" + strings.TrimSpace(turnID)
}

// rememberSampledToolCallsFromTurnResult publishes the tool calls of every model
// response the turn sampled. Rust's core publishes this fact from the response
// event loop, which sees every response; a Go turn whose agent streams publishes
// each response as it completes, and this covers the aggregated turn result too
// (for agents that do not stream).
func (r *RuntimeRouter) rememberSampledToolCallsFromTurnResult(threadID string, turnID string, result *turn.AgentLoopResult) {
	if r == nil || result == nil {
		return
	}
	for _, response := range result.ModelResponses() {
		if response == nil {
			continue
		}
		callIDs := make([]string, 0, len(response.Items))
		for i := range response.Items {
			if callID := sampledOutputToolCallID(&response.Items[i]); callID != "" {
				callIDs = append(callIDs, callID)
				if len(callIDs) >= maxSampledToolCallsPerResponse {
					break
				}
			}
		}
		r.rememberSampledToolCalls(threadID, turnID, response.ResponseID, callIDs)
	}
}

// rememberSampledToolCalls records the tool calls one sampled model response
// emitted, keyed by their call ids (Rust #45535's
// `ingest_sampling_response_completed`). The response ids themselves are kept for
// the response-correlation lane; the classification only needs the call ids.
func (r *RuntimeRouter) rememberSampledToolCalls(threadID string, turnID string, responseID string, callIDs []string) {
	if r == nil || len(callIDs) == 0 {
		r.releasePendingToolEvents(threadID, turnID, responseID)
		return
	}
	key := sampledToolCallsKey(threadID, turnID)
	if strings.TrimSpace(key) == "\x00" {
		return
	}
	r.sampledToolCallsMu.Lock()
	if r.sampledToolCalls == nil {
		r.sampledToolCalls = map[string]map[string]string{}
	}
	calls := r.sampledToolCalls[key]
	if calls == nil {
		calls = map[string]string{}
		r.sampledToolCalls[key] = calls
	}
	for _, callID := range callIDs {
		callID = strings.TrimSpace(callID)
		if callID == "" {
			continue
		}
		if _, known := calls[callID]; !known && len(calls) >= maxSampledToolCallsPerTurn {
			continue
		}
		calls[callID] = strings.TrimSpace(responseID)
	}
	// Rust fills a cell's missing originating response as soon as the response of
	// its parent call is known.
	if responseID = strings.TrimSpace(responseID); responseID != "" {
		for cellID, cell := range r.codeModeCells[threadID] {
			if cell.OriginatingResponseID != "" {
				continue
			}
			if known, ok := calls[cell.ParentCallID]; ok && strings.TrimSpace(known) != "" {
				cell.OriginatingResponseID = strings.TrimSpace(known)
				r.codeModeCells[threadID][cellID] = cell
			}
		}
	}
	r.sampledToolCallsMu.Unlock()
	// Rust releases the events waiting on a later response as soon as one is
	// known.
	r.releasePendingToolEvents(threadID, turnID, responseID)
}

// pendingToolEvent is one correlated tool event held until a later sampled
// response is known; releasing it fills the event's subsequent response id
// (Rust #36729's `pending_tool_events`).
type pendingToolEvent struct {
	base *telemetry.CodexToolItemEventBase
	emit func()
}

// emitToolEvent publishes one tool event the way Rust's `record_tool_event`
// does: an event with neither a cell nor an originating response is published
// immediately, while a correlated one waits until a later sampled response
// completes. The queue is bounded like Rust's MAX_TOOL_RESPONSE_ENTRIES, and the
// oldest event is published first when it overflows.
func (r *RuntimeRouter) emitToolEvent(threadID string, turnID string, base *telemetry.CodexToolItemEventBase, emit func()) {
	if r == nil || emit == nil {
		return
	}
	if base == nil || (base.CellID == nil && base.OriginatingResponseID == nil) {
		emit()
		return
	}
	key := sampledToolCallsKey(threadID, turnID)
	r.sampledToolCallsMu.Lock()
	if r.pendingToolEvents == nil {
		r.pendingToolEvents = map[string][]pendingToolEvent{}
	}
	pending := append(r.pendingToolEvents[key], pendingToolEvent{base: base, emit: emit})
	var overflow *pendingToolEvent
	if len(pending) > maxPendingToolEventsPerTurn {
		oldest := pending[0]
		pending = pending[1:]
		overflow = &oldest
	}
	r.pendingToolEvents[key] = pending
	r.sampledToolCallsMu.Unlock()
	if overflow != nil {
		overflow.emit()
	}
}

// releasePendingToolEvents publishes the queued events whose originating response
// is a different, already known response, stamping that response as their
// subsequent one (Rust's `ingest_sampling_response_completed` release loop).
func (r *RuntimeRouter) releasePendingToolEvents(threadID string, turnID string, responseID string) {
	if r == nil {
		return
	}
	responseID = strings.TrimSpace(responseID)
	key := sampledToolCallsKey(threadID, turnID)
	r.sampledToolCallsMu.Lock()
	pending := r.pendingToolEvents[key]
	if len(pending) == 0 {
		r.sampledToolCallsMu.Unlock()
		return
	}
	released := make([]pendingToolEvent, 0, len(pending))
	remaining := pending[:0]
	for _, event := range pending {
		if event.base != nil {
			r.enrichToolEventBaseLocked(event.base, threadID, turnID)
		}
		originating := ""
		if event.base != nil && event.base.OriginatingResponseID != nil {
			originating = strings.TrimSpace(*event.base.OriginatingResponseID)
		}
		if responseID == "" || originating == "" || originating == responseID {
			remaining = append(remaining, event)
			continue
		}
		if event.base != nil {
			subsequent := responseID
			event.base.SubsequentResponseID = &subsequent
		}
		released = append(released, event)
	}
	r.pendingToolEvents[key] = remaining
	r.sampledToolCallsMu.Unlock()
	for _, event := range released {
		event.emit()
	}
}

// flushPendingToolEvents publishes every queued event for a thread and turn,
// which is what Rust does when a turn completes or a thread closes.
func (r *RuntimeRouter) flushPendingToolEvents(threadID string, turnID string) {
	if r == nil {
		return
	}
	key := sampledToolCallsKey(threadID, turnID)
	r.sampledToolCallsMu.Lock()
	pending := r.pendingToolEvents[key]
	delete(r.pendingToolEvents, key)
	r.sampledToolCallsMu.Unlock()
	for _, event := range pending {
		event.emit()
	}
}

// flushThreadPendingToolEvents publishes every queued event for a closing thread.
func (r *RuntimeRouter) flushThreadPendingToolEvents(threadID string) {
	if r == nil {
		return
	}
	prefix := strings.TrimSpace(threadID) + "\x00"
	r.sampledToolCallsMu.Lock()
	flushed := []pendingToolEvent{}
	for key, pending := range r.pendingToolEvents {
		if strings.HasPrefix(key, prefix) {
			flushed = append(flushed, pending...)
			delete(r.pendingToolEvents, key)
		}
	}
	r.sampledToolCallsMu.Unlock()
	for _, event := range flushed {
		event.emit()
	}
}

// toolEventTypeForCall classifies a tool event from exact call-ID evidence
// (Rust #45535): a call id the model emitted in a sampled response is a model
// tool call, a call id a Code Mode cell dispatched is an inner tool call, and an
// item id that matches nothing - or both - stays null rather than guessing from
// cell association or lineage.
func (r *RuntimeRouter) toolEventTypeForCall(threadID string, turnID string, itemID string) *string {
	if r == nil {
		return nil
	}
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		return nil
	}
	key := sampledToolCallsKey(threadID, turnID)
	r.sampledToolCallsMu.Lock()
	_, sampled := r.sampledToolCalls[key][itemID]
	_, child := r.codeModeChildCalls[key][itemID]
	r.sampledToolCallsMu.Unlock()
	var eventType string
	switch {
	case sampled && !child:
		eventType = telemetry.ToolEventTypeModelToolCall
	case !sampled && child:
		eventType = telemetry.ToolEventTypeInnerToolCall
	default:
		return nil
	}
	return &eventType
}

// rememberCodeModeChildCall records one child call a Code Mode cell dispatched,
// with the cell it belongs to (Rust #45535's `cell_ids_by_child_call_id`). The
// evidence belongs to the turn whose cell dispatched the call, which is the
// thread's active turn at dispatch time.
func (r *RuntimeRouter) rememberCodeModeChildCall(threadID string, turnID string, cellID string, callID string) {
	if r == nil {
		return
	}
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return
	}
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	cellID = strings.TrimSpace(cellID)
	if turnID == "" {
		return
	}
	key := sampledToolCallsKey(threadID, turnID)
	r.sampledToolCallsMu.Lock()
	defer r.sampledToolCallsMu.Unlock()
	// Rust records a child call only while its cell is known.
	if cellID == "" {
		return
	}
	if _, known := r.codeModeCells[threadID][cellID]; !known {
		return
	}
	if r.codeModeChildCalls == nil {
		r.codeModeChildCalls = map[string]map[string]string{}
	}
	calls := r.codeModeChildCalls[key]
	if calls == nil {
		calls = map[string]string{}
		r.codeModeChildCalls[key] = calls
	}
	if _, known := calls[callID]; !known && len(calls) >= maxSampledToolCallsPerTurn {
		return
	}
	calls[callID] = cellID
}

// forgetSampledToolCalls drops a finished turn's evidence, mirroring Rust's
// per-turn `tool_response_states` removal.
func (r *RuntimeRouter) forgetSampledToolCalls(threadID string, turnID string) {
	if r == nil {
		return
	}
	r.sampledToolCallsMu.Lock()
	delete(r.sampledToolCalls, sampledToolCallsKey(threadID, turnID))
	delete(r.codeModeChildCalls, sampledToolCallsKey(threadID, turnID))
	// Rust drops the cells closed in this turn and keeps the rest for later
	// turns of the same thread.
	if cells := r.codeModeCells[threadID]; len(cells) > 0 {
		for cellID, cell := range cells {
			if cell.ClosedInTurnID == strings.TrimSpace(turnID) {
				delete(cells, cellID)
			}
		}
		if len(cells) == 0 {
			delete(r.codeModeCells, threadID)
		}
	}
	r.sampledToolCallsMu.Unlock()
}

// sampledOutputToolCallID reports the call id a sampled output item registers,
// mirroring Rust's OutputItemDone match: function and custom tool calls use their
// call id, tool-search and local-shell calls their optional call id, and web
// search / image generation calls their item id.
func sampledOutputToolCallID(item *model.AgentItem) string {
	if item == nil {
		return ""
	}
	switch item.Type {
	case "function_call", "custom_tool_call":
		return strings.TrimSpace(item.CallID)
	case "tool_search_call", "local_shell_call":
		return strings.TrimSpace(firstNonEmpty(item.CallID, item.ID))
	case "web_search_call", "image_generation_call":
		return strings.TrimSpace(firstNonEmpty(item.ID, item.CallID))
	default:
		return ""
	}
}

// codeModeCellState mirrors Rust's CodeModeCellState: the call that created the
// cell, the response that call came from, and the turn that closed the cell.
type codeModeCellState struct {
	ParentCallID          string
	OriginatingResponseID string
	ClosedInTurnID        string
}

// observeCodeModeCall records one code-mode cell or child-call fact (Rust's
// CodeModeToolCallFact) against the thread's active turn.
func (r *RuntimeRouter) observeCodeModeCall(threadID string, observation tool.CodeModeCallObservation) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	turnID := ""
	if active := r.threads.ActiveTurn(threadID); active != nil {
		turnID = strings.TrimSpace(active.TurnID)
	}
	switch observation.Kind {
	case tool.CodeModeCallCellStarted:
		r.rememberCodeModeCell(threadID, turnID, observation.CellID, observation.ParentCallID)
	case tool.CodeModeCallChildStarted:
		r.rememberCodeModeChildCall(threadID, turnID, observation.CellID, observation.CallID)
	case tool.CodeModeCallCellClosed:
		r.closeCodeModeCell(threadID, turnID, observation.CellID)
	case tool.CodeModeCallCompleted:
		r.emitCodeModeToolCallEvent(threadID, turnID, observation)
	}
}

// rememberCodeModeCell records the call that started a cell and, when the call's
// response is already known, the response the cell originated in (Rust's
// CodeModeToolCallFact::CellStarted).
func (r *RuntimeRouter) rememberCodeModeCell(threadID string, turnID string, cellID string, parentCallID string) {
	cellID = strings.TrimSpace(cellID)
	parentCallID = strings.TrimSpace(parentCallID)
	if cellID == "" || parentCallID == "" {
		return
	}
	r.sampledToolCallsMu.Lock()
	defer r.sampledToolCallsMu.Unlock()
	if r.codeModeCells == nil {
		r.codeModeCells = map[string]map[string]codeModeCellState{}
	}
	cells := r.codeModeCells[threadID]
	if cells == nil {
		cells = map[string]codeModeCellState{}
		r.codeModeCells[threadID] = cells
	}
	if _, known := cells[cellID]; !known && len(cells) >= maxSampledToolCallsPerTurn {
		return
	}
	cells[cellID] = codeModeCellState{
		ParentCallID:          parentCallID,
		OriginatingResponseID: r.sampledToolCalls[sampledToolCallsKey(threadID, turnID)][parentCallID],
	}
}

// closeCodeModeCell marks a cell finished in one turn, mirroring Rust's
// CodeModeToolCallFact::CellClosed; the cell is dropped when that turn ends.
func (r *RuntimeRouter) closeCodeModeCell(threadID string, turnID string, cellID string) {
	cellID = strings.TrimSpace(cellID)
	if cellID == "" || turnID == "" {
		return
	}
	r.sampledToolCallsMu.Lock()
	defer r.sampledToolCallsMu.Unlock()
	cells := r.codeModeCells[threadID]
	if cells == nil {
		return
	}
	cell, known := cells[cellID]
	if !known {
		return
	}
	cell.ClosedInTurnID = turnID
	cells[cellID] = cell
}

// enrichToolEventBase fills the code-mode correlation fields of one tool event,
// mirroring Rust's `enrich_tool_response_event`: a child call takes its cell from
// the child evidence, the cell supplies the parent call, and the originating
// response is the call's own sampled response or the cell's. A child call whose
// cell is unknown loses both fields.
func (r *RuntimeRouter) enrichToolEventBase(base *telemetry.CodexToolItemEventBase, threadID string, turnID string) {
	if r == nil || base == nil {
		return
	}
	base.SessionID = firstNonEmpty(base.SessionID, r.responsesMetadataLineage(threadID).SessionID)
	r.sampledToolCallsMu.Lock()
	defer r.sampledToolCallsMu.Unlock()
	r.enrichToolEventBaseLocked(base, threadID, turnID)
}

// enrichToolEventBaseLocked fills the correlation fields; the caller holds
// sampledToolCallsMu.
func (r *RuntimeRouter) enrichToolEventBaseLocked(base *telemetry.CodexToolItemEventBase, threadID string, turnID string) {
	if r == nil || base == nil {
		return
	}
	itemID := strings.TrimSpace(base.ItemID)
	if itemID == "" {
		return
	}
	key := sampledToolCallsKey(threadID, turnID)
	// Rust takes the event's own cell id when it has one (a code-mode fact sets
	// it explicitly) and otherwise derives it from the child-call evidence.
	cellID := ""
	if base.CellID != nil {
		cellID = strings.TrimSpace(*base.CellID)
	}
	if cellID == "" {
		cellID = strings.TrimSpace(r.codeModeChildCalls[key][itemID])
	}
	cell, cellKnown := r.codeModeCells[threadID][cellID]
	sampledResponseID := strings.TrimSpace(r.sampledToolCalls[key][itemID])

	if base.CellID == nil && cellID != "" {
		base.CellID = &cellID
	}
	if !cellKnown {
		base.CellID = nil
		base.ParentCallID = nil
	} else if cell.ParentCallID != "" && cell.ParentCallID != itemID {
		parentCallID := cell.ParentCallID
		base.ParentCallID = &parentCallID
	} else {
		base.ParentCallID = nil
	}
	responseID := firstNonEmpty(sampledResponseID, cell.OriginatingResponseID)
	if responseID != "" {
		base.OriginatingResponseID = &responseID
	}
}

// forgetThreadToolEvidence drops a closing thread's Code Mode cells and per-turn
// evidence, mirroring Rust's ThreadClosed handling.
func (r *RuntimeRouter) forgetThreadToolEvidence(threadID string) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	prefix := threadID + "\x00"
	r.sampledToolCallsMu.Lock()
	defer r.sampledToolCallsMu.Unlock()
	delete(r.codeModeCells, threadID)
	for key := range r.sampledToolCalls {
		if strings.HasPrefix(key, prefix) {
			delete(r.sampledToolCalls, key)
		}
	}
	for key := range r.codeModeChildCalls {
		if strings.HasPrefix(key, prefix) {
			delete(r.codeModeChildCalls, key)
		}
	}
	for key := range r.pendingToolEvents {
		if strings.HasPrefix(key, prefix) {
			delete(r.pendingToolEvents, key)
		}
	}
}
