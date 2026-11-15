package utils

import (
	"reflect"
	"testing"
)

// TestMeasuredToolOutputKeepsChargesPairedWithRetainedClipsLikeRust mirrors
// Rust #61394 `measured_tool_output_keeps_charges_paired_with_retained_clips`:
// the premeasured audio charge travels with the retained clip instead of being
// recomputed from the truncated output.
func TestMeasuredToolOutputKeepsChargesPairedWithRetainedClipsLikeRust(t *testing.T) {
	clip := FunctionCallOutputContentItem{Kind: ContentInputAudio, AudioURL: "clip"}
	text := FunctionCallOutputContentItem{Kind: ContentInputText, Text: "keep"}
	omitted := FunctionCallOutputContentItem{Kind: ContentInputText, Text: "[omitted 1 audio items ...]"}

	output := TruncateMeasuredFunctionOutputItems([]MeasuredFunctionCallOutputContentItem{
		{Item: clip, AudioTokens: 9},
		{Item: text, AudioTokens: 0},
		{Item: clip, AudioTokens: 2},
		{Item: clip, AudioTokens: 1},
	}, TokensPolicy(4))

	want := []MeasuredFunctionCallOutputContentItem{
		{Item: text, AudioTokens: 0},
		{Item: clip, AudioTokens: 2},
		{Item: clip, AudioTokens: 1},
		{Item: omitted, AudioTokens: 0},
	}
	if !reflect.DeepEqual(output, want) {
		t.Fatalf("measured truncation = %#v, want %#v", output, want)
	}
}
