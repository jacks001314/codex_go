package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
)

// cloudStatusModel builds the serving-model state plus a cloud catalog entry
// whose display label differs from its id, the way the Cloud catalog reports
// `display_name` (Rust #61350).
func cloudStatusModel(t *testing.T) *Model {
	t.Helper()
	items := []string{"model-with-reasoning"}
	return NewModel(codextui.NewState(&codextui.Options{Model: "gpt-5.5", ReasoningEffort: "low"}), Options{
		Width:           100,
		Height:          30,
		StatusLineItems: items,
		ModelPickerOptions: []codextui.ModelPickerOption{{
			ID:                     "gpt-5.6-terra",
			Label:                  "Cloud Terra",
			DefaultReasoningEffort: "high",
			SupportedReasoningEfforts: []codextui.ReasoningEffortOption{
				{Effort: "low", Label: "Low"},
				{Effort: "medium", Label: "Medium"},
				{Effort: "high", Label: "High", IsDefault: true},
			},
		}},
	})
}

// TestCloudModelSelectionReflectsAcceptedStatusLikeRust locks Rust #61350: an
// accepted session model selection replaces the status line, /status, and the
// reasoning-only /model target, while the pre-acceptance status stays put and
// the serving model's id remains the selection value.
func TestCloudModelSelectionReflectsAcceptedStatusLikeRust(t *testing.T) {
	model := cloudStatusModel(t)
	model.switchThreadID("thread-dest")
	original := model.renderStatusHeader()
	if !strings.Contains(original, "gpt-5.5 low") {
		t.Fatalf("original status line = %q, want the serving model", original)
	}

	// Accepting the cloud catalog entry opens its reasoning submenu; the
	// catalog default (high) is the accepted effort.
	model.openModelPicker()
	model.Update(key(keymsg.KeyEnter))
	model.Update(key(keymsg.KeyDown))
	model.Update(key(keymsg.KeyDown))
	model.Update(key(keymsg.KeyEnter))
	if model.State.Model != "gpt-5.6-terra" || model.State.EffectiveReasoningEffort() != "high" {
		t.Fatalf("session model/effort = %q/%q", model.State.Model, model.State.EffectiveReasoningEffort())
	}

	statusLine := model.renderStatusHeader()
	if !strings.Contains(statusLine, "Cloud Terra high") {
		t.Fatalf("status line = %q, want the accepted catalog display label", statusLine)
	}
	if strings.Contains(statusLine, "gpt-5.6-terra") {
		t.Fatalf("status line = %q, want the display label instead of the id", statusLine)
	}

	typeText(t, model, "/status")
	model.Update(key(keymsg.KeyEnter))
	if len(model.State.Messages) == 0 {
		t.Fatal("/status produced no history cell")
	}
	card := model.State.Messages[len(model.State.Messages)-1].Text
	if !strings.Contains(card, "Cloud Terra (reasoning high") {
		t.Fatalf("status card = %q, want the accepted display label and effort", card)
	}
	if strings.Contains(card, "gpt-5.6-terra (reasoning") {
		t.Fatalf("status card = %q, want the display label instead of the id", card)
	}

	model.applyModelCommandArgs("--reasoning medium")
	if want := "Model changed to gpt-5.6-terra medium for this session only"; model.notice != want {
		t.Fatalf("reasoning-only confirmation = %q, want %q", model.notice, want)
	}

	// Session metadata clearing the selection returns the serving model to
	// every surface (Rust asserts the original status line comes back).
	model.State.Model = "gpt-5.5"
	model.State.ReasoningEffort = "low"
	model.refreshServiceTierCommands()
	if restored := model.renderStatusHeader(); restored != original {
		t.Fatalf("restored status line = %q, want %q", restored, original)
	}
}
