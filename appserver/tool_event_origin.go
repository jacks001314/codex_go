package appserver

import (
	"strings"

	"codex_go/model"
	"codex_go/telemetry"
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
		return
	}
	key := sampledToolCallsKey(threadID, turnID)
	if strings.TrimSpace(key) == "\x00" {
		return
	}
	r.sampledToolCallsMu.Lock()
	defer r.sampledToolCallsMu.Unlock()
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
func (r *RuntimeRouter) rememberCodeModeChildCall(threadID string, cellID string, callID string) {
	if r == nil {
		return
	}
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return
	}
	turnID := ""
	if active := r.threads.ActiveTurn(strings.TrimSpace(threadID)); active != nil {
		turnID = strings.TrimSpace(active.TurnID)
	}
	if turnID == "" {
		return
	}
	key := sampledToolCallsKey(threadID, turnID)
	r.sampledToolCallsMu.Lock()
	defer r.sampledToolCallsMu.Unlock()
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
	calls[callID] = strings.TrimSpace(cellID)
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
