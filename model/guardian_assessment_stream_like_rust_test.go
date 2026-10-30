package model

import (
	"encoding/json"
	"testing"
)

// TestGuardianAssessmentFromResponseMetadataLikeRust covers Rust #57757: a
// `response.metadata` event may carry action-bound Guardian evidence outside
// model-visible items, and a missing or malformed assessment stays unavailable.
func TestGuardianAssessmentFromResponseMetadataLikeRust(t *testing.T) {
	valid := []byte(`{"type":"response.metadata","response_id":"resp-1","metadata":{"guardian":{"item_id":"call-1","requires_review":false}}}`)
	assessment := guardianAssessmentFromStreamMetadata(valid)
	if assessment == nil {
		t.Fatal("missing assessment")
	}
	if assessment.ResponseID != "resp-1" || assessment.ItemID != "call-1" || assessment.RequiresReview {
		t.Fatalf("assessment = %#v", assessment)
	}
	for _, invalid := range [][]byte{
		[]byte(`{"type":"response.metadata","metadata":{"guardian":{"item_id":"call-1","requires_review":false}}}`),
		[]byte(`{"type":"response.metadata","response_id":"","metadata":{"guardian":{"item_id":"call-1","requires_review":false}}}`),
		[]byte(`{"type":"response.metadata","response_id":"resp-1","metadata":{"guardian":{"item_id":"","requires_review":false}}}`),
		[]byte(`{"type":"response.metadata","response_id":"resp-1","metadata":{"guardian":{"item_id":"call-1"}}}`),
		[]byte(`{"type":"response.metadata","response_id":"resp-1","metadata":{"guardian":{"item_id":"call-1","requires_review":"no"}}}`),
		[]byte(`{"type":"response.metadata","response_id":"resp-1","metadata":{}}`),
	} {
		if got := guardianAssessmentFromStreamMetadata(invalid); got != nil {
			t.Fatalf("assessment = %#v for %s", got, invalid)
		}
	}
}

// TestResponsesStreamEmitsGuardianAssessmentLikeRust checks the accumulator
// publishes the assessment as its own ordered stream event with a telemetry
// name, without adding it to conversation history.
func TestResponsesStreamEmitsGuardianAssessmentLikeRust(t *testing.T) {
	events := []*ResponsesStreamEvent{}
	handler := func(event *ResponsesStreamEvent) {
		events = append(events, event)
	}
	accumulator := newResponsesStreamAccumulator(&AgentRequest{})
	_, err := accumulator.apply(&responsesSSEEvent{
		Event: "response.metadata",
		Data:  json.RawMessage(`{"type":"response.metadata","response_id":"resp-1","metadata":{"guardian":{"item_id":"call-1","requires_review":true}}}`),
	}, handler)
	if err != nil {
		t.Fatalf("apply() error = %v", err)
	}
	var found *ResponsesStreamEvent
	for _, event := range events {
		if event.Kind == ResponsesStreamEventGuardianAssessment {
			found = event
		}
	}
	if found == nil || found.GuardianAssessment == nil {
		t.Fatalf("events = %#v", events)
	}
	if !found.GuardianAssessment.RequiresReview || found.GuardianAssessment.ResponseID != "resp-1" {
		t.Fatalf("assessment = %#v", found.GuardianAssessment)
	}
	if got := responsesTypeName(found); got != "guardian_assessment" {
		t.Fatalf("telemetry name = %q", got)
	}
	if len(accumulator.items) != 0 {
		t.Fatalf("assessment entered history items: %#v", accumulator.items)
	}
}
