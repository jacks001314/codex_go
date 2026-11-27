package appserver

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestPromptWellbeingResultsSurviveAnswerModerationLikeRust(t *testing.T) {
	display := map[string]any{
		"version":         1,
		"mode":            "show",
		"trustedContacts": "skip",
		"annotation":      map[string]any{"kind": "wellbeingHelpline", "subject": "answer"},
	}
	results := []any{map[string]any{
		"inputItemId": "item-1",
		"scores":      map[string]any{"self_harm": 0.25},
		"labels":      []any{"wellbeing", "helpline"},
	}}
	metadata := map[string]any{"final_answer_moderation": map[string]any{
		"moderated":              false,
		"display":                display,
		"promptWellbeingResults": results,
	}}
	want := map[string]any{
		"moderated":              false,
		"display":                display,
		"promptWellbeingResults": results,
	}
	if got := normalizeFinalAnswerModeration(metadata); !reflect.DeepEqual(got, want) {
		t.Fatalf("normalized moderation = %#v, want %#v", got, want)
	}

	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !json.Valid(encoded) {
		t.Fatalf("moderation JSON = %q", encoded)
	}
}

func TestPromptWellbeingResultsDoNotOverrideModerationLikeRust(t *testing.T) {
	valid := []any{map[string]any{
		"inputItemId": "item-1",
		"scores":      map[string]any{"score": json.Number("1.5")},
		"labels":      []string{"label"},
	}}
	for _, malformed := range []any{
		"not-an-array",
		[]any{map[string]any{"inputItemId": "missing-fields"}},
		[]any{map[string]any{
			"inputItemId": "bad-score",
			"scores":      map[string]any{"score": "high"},
			"labels":      []any{"label"},
		}},
	} {
		got := normalizeFinalAnswerModeration(map[string]any{
			"final_answer_moderation": map[string]any{
				"moderated":              false,
				"promptWellbeingResults": malformed,
			},
		})
		want := map[string]any{"moderated": false, "display": nil}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("moderation for %#v = %#v, want %#v", malformed, got, want)
		}
	}

	got := normalizeFinalAnswerModeration(map[string]any{
		"final_answer_moderation": map[string]any{
			"moderated":              false,
			"display":                map[string]any{"action": "legacy"},
			"promptWellbeingResults": valid,
		},
	})
	want := map[string]any{
		"moderated": true,
		"display":   nil,
		"promptWellbeingResults": []any{map[string]any{
			"inputItemId": "item-1",
			"scores":      map[string]any{"score": json.Number("1.5")},
			"labels":      []any{"label"},
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("invalid display moderation = %#v, want %#v", got, want)
	}
}

func TestRestoredPromptWellbeingResultsLikeRust(t *testing.T) {
	results := []any{map[string]any{
		"inputItemId": "item-1",
		"scores":      map[string]any{"score": float64(0.5)},
		"labels":      []any{"label"},
	}}
	stored := map[string]any{
		"moderated":              false,
		"display":                nil,
		"promptWellbeingResults": results,
	}
	want := map[string]any{
		"moderated":              false,
		"display":                nil,
		"promptWellbeingResults": results,
	}
	if got := restoredModeration(stored); !reflect.DeepEqual(got, want) {
		t.Fatalf("restored moderation = %#v, want %#v", got, want)
	}

	malformed := map[string]any{
		"moderated":              false,
		"display":                nil,
		"promptWellbeingResults": []any{map[string]any{"inputItemId": "item-1"}},
	}
	if got := restoredModeration(malformed); !reflect.DeepEqual(got, map[string]any{"moderated": false, "display": nil}) {
		t.Fatalf("malformed restored moderation = %#v", got)
	}
}
