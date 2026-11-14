package appserver

import (
	"testing"

	"codex_go/turn"
)

func TestTurnDictationMetadataCaptureLikeRust(t *testing.T) {
	replacement := map[string]string{
		usedDictationMetadataKey: "true",
		"source":                 "codex",
	}
	patch := map[string]string{usedDictationMetadataKey: "false"}
	params := &turn.TurnStartParams{Prompt: "hello", ResponsesAPIMetadata: replacement, ResponsesAPIMetadataPatch: patch}
	captureTurnStartDictation(params)
	if params.UsedDictation == nil || *params.UsedDictation {
		t.Fatalf("used dictation = %#v, want false from patch precedence", params.UsedDictation)
	}
	if _, ok := params.ResponsesAPIMetadata[usedDictationMetadataKey]; ok {
		t.Fatalf("replacement retained dictation marker: %#v", params.ResponsesAPIMetadata)
	}
	if _, ok := params.ResponsesAPIMetadataPatch[usedDictationMetadataKey]; ok {
		t.Fatalf("patch retained dictation marker: %#v", params.ResponsesAPIMetadataPatch)
	}
	if got := params.ResponsesAPIMetadata["source"]; got != "codex" {
		t.Fatalf("source metadata changed: %q", got)
	}
}

func TestTurnDictationRejectsNonRustBoolSpellingsLikeRust(t *testing.T) {
	for _, raw := range []string{"True", "1", "yes", " false "} {
		t.Run(raw, func(t *testing.T) {
			params := &turn.TurnStartParams{Prompt: "hello", ResponsesAPIMetadata: map[string]string{usedDictationMetadataKey: raw}}
			captureTurnStartDictation(params)
			if params.UsedDictation != nil {
				t.Fatalf("used dictation = %#v, want unknown", params.UsedDictation)
			}
			if _, ok := params.ResponsesAPIMetadata[usedDictationMetadataKey]; ok {
				t.Fatal("malformed marker must still be removed from Responses metadata")
			}
		})
	}
}

func TestTurnDictationAggregationLikeRust(t *testing.T) {
	trueValue, falseValue := true, false
	for _, tc := range []struct {
		name   string
		inputs []*bool
		want   *bool
	}{
		{name: "single false", inputs: []*bool{&falseValue}, want: &falseValue},
		{name: "false then unknown", inputs: []*bool{&falseValue, nil}},
		{name: "unknown then true", inputs: []*bool{nil, &trueValue, &falseValue}, want: &trueValue},
		{name: "true dominates", inputs: []*bool{&trueValue, nil}, want: &trueValue},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := turnDictationNoUserInput
			for _, input := range tc.inputs {
				state = mergeTurnDictation(state, input)
			}
			got := state.analyticsValue()
			if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
				t.Fatalf("analytics value = %#v, want %#v", got, tc.want)
			}
		})
	}
}
