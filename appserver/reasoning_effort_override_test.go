package appserver

import (
	"sync"
	"testing"

	"codex_go/config"
	"codex_go/model"
	"codex_go/session"
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
	if _, ok := pin.Get("gpt-5"); ok {
		t.Fatal("unset pin should have no effort")
	}
	if got := pin.Pin("gpt-5", "high"); got != "high" {
		t.Fatalf("pin = %q, want high", got)
	}
	if got, ok := pin.Get("gpt-5"); !ok || got != "high" {
		t.Fatalf("get = %q,%v want high,true", got, ok)
	}
	// A different model does not reuse the pin and replaces it.
	if got := pin.Pin("gpt-6", "low"); got != "low" {
		t.Fatalf("pin other model = %q, want low", got)
	}
	if _, ok := pin.Get("gpt-5"); ok {
		t.Fatal("old model should no longer be pinned")
	}
	// Compacted pins are not active.
	pin = reasoningEffortPin{Kind: reasoningEffortPinCompacted}
	if _, ok := pin.Get("gpt-5"); ok {
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
	router.setReasoningEffortPinState(threadID, reasoningEffortPin{Kind: reasoningEffortPinActive, Model: "gpt-5", Effort: "high"})
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
	if _, ok := router.reasoningEffortPinState(threadID).Get("gpt-5"); ok {
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
	if got, _ := router.reasoningEffortPinState(threadID).Get("gpt-5"); got != "high" {
		t.Fatalf("compaction mutated pin to %q", got)
	}

	// Unavailable overrides clear the pin and fall back to the selection.
	if got := router.reasoningEffortForRequest(threadID, "gpt-5", "medium", true, "", false, requestEffortSampling); got != "medium" {
		t.Fatalf("unavailable effort = %q, want medium", got)
	}
	if _, ok := router.reasoningEffortPinState(threadID).Get("gpt-5"); ok {
		t.Fatal("unavailable overrides must clear the pin")
	}
}

func TestEffortForConfigurationUpdateGatingLikeRust(t *testing.T) {
	router := &RuntimeRouter{}
	params := &turn.TurnStartParams{}
	supported := &model.ModelInfo{Slug: "gpt-5", UseResponsesLite: true, SupportsReasoningEffortUpdates: true, DefaultReasoningLevel: "medium"}
	// Responses Lite does not establish reasoning-effort update support (#46530).
	unsupported := &model.ModelInfo{Slug: "gpt-5", UseResponsesLite: true, DefaultReasoningLevel: "medium"}
	// Support is independent of Responses Lite.
	nonLiteSupported := &model.ModelInfo{Slug: "gpt-5", SupportsReasoningEffortUpdates: true, DefaultReasoningLevel: "medium"}

	if _, ok := router.effortForConfigurationUpdate("thread-1", reasoningEffortTestConfig("high", false), params, supported, "openai"); ok {
		t.Fatal("feature disabled must not offer an override")
	}
	if _, ok := router.effortForConfigurationUpdate("thread-1", reasoningEffortTestConfig("high", true), params, unsupported, "openai"); ok {
		t.Fatal("missing explicit model support must not offer an override")
	}
	if _, ok := router.effortForConfigurationUpdate("thread-1", reasoningEffortTestConfig("high", true), params, supported, "azure"); ok {
		t.Fatal("non-openai provider must not offer an override")
	}
	if _, ok := router.effortForConfigurationUpdate("thread-1", reasoningEffortTestConfig("high", true), params, nonLiteSupported, "openai"); !ok {
		t.Fatal("explicit model support must offer an override independently of responses lite")
	}
	effort, ok := router.effortForConfigurationUpdate("thread-1", reasoningEffortTestConfig("persistent", true), params, supported, "openai")
	if !ok || effort != "disabled" {
		t.Fatalf("persistent effort = %q,%v want disabled,true", effort, ok)
	}
	// Unknown custom efforts are excluded from durable updates.
	custom := &model.ModelInfo{Slug: "gpt-5", SupportsReasoningEffortUpdates: true, DefaultReasoningLevel: "turbo"}
	if _, ok := router.effortForConfigurationUpdate("thread-1", reasoningEffortTestConfig("", true), params, custom, "openai"); ok {
		t.Fatal("custom effort must not be recorded")
	}
}

// TestReasoningEffortOverrideExemptsFixedEffortWorkersLikeRust covers Rust
// #46531: memory consolidation and ephemeral thread-title workers use their
// selected request-level effort even when the override feature is enabled.
func TestReasoningEffortOverrideExemptsFixedEffortWorkersLikeRust(t *testing.T) {
	ephemeralExtra := func() map[string]any { return map[string]any{"ephemeral": true} }
	router := &RuntimeRouter{threads: &ThreadManager{
		ephemeralMu: sync.RWMutex{},
		ephemeral: map[string]*session.Record{
			"memory": {ID: "memory", Metadata: session.Metadata{
				Source:       internalMemorySessionSource,
				ThreadSource: string(ThreadSourceMemoryConsolidation),
				Extra:        ephemeralExtra(),
			}},
			"title": {ID: "title", Metadata: session.Metadata{
				ThreadSource: "thread_title",
				Extra:        ephemeralExtra(),
			}},
			"persisted-title": {ID: "persisted-title", Metadata: session.Metadata{
				ThreadSource: "thread_title",
			}},
			"ordinary": {ID: "ordinary", Metadata: session.Metadata{Extra: ephemeralExtra()}},
		},
	}}
	supported := &model.ModelInfo{Slug: "gpt-5", SupportsReasoningEffortUpdates: true, DefaultReasoningLevel: "medium"}
	cfg := reasoningEffortTestConfig("high", true)

	for _, threadID := range []string{"memory", "title"} {
		if !router.reasoningEffortOverrideExempt(threadID) {
			t.Fatalf("thread %q must be exempt from reasoning-effort overrides", threadID)
		}
		if router.reasoningEffortOverrideEnabled(threadID, cfg, supported, "openai") {
			t.Fatalf("thread %q must not enable reasoning-effort overrides", threadID)
		}
		if _, ok := router.effortForConfigurationUpdate(threadID, cfg, &turn.TurnStartParams{}, supported, "openai"); ok {
			t.Fatalf("exempt thread %q must not record a trusted update", threadID)
		}
	}
	for _, threadID := range []string{"persisted-title", "ordinary"} {
		if router.reasoningEffortOverrideExempt(threadID) {
			t.Fatalf("thread %q must keep reasoning-effort overrides", threadID)
		}
		if !router.reasoningEffortOverrideEnabled(threadID, cfg, supported, "openai") {
			t.Fatalf("thread %q must enable reasoning-effort overrides", threadID)
		}
	}
}
