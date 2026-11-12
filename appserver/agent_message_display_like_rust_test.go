package appserver

import (
	"reflect"
	"testing"
)

// TestAgentMessageDisplayVersionedContractLikeRust mirrors Rust #60904: the
// completed answer carries the narrowed moderation contract, version 1 display
// instructions survive verbatim, and anything the harness cannot read leaves
// the answer restricted.
func TestAgentMessageDisplayVersionedContractLikeRust(t *testing.T) {
	display := map[string]any{
		"version":         1,
		"mode":            "show",
		"trustedContacts": "skip",
		"annotation":      map[string]any{"kind": "wellbeingHelpline", "subject": "answer"},
	}
	metadata := map[string]any{
		"final_answer_moderation": map[string]any{
			"item_id":   "msg-42",
			"moderated": true,
			"display":   display,
		},
		"generation": map[string]any{"private": "classifier details"},
	}
	want := map[string]any{"moderated": true, "display": display}
	if got := normalizeFinalAnswerModeration(metadata); !reflect.DeepEqual(got, want) {
		t.Fatalf("normalized moderation = %#v, want %#v", got, want)
	}

	// An unsupported version or the pre-versioning action payload is
	// unreadable, so the answer stays restricted and the display is dropped.
	for _, bad := range []any{
		map[string]any{"version": 2, "mode": "show", "trustedContacts": "skip",
			"annotation": map[string]any{"kind": "wellbeingHelpline", "subject": "answer"}},
		map[string]any{"action": "answerHelpline"},
		map[string]any{"version": 1, "mode": "hide"},
	} {
		got := normalizeFinalAnswerModeration(map[string]any{
			"final_answer_moderation": map[string]any{"moderated": false, "display": bad},
		})
		if !reflect.DeepEqual(got, map[string]any{"moderated": true}) {
			t.Fatalf("moderation for %#v = %#v, want restricted", bad, got)
		}
	}

	// A verdict without a display stays as reported.
	if got := normalizeFinalAnswerModeration(map[string]any{
		"final_answer_moderation": map[string]any{"moderated": false},
	}); !reflect.DeepEqual(got, map[string]any{"moderated": false}) {
		t.Fatalf("display-less moderation = %#v", got)
	}
}

// TestAgentMessageDisplayBlockAndHideLikeRust covers the remaining two modes.
func TestAgentMessageDisplayBlockAndHideLikeRust(t *testing.T) {
	cases := []map[string]any{
		{"version": 1, "mode": "hide", "replacement": map[string]any{"kind": "wellbeingPromptHelpline"}},
		{"version": 1, "mode": "block", "reason": map[string]any{"kind": "protection", "protectionType": "self_harm"}},
		{"version": 1, "mode": "block", "reason": map[string]any{"kind": "cyber", "trustedAccess": true}},
	}
	for _, display := range cases {
		normalized, ok := normalizeAgentMessageDisplay(display)
		if !ok {
			t.Fatalf("display %#v rejected", display)
		}
		if !reflect.DeepEqual(normalized, display) {
			t.Fatalf("normalized %#v = %#v", display, normalized)
		}
	}
}
