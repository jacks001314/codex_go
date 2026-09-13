package appserver

import (
	"strings"
	"time"

	"codex_go/model"
	"codex_go/telemetry"
)

// Rust parity: codex-rs/core/src/turn_timing.rs. The turn's
// time-to-first-token and time-to-first-message durations are recorded once per
// turn, measured from the turn start.

// recordTurnTimingMetrics mirrors record_turn_ttft_metric /
// record_turn_ttfm_metric for one observed stream event.
func (r *RuntimeRouter) recordTurnTimingMetrics(state *responsesStreamNotificationState, event *model.ResponsesStreamEvent) {
	if r == nil || state == nil || event == nil || state.metrics == nil {
		return
	}
	if state.ttftRecorded && state.ttfmRecorded {
		return
	}
	elapsedMS := time.Now().UTC().UnixMilli() - state.turnStartedAtMS
	if elapsedMS < 0 {
		elapsedMS = 0
	}
	elapsed := time.Duration(elapsedMS) * time.Millisecond
	if !state.ttftRecorded && streamEventRecordsTurnTTFT(event) {
		state.ttftRecorded = true
		state.metrics.RecordDuration(telemetry.TurnTTFTDurationMetric, elapsed, nil)
	}
	if !state.ttfmRecorded && streamEventRecordsTurnTTFM(event) {
		state.ttfmRecorded = true
		state.metrics.RecordDuration(telemetry.TurnTTFMDurationMetric, elapsed, nil)
	}
}

// streamEventRecordsTurnTTFT mirrors response_event_records_turn_ttft: the
// first text/reasoning delta or a qualifying output item.
func streamEventRecordsTurnTTFT(event *model.ResponsesStreamEvent) bool {
	switch event.Kind {
	case model.ResponsesStreamEventOutputText,
		model.ResponsesStreamEventReasoningSummaryTextDelta,
		model.ResponsesStreamEventReasoningTextDelta:
		return true
	case model.ResponsesStreamEventOutputAdded, model.ResponsesStreamEventOutputDone:
		return streamItemRecordsTurnTTFT(event.Item)
	}
	return false
}

// streamEventRecordsTurnTTFM mirrors record_turn_ttfm_for_turn_item: the first
// agent message item.
func streamEventRecordsTurnTTFM(event *model.ResponsesStreamEvent) bool {
	switch event.Kind {
	case model.ResponsesStreamEventOutputAdded, model.ResponsesStreamEventOutputDone:
		return event.Item != nil && event.Item.Type == "agent_message" && strings.TrimSpace(event.Item.Text) != ""
	}
	return false
}

// streamItemRecordsTurnTTFT mirrors response_item_records_turn_ttft for Go's
// agent item types: an assistant message with text, reasoning with text, or a
// tool-call/compaction item counts; outputs and configuration updates do not.
func streamItemRecordsTurnTTFT(item *model.AgentItem) bool {
	if item == nil {
		return false
	}
	switch item.Type {
	case "agent_message":
		return strings.TrimSpace(item.Text) != ""
	case "reasoning":
		return strings.TrimSpace(item.Text) != "" || agentItemReasoningHasText(item.Data)
	case "function_call", "custom_tool_call", "local_shell_call", "web_search_call",
		"image_generation_call", "compaction", "context_compaction":
		return true
	}
	return false
}

// agentItemReasoningHasText reports whether a reasoning item carries any
// non-empty summary or content text.
func agentItemReasoningHasText(data map[string]any) bool {
	for _, key := range []string{"summary", "content", "reasoningContent", "reasoning_content"} {
		if anyValueHasText(data[key]) {
			return true
		}
	}
	return false
}

func anyValueHasText(value any) bool {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed) != ""
	case []any:
		for _, entry := range typed {
			if anyValueHasText(entry) {
				return true
			}
		}
	case map[string]any:
		for _, entry := range typed {
			if anyValueHasText(entry) {
				return true
			}
		}
	}
	return false
}
