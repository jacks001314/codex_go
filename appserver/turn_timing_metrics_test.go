package appserver

import (
	"testing"
	"time"

	"codex_go/model"
	"codex_go/state"
	"codex_go/telemetry"
)

// Mirrors codex-rs/core/src/turn_timing.rs: the first qualifying stream event
// records time-to-first-token and the first agent message records
// time-to-first-message, once per turn.
func TestRecordTurnTimingMetricsLikeRust(t *testing.T) {
	metrics := state.NewTaskMetrics()
	router := NewRuntimeRouter(RuntimeServices{TurnMetrics: metrics})
	streamState := &responsesStreamNotificationState{
		metrics:         metrics,
		turnStartedAtMS: time.Now().Add(-50 * time.Millisecond).UnixMilli(),
	}

	// A reasoning-summary delta records TTFT; a later text delta must not record
	// it again.
	router.recordTurnTimingMetrics(streamState, &model.ResponsesStreamEvent{Kind: model.ResponsesStreamEventReasoningSummaryTextDelta})
	router.recordTurnTimingMetrics(streamState, &model.ResponsesStreamEvent{Kind: model.ResponsesStreamEventOutputText})
	// The first agent message records TTFM.
	router.recordTurnTimingMetrics(streamState, &model.ResponsesStreamEvent{
		Kind: model.ResponsesStreamEventOutputDone,
		Item: &model.AgentItem{Type: "agent_message", Text: "hi"},
	})

	records := metrics.Records()
	if len(records) != 2 {
		t.Fatalf("records = %#v", records)
	}
	if records[0].Name != telemetry.TurnTTFTDurationMetric || records[0].Kind != "duration" || records[0].DurationMS < 0 {
		t.Fatalf("ttft record = %#v", records[0])
	}
	if records[1].Name != telemetry.TurnTTFMDurationMetric || records[1].Kind != "duration" || records[1].DurationMS < 0 {
		t.Fatalf("ttfm record = %#v", records[1])
	}

	// No sink records nothing.
	(&RuntimeRouter{}).recordTurnTimingMetrics(streamState, &model.ResponsesStreamEvent{Kind: model.ResponsesStreamEventOutputText})
}

// The TTFT event conditions mirror response_event_records_turn_ttft.
func TestStreamEventRecordsTurnTTFTLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		event *model.ResponsesStreamEvent
		want  bool
	}{
		{"text delta", &model.ResponsesStreamEvent{Kind: model.ResponsesStreamEventOutputText}, true},
		{"reasoning summary delta", &model.ResponsesStreamEvent{Kind: model.ResponsesStreamEventReasoningSummaryTextDelta}, true},
		{"reasoning text delta", &model.ResponsesStreamEvent{Kind: model.ResponsesStreamEventReasoningTextDelta}, true},
		{"summary part added", &model.ResponsesStreamEvent{Kind: model.ResponsesStreamEventReasoningSummaryPartAdded}, false},
		{"created", &model.ResponsesStreamEvent{Kind: model.ResponsesStreamEventCreated}, false},
		{"server reasoning included", &model.ResponsesStreamEvent{Kind: model.ResponsesStreamEventReasoning}, false},
		{"agent message with text", &model.ResponsesStreamEvent{Kind: model.ResponsesStreamEventOutputDone, Item: &model.AgentItem{Type: "agent_message", Text: "hi"}}, true},
		{"agent message without text", &model.ResponsesStreamEvent{Kind: model.ResponsesStreamEventOutputDone, Item: &model.AgentItem{Type: "agent_message"}}, false},
		{"reasoning with summary text", &model.ResponsesStreamEvent{
			Kind: model.ResponsesStreamEventOutputDone,
			Item: &model.AgentItem{Type: "reasoning", Data: map[string]any{"summary": []any{map[string]any{"text": "thinking"}}}},
		}, true},
		{"reasoning without text", &model.ResponsesStreamEvent{Kind: model.ResponsesStreamEventOutputDone, Item: &model.AgentItem{Type: "reasoning"}}, false},
		{"function call", &model.ResponsesStreamEvent{Kind: model.ResponsesStreamEventOutputDone, Item: &model.AgentItem{Type: "function_call"}}, true},
		{"function call output", &model.ResponsesStreamEvent{Kind: model.ResponsesStreamEventOutputDone, Item: &model.AgentItem{Type: "function_call_output"}}, false},
	} {
		if got := streamEventRecordsTurnTTFT(testCase.event); got != testCase.want {
			t.Fatalf("%s: streamEventRecordsTurnTTFT = %v, want %v", testCase.name, got, testCase.want)
		}
	}
}

// Only an agent message records time-to-first-message.
func TestStreamEventRecordsTurnTTFMLikeRust(t *testing.T) {
	if !streamEventRecordsTurnTTFM(&model.ResponsesStreamEvent{
		Kind: model.ResponsesStreamEventOutputAdded,
		Item: &model.AgentItem{Type: "agent_message", Text: "hi"},
	}) {
		t.Fatal("an agent message did not record TTFM")
	}
	for _, event := range []*model.ResponsesStreamEvent{
		{Kind: model.ResponsesStreamEventOutputAdded, Item: &model.AgentItem{Type: "reasoning", Text: "thinking"}},
		{Kind: model.ResponsesStreamEventOutputAdded, Item: &model.AgentItem{Type: "function_call"}},
		{Kind: model.ResponsesStreamEventOutputText},
	} {
		if streamEventRecordsTurnTTFM(event) {
			t.Fatalf("event %#v recorded TTFM", event)
		}
	}
}
