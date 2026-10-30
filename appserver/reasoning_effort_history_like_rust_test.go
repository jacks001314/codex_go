package appserver

import (
	"testing"

	"codex_go/model"
	"codex_go/turn"
)

func reasoningEffortHistoryUserMessage(text string) map[string]any {
	return map[string]any{
		"type":    "message",
		"role":    "user",
		"content": []any{map[string]any{"type": "input_text", "text": text}},
	}
}

func reasoningEffortFromUpdate(t *testing.T, item any) string {
	t.Helper()
	payload, ok := item.(map[string]any)
	if !ok {
		t.Fatalf("configuration_update item = %#v", item)
	}
	reasoning, _ := payload["reasoning"].(map[string]any)
	effort, _ := reasoning["effort"].(string)
	if effort == "" {
		t.Fatalf("configuration_update carries no effort: %#v", payload)
	}
	return effort
}

// Mirrors Rust #43110's prefix test: history updates append only after accepted
// input, an unchanged selection appends nothing, and the request baseline stays
// on the first pinned effort while configuration updates change.
func TestReasoningEffortOverrideAppendsOnlyOnChangeLikeRust(t *testing.T) {
	router := &RuntimeRouter{}
	const threadID = "thread-effort-append"
	const modelSlug = "gpt-5"

	var history []any
	var requests []string
	turn := func(selection string) {
		history = append(history, reasoningEffortHistoryUserMessage("message "+selection))
		items := router.reasoningEffortOverrideInputItems(threadID, modelSlug, selection, true, history)
		history = append(history, items...)
		requests = append(requests, router.reasoningEffortForRequest(
			threadID, modelSlug, selection, true, selection, true, requestEffortSampling,
		))
	}
	turn("medium")
	turn("high")
	turn("high")
	turn("low")

	for i, effort := range requests {
		if effort != "medium" {
			t.Fatalf("turn %d request effort = %q, want the pinned medium", i+1, effort)
		}
	}
	// user, update(medium), user, update(high), user, user, update(low)
	if len(history) != 7 {
		t.Fatalf("history length = %d, want 7 (%#v)", len(history), history)
	}
	wantUpdates := map[int]string{1: "medium", 3: "high", 6: "low"}
	for i := range history {
		want, isUpdate := wantUpdates[i]
		got := isConfigurationUpdateInputItem(history[i])
		if got != isUpdate {
			t.Fatalf("history[%d] update = %v, want %v", i, got, isUpdate)
		}
		if !isUpdate {
			continue
		}
		if effort := reasoningEffortFromUpdate(t, history[i]); effort != want {
			t.Fatalf("history[%d] effort = %q, want %q", i, effort, want)
		}
	}
}

// Mirrors Rust #43110's ultra normalization case: the model resolves ultra to
// its supported effort, and an explicit equivalent selection must not append a
// second update.
func TestReasoningEffortOverrideNormalizesUltraBeforeComparingLikeRust(t *testing.T) {
	router := &RuntimeRouter{}
	const threadID = "thread-effort-ultra"
	const modelSlug = "gpt-5.4"
	info := &model.ModelInfo{
		Slug: modelSlug, SupportsReasoningEffortUpdates: true,
		MultiAgentReasoningEffort: stringPtr("high"),
		SupportedReasoningLevels:  []string{"low", "medium", "high"},
		DefaultReasoningLevel:     "medium",
	}
	params := &turn.TurnStartParams{ThreadID: threadID}

	ultraEffort, ok := router.effortForConfigurationUpdate(threadID, reasoningEffortTestConfig("ultra", true), params, info, "openai")
	if !ok || ultraEffort != "high" {
		t.Fatalf("ultra resolution = %q,%v, want high,true", ultraEffort, ok)
	}
	items := router.reasoningEffortOverrideInputItems(threadID, modelSlug, ultraEffort, true, nil)
	if len(items) != 1 {
		t.Fatalf("ultra update items = %#v, want one update", items)
	}
	if got := router.reasoningEffortForRequest(threadID, modelSlug, "ultra", true, ultraEffort, true, requestEffortSampling); got != "high" {
		t.Fatalf("ultra request effort = %q, want high", got)
	}

	history := []any{items[0], reasoningEffortHistoryUserMessage("equivalent explicit effort")}
	explicitEffort, ok := router.effortForConfigurationUpdate(threadID, reasoningEffortTestConfig("high", true), params, info, "openai")
	if !ok || explicitEffort != "high" {
		t.Fatalf("explicit resolution = %q,%v, want high,true", explicitEffort, ok)
	}
	if extra := router.reasoningEffortOverrideInputItems(threadID, modelSlug, explicitEffort, true, history); len(extra) != 0 {
		t.Fatalf("equivalent explicit effort appended %#v, want none", extra)
	}
	if got := router.reasoningEffortForRequest(threadID, modelSlug, "high", true, explicitEffort, true, requestEffortSampling); got != "high" {
		t.Fatalf("explicit request effort = %q, want high", got)
	}
}

// Mirrors Rust #43110's persistent transitions: persistent resolves to the
// request-level disabled effort, each real transition appends one trusted
// update, and repeating the same selection appends nothing.
func TestReasoningEffortOverridePersistentTransitionsLikeRust(t *testing.T) {
	router := &RuntimeRouter{}
	const threadID = "thread-effort-persistent"
	const modelSlug = "gpt-5"
	info := &model.ModelInfo{
		Slug: modelSlug, SupportsReasoningEffortUpdates: true,
		SupportedReasoningLevels: []string{"low", "medium", "high", "xhigh"},
		DefaultReasoningLevel:    "medium",
	}
	params := &turn.TurnStartParams{ThreadID: threadID}

	var history []any
	var updates []string
	turn := func(selection string) string {
		history = append(history, reasoningEffortHistoryUserMessage("continue"))
		effort, ok := router.effortForConfigurationUpdate(threadID, reasoningEffortTestConfig(selection, true), params, info, "openai")
		if !ok {
			t.Fatalf("selection %q did not resolve to an override effort", selection)
		}
		items := router.reasoningEffortOverrideInputItems(threadID, modelSlug, effort, true, history)
		if len(items) > 1 {
			t.Fatalf("selection %q appended %d updates", selection, len(items))
		}
		if len(items) == 1 {
			updates = append(updates, reasoningEffortFromUpdate(t, items[0]))
		}
		history = append(history, items...)
		return router.reasoningEffortForRequest(threadID, modelSlug, selection, true, effort, true, requestEffortSampling)
	}

	requests := []string{turn("high"), turn("persistent"), turn("persistent"), turn("high")}
	wantUpdates := []string{"high", "disabled", "high"}
	if len(updates) != len(wantUpdates) {
		t.Fatalf("updates = %#v, want %#v", updates, wantUpdates)
	}
	for i := range updates {
		if updates[i] != wantUpdates[i] {
			t.Fatalf("updates = %#v, want %#v", updates, wantUpdates)
		}
	}
	for i, effort := range requests {
		if effort != "high" {
			t.Fatalf("turn %d request effort = %q, want the pinned high", i+1, effort)
		}
	}
}
