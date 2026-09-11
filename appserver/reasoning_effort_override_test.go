package appserver

import (
	"testing"

	"codex_go/config"
	"codex_go/model"
	"codex_go/turn"
)

func reasoningEffortTestConfig(effort string, featureEnabled bool) *config.Config {
	values := map[string]any{}
	if featureEnabled {
		values["features"] = map[string]any{"reasoning_effort_override": true}
	}
	if effort != "" {
		values["model_reasoning_effort"] = effort
	}
	return &config.Config{Values: values}
}

func trustedReasoningUpdate(effort string) map[string]any {
	return map[string]any{
		"type":             "configuration_update",
		"reasoning":        map[string]any{"effort": effort},
		"harness_metadata": map[string]any{"harness_authored_configuration": true},
	}
}

func TestReasoningEffortPinLikeRust(t *testing.T) {
	var pin reasoningEffortPin
	if _, ok := pin.get("gpt-5"); ok {
		t.Fatal("unset pin should have no effort")
	}
	if got := pin.pin("gpt-5", "high"); got != "high" {
		t.Fatalf("pin = %q, want high", got)
	}
	if got, ok := pin.get("gpt-5"); !ok || got != "high" {
		t.Fatalf("get = %q,%v want high,true", got, ok)
	}
	// A different model does not reuse the pin and replaces it.
	if got := pin.pin("gpt-6", "low"); got != "low" {
		t.Fatalf("pin other model = %q, want low", got)
	}
	if _, ok := pin.get("gpt-5"); ok {
		t.Fatal("old model should no longer be pinned")
	}
	// Compacted pins are not active.
	pin = reasoningEffortPin{kind: reasoningEffortPinCompacted}
	if _, ok := pin.get("gpt-5"); ok {
		t.Fatal("compacted pin should not report an effort")
	}
}

func TestReasoningEffortOverrideInputItemsLikeRust(t *testing.T) {
	router := &RuntimeRouter{}
	threadID := "thread-effort"

	// First turn: no history, no pin -> record.
	items := router.reasoningEffortOverrideInputItems(threadID, "gpt-5", "high", true, nil)
	if len(items) != 1 {
		t.Fatalf("first turn items = %#v, want one update", items)
	}

	// Pin now matches and history is empty -> the request pin already covers it
	// only after it is established; history without an update still records.
	router.setReasoningEffortPinState(threadID, reasoningEffortPin{kind: reasoningEffortPinActive, model: "gpt-5", effort: "high"})
	if items := router.reasoningEffortOverrideInputItems(threadID, "gpt-5", "high", true, nil); len(items) != 0 {
		t.Fatalf("matching pin items = %#v, want none", items)
	}

	// Changed selection records again.
	if items := router.reasoningEffortOverrideInputItems(threadID, "gpt-5", "low", true, nil); len(items) != 1 {
		t.Fatalf("changed selection items = %#v, want one update", items)
	}

	// A matching trusted tail update is reused (recovery) even with no pin.
	router.clearReasoningEffortPin(threadID)
	history := []any{trustedReasoningUpdate("high")}
	if items := router.reasoningEffortOverrideInputItems(threadID, "gpt-5", "high", true, history); len(items) != 0 {
		t.Fatalf("tail reuse items = %#v, want none", items)
	}

	// Trusted history that is not the tail still suppresses a matching pin.
	nonTail := []any{trustedReasoningUpdate("high"), map[string]any{"type": "message", "role": "assistant"}}
	if items := router.reasoningEffortOverrideInputItems(threadID, "gpt-5", "high", true, nonTail); len(items) != 1 {
		t.Fatalf("non-tail established items = %#v, want one update (pin unset)", items)
	}

	// Unavailable override records nothing.
	if items := router.reasoningEffortOverrideInputItems(threadID, "gpt-5", "", false, nil); len(items) != 0 {
		t.Fatalf("unavailable items = %#v, want none", items)
	}
}

func TestReasoningEffortForRequestLikeRust(t *testing.T) {
	router := &RuntimeRouter{}
	threadID := "thread-effort-req"

	// Feature disabled always uses the selected effort.
	if got := router.reasoningEffortForRequest(threadID, "gpt-5", "high", false, "high", true, requestEffortSampling); got != "high" {
		t.Fatalf("disabled feature effort = %q, want high", got)
	}
	if _, ok := router.reasoningEffortPinState(threadID).get("gpt-5"); ok {
		t.Fatal("disabled feature must not pin")
	}

	// Sampling pins the resolved effort.
	if got := router.reasoningEffortForRequest(threadID, "gpt-5", "low", true, "high", true, requestEffortSampling); got != "high" {
		t.Fatalf("sampling effort = %q, want pinned high", got)
	}
	// A later different selection keeps the pinned baseline for the request.
	if got := router.reasoningEffortForRequest(threadID, "gpt-5", "low", true, "low", true, requestEffortSampling); got != "high" {
		t.Fatalf("pinned sampling effort = %q, want high", got)
	}
	// Compaction reuses the pin and never mutates it.
	if got := router.reasoningEffortForRequest(threadID, "gpt-5", "low", true, "medium", true, requestEffortCompaction); got != "high" {
		t.Fatalf("compaction effort = %q, want pinned high", got)
	}
	if got, _ := router.reasoningEffortPinState(threadID).get("gpt-5"); got != "high" {
		t.Fatalf("compaction mutated pin to %q", got)
	}

	// Unavailable overrides clear the pin and fall back to the selection.
	if got := router.reasoningEffortForRequest(threadID, "gpt-5", "medium", true, "", false, requestEffortSampling); got != "medium" {
		t.Fatalf("unavailable effort = %q, want medium", got)
	}
	if _, ok := router.reasoningEffortPinState(threadID).get("gpt-5"); ok {
		t.Fatal("unavailable overrides must clear the pin")
	}
}

func TestEffortForConfigurationUpdateGatingLikeRust(t *testing.T) {
	router := &RuntimeRouter{}
	params := &turn.TurnStartParams{}
	lite := &model.ModelInfo{Slug: "gpt-5", UseResponsesLite: true, DefaultReasoningLevel: "medium"}
	notLite := &model.ModelInfo{Slug: "gpt-5", UseResponsesLite: false, DefaultReasoningLevel: "medium"}

	if _, ok := router.effortForConfigurationUpdate(reasoningEffortTestConfig("high", false), params, lite, "openai"); ok {
		t.Fatal("feature disabled must not offer an override")
	}
	if _, ok := router.effortForConfigurationUpdate(reasoningEffortTestConfig("high", true), params, notLite, "openai"); ok {
		t.Fatal("responses lite disabled must not offer an override")
	}
	if _, ok := router.effortForConfigurationUpdate(reasoningEffortTestConfig("high", true), params, lite, "azure"); ok {
		t.Fatal("non-openai provider must not offer an override")
	}
	effort, ok := router.effortForConfigurationUpdate(reasoningEffortTestConfig("persistent", true), params, lite, "openai")
	if !ok || effort != "disabled" {
		t.Fatalf("persistent effort = %q,%v want disabled,true", effort, ok)
	}
	// Unknown custom efforts are excluded from durable updates.
	custom := &model.ModelInfo{Slug: "gpt-5", UseResponsesLite: true, DefaultReasoningLevel: "turbo"}
	if _, ok := router.effortForConfigurationUpdate(reasoningEffortTestConfig("", true), params, custom, "openai"); ok {
		t.Fatal("custom effort must not be recorded")
	}
}
