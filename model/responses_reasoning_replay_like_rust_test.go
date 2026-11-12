package model

import (
	"context"
	"strings"
	"testing"
)

// Rust #61017: a completed response reports the replayed reasoning items the
// API removed, counted by bounded reason.
func TestResponsesStreamReportsReasoningRemovalsLikeRust(t *testing.T) {
	var completed *ResponsesStreamEvent
	_, err := parseResponsesStream(
		context.Background(),
		strings.NewReader(responsesSSE(
			`{"type":"response.created","response":{"id":"resp-1"}}`,
			`{"type":"response.completed","response":{"id":"resp-1","output":[{"id":"msg-1","type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"input_transformations":[{"type":"reasoning","action":"removed","reason":"prefix_mismatch"},{"type":"reasoning","action":"removed","reason":"turn_boundary"},{"type":"reasoning","action":"removed","reason":"not_a_bounded_reason"},{"type":"message","action":"removed","reason":"prefix_mismatch"},{"type":"reasoning","action":"kept","reason":"prefix_mismatch"}]}}`,
		)),
		&AgentRequest{Prompt: "hello", Model: "gpt-test"},
		"openai",
		func(event *ResponsesStreamEvent) {
			if event.Kind == ResponsesStreamEventCompleted {
				completed = event
			}
		},
	)
	if err != nil {
		t.Fatalf("parseResponsesStream() error = %v", err)
	}
	if completed == nil || completed.ReasoningRemovals == nil {
		t.Fatal("completed event carried no reasoning-removal report")
	}
	want := map[ReasoningRemovalReason]int64{
		ReasoningRemovalPrefixMismatch: 1,
		ReasoningRemovalTurnBoundary:   1,
		ReasoningRemovalOther:          1,
	}
	if len(completed.ReasoningRemovals.Counts) != len(want) {
		t.Fatalf("counts = %#v, want %#v", completed.ReasoningRemovals.Counts, want)
	}
	for reason, count := range want {
		if completed.ReasoningRemovals.Counts[reason] != count {
			t.Fatalf("counts = %#v, want %#v", completed.ReasoningRemovals.Counts, want)
		}
	}
	if !completed.ReasoningRemovals.HasPrefixMismatch() {
		t.Fatal("HasPrefixMismatch() = false, want true")
	}
}

// Rust #61017: only an array report counts as reported; a missing, null, or
// malformed report stays unavailable instead of failing the response.
func TestResponsesStreamReasoningRemovalsAvailabilityLikeRust(t *testing.T) {
	cases := []struct {
		name string
		data string
		want bool
	}{
		{name: "missing", data: `{"type":"response.completed","response":{"id":"r"}}`, want: false},
		{name: "null", data: `{"type":"response.completed","response":{"id":"r","input_transformations":null}}`, want: false},
		{name: "object", data: `{"type":"response.completed","response":{"id":"r","input_transformations":{"type":"reasoning"}}}`, want: false},
		{name: "malformed entry", data: `{"type":"response.completed","response":{"id":"r","input_transformations":[{"type":7}]}}`, want: false},
		{name: "empty array", data: `{"type":"response.completed","response":{"id":"r","input_transformations":[]}}`, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report := reasoningRemovalsFromStreamEventData([]byte(tc.data))
			if tc.want {
				if report == nil {
					t.Fatal("report = nil, want an available empty report")
				}
				if len(report.Counts) != 0 {
					t.Fatalf("counts = %#v, want none", report.Counts)
				}
				return
			}
			if report != nil {
				t.Fatalf("report = %#v, want unavailable", report)
			}
		})
	}
}
