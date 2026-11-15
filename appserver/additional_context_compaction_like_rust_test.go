package appserver

import (
	"strings"
	"testing"

	"codex_go/compact"
	"codex_go/session"
	"codex_go/turn"
)

const additionalContextCompactionThread = "123e4567-e89b-42d3-a456-426614174190"

func emitAdditionalContextForCompactionTest(t *testing.T, router *RuntimeRouter, toolOutputs bool, value string) []session.Item {
	t.Helper()
	_, _, persisted := router.mergeAdditionalContext(additionalContextCompactionThread, map[string]turn.AdditionalContextEntry{
		"browser_info": {Kind: turn.AdditionalContextUntrusted, Value: value},
	}, toolOutputs)
	items := additionalContextSessionItems("turn-1", persisted, fixedTime())
	return items
}

func additionalContextHistoryItem(id string, text string) compact.Item {
	return compact.Item{ID: id, Type: "message", Role: "user", Text: text, Kind: "user_message"}
}

// TestAdditionalContextRestoredAfterCompactionLikeRust covers Rust #61390: an
// active client context item dropped by the replaced prefix is reinstalled
// before the preserved user prompt, and an unchanged value keeps its original
// response-item identity across deduplication.
func TestAdditionalContextRestoredAfterCompactionLikeRust(t *testing.T) {
	for _, toolOutputs := range []bool{false, true} {
		name := "messages"
		if toolOutputs {
			name = "tool_outputs"
		}
		t.Run(name, func(t *testing.T) {
			router := &RuntimeRouter{}
			contextItems := emitAdditionalContextForCompactionTest(t, router, toolOutputs, "tab one")
			if len(contextItems) != 1 {
				t.Fatalf("context session items = %#v", contextItems)
			}
			contextID := contextItems[0].ID
			history := append(compactItemsFromSessionItems([]session.Item{
				{ID: "prompt-1", Type: "message", Role: "user", Text: "first prompt"},
			}), compactItemsFromSessionItems(contextItems)...)
			history = append(history, additionalContextHistoryItem("prompt-2", "second prompt"))

			// An unchanged value is not re-emitted, but its item ID survives
			// deduplication so compaction can still find it.
			if repeated := emitAdditionalContextForCompactionTest(t, router, toolOutputs, "tab one"); len(repeated) != 0 {
				t.Fatalf("unchanged context re-emitted: %#v", repeated)
			}

			replacement := compact.BuildCompactedHistory(nil, []compact.Item{history[2]}, "summary")
			restored, replay := router.restoreCompactedAdditionalContext(additionalContextCompactionThread, history, nil, replacement)
			if len(replay) != 0 {
				t.Fatalf("unexpected replay keys = %#v", replay)
			}
			if len(restored) != 1 || restored[0].ID != contextID || !strings.Contains(restored[0].Text, "tab one") {
				t.Fatalf("restored = %#v", restored)
			}

			installed := compact.InsertInitialContextBeforeLastUserOrSummary(replacement, restored)
			contextAt, promptAt, count := -1, -1, 0
			for index := range installed {
				if installed[index].ID == contextID {
					count++
					contextAt = index
				}
				if installed[index].ID == "prompt-2" {
					promptAt = index
				}
			}
			if count != 1 {
				t.Fatalf("context appears %d times in installed history", count)
			}
			if contextAt < 0 || promptAt < 0 || contextAt > promptAt {
				t.Fatalf("restored context at %d, preserved prompt at %d", contextAt, promptAt)
			}

			// A newer value that arrived after the captured prefix is left to
			// its own history item; the stale value is not restored again.
			updated := emitAdditionalContextForCompactionTest(t, router, toolOutputs, "tab two")
			if len(updated) != 1 {
				t.Fatalf("updated context session items = %#v", updated)
			}
			nextReplacement := compact.BuildCompactedHistory(nil, []compact.Item{additionalContextHistoryItem("prompt-3", "third prompt")}, "next summary")
			restoredAgain, _ := router.restoreCompactedAdditionalContext(
				additionalContextCompactionThread,
				history,
				compactItemsFromSessionItems(updated),
				nextReplacement,
			)
			if len(restoredAgain) != 0 {
				t.Fatalf("stale or queued context restored: %#v", restoredAgain)
			}
		})
	}
}

// TestAdditionalContextInstructionFragmentsReplayAfterCompactionLikeRust
// covers the instruction-delivered form: it never enters history, so the
// deduplication marker is dropped and the next turn emits it again.
func TestAdditionalContextInstructionFragmentsReplayAfterCompactionLikeRust(t *testing.T) {
	router := &RuntimeRouter{}
	developer, _, persisted := router.mergeAdditionalContext(additionalContextCompactionThread, map[string]turn.AdditionalContextEntry{
		"goal": {Kind: turn.AdditionalContextApplication, Value: "finish the migration"},
	}, false)
	if len(developer) != 1 || len(persisted) != 0 {
		t.Fatalf("developer = %#v, persisted = %#v", developer, persisted)
	}
	restored, replay := router.restoreCompactedAdditionalContext(additionalContextCompactionThread, nil, nil, nil)
	if len(restored) != 0 {
		t.Fatalf("instruction fragment restored into history: %#v", restored)
	}
	if len(replay) != 1 || replay[0] != "goal" {
		t.Fatalf("replay keys = %#v", replay)
	}
	router.forgetAdditionalContextKeys(additionalContextCompactionThread, replay)
	developerAgain, _, _ := router.mergeAdditionalContext(additionalContextCompactionThread, map[string]turn.AdditionalContextEntry{
		"goal": {Kind: turn.AdditionalContextApplication, Value: "finish the migration"},
	}, false)
	if len(developerAgain) != 1 || !strings.Contains(developerAgain[0], "finish the migration") {
		t.Fatalf("instruction fragment not re-emitted: %#v", developerAgain)
	}
}
