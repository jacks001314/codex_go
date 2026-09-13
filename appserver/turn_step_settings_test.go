package appserver

import (
	"strings"
	"testing"

	"codex_go/codexapi"
	"codex_go/turn"
)

// Rust session::step_settings: a mid-turn turn/settings/update reaches the
// following sampling step, so the step's model, reasoning effort, and request
// metadata describe the updated settings.
func TestTurnStepSettingsProviderReflectsMidTurnUpdatesLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	params := &turn.TurnStartParams{ThreadID: "thread-1", Model: "gpt-initial"}
	if err := router.threads.RegisterTurn("thread-1", "turn-1", nil, 0, params); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}
	if !router.threads.UpdateTurn("thread-1", "turn-1", func(active *activeRuntimeTurn) {
		active.RunConfig = &appTurnRunConfig{
			Model:           "gpt-initial",
			ReasoningEffort: "low",
			ClientMetadata: map[string]string{
				codexapi.ClientCodexTurnMetadataHeader: `{"thread_id":"thread-1","model":"gpt-initial","reasoning_effort":"low","codex_version":"1.2.3"}`,
			},
		}
	}) {
		t.Fatal("UpdateTurn() did not find the registered turn")
	}
	provider := router.turnStepSettingsProvider("thread-1", "turn-1")
	before := provider()
	if before == nil || before.Model != "gpt-initial" || before.ReasoningEffort != "low" {
		t.Fatalf("initial step settings = %#v", before)
	}
	if metadata := before.ClientMetadata[codexapi.ClientCodexTurnMetadataHeader]; !strings.Contains(metadata, `"model":"gpt-initial"`) {
		t.Fatalf("initial step metadata = %q", metadata)
	}

	model, effort := "gpt-updated", "high"
	router.updateActiveTurnRuntimeSettings(&turn.TurnSettingsUpdateParams{
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		Model:    &model,
		Effort:   &effort,
	})
	after := provider()
	if after.Model != "gpt-updated" || after.ReasoningEffort != "high" {
		t.Fatalf("updated step settings = %#v", after)
	}
	metadata := after.ClientMetadata[codexapi.ClientCodexTurnMetadataHeader]
	if !strings.Contains(metadata, `"model":"gpt-updated"`) || !strings.Contains(metadata, `"reasoning_effort":"high"`) {
		t.Fatalf("updated step metadata = %q", metadata)
	}
	// Every other metadata entry survives the refresh.
	if !strings.Contains(metadata, `"codex_version":"1.2.3"`) || !strings.Contains(metadata, `"thread_id":"thread-1"`) {
		t.Fatalf("step metadata lost entries: %q", metadata)
	}

	// Clearing the effort removes the key, like Rust's ExecutionMetadata.
	cleared := ""
	router.updateActiveTurnRuntimeSettings(&turn.TurnSettingsUpdateParams{
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		Effort:   &cleared,
	})
	if metadata := provider().ClientMetadata[codexapi.ClientCodexTurnMetadataHeader]; strings.Contains(metadata, "reasoning_effort") {
		t.Fatalf("cleared step metadata = %q", metadata)
	}

	// A turn that is no longer active reports nothing.
	if _, ok := router.threads.ConsumeTurn("thread-1", "turn-1", true); !ok {
		t.Fatal("ConsumeTurn() did not find the active turn")
	}
	if settings := provider(); settings != nil {
		t.Fatalf("inactive turn step settings = %#v", settings)
	}
}
