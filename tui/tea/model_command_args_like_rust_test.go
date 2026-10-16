package tea

import (
	"testing"

	codextui "codex_go/tui"
)

// Rust #54378 (upstream 0884c8047a): `/model [model] --reasoning <level>
// --speed <tier>` updates the current session only, bare `/model` still opens the
// picker, and both space-separated and `=` values are accepted. Counterparts:
// codex-rs/tui/src/chatwidget/model_command.rs (grammar and validation) and
// codex-rs/tui/src/app/tests/model_defaults_tests.rs
// (session-only application, unchanged saved defaults, hidden current models,
// speed defaults).

func modelCommandTestOptions() []codextui.ModelPickerOption {
	return []codextui.ModelPickerOption{
		{
			ID:                        "gpt-5.5",
			Label:                     "gpt-5.5",
			ShowInPicker:              true,
			DefaultReasoningEffort:    "medium",
			SupportedReasoningEfforts: []codextui.ReasoningEffortOption{{Effort: "low"}, {Effort: "high"}},
			ServiceTiers:              []string{"priority", "flex"},
		},
		{
			ID:                        "codex-auto-compact",
			Label:                     "codex-auto-compact",
			ShowInPicker:              true,
			DefaultReasoningEffort:    "medium",
			SupportedReasoningEfforts: []codextui.ReasoningEffortOption{{Effort: "medium"}},
		},
	}
}

// modelCommandTestModel wires a session on a thread with a loaded catalog and a
// settings writer the test can watch for accidental saved-default writes.
func modelCommandTestModel(t *testing.T, options []codextui.ModelPickerOption) (*Model, *int) {
	t.Helper()
	state := codextui.NewState(&codextui.Options{Model: "gpt-5.5", ReasoningEffort: "medium"})
	state.SetThreadID("thread-model-command")
	writes := 0
	model := NewModel(state, Options{
		Width:              90,
		Height:             24,
		FeatureSettings:    map[string]bool{"fast_mode": true},
		ModelPickerOptions: options,
		OnWriteSettings: func(edits []SettingsEdit) (SettingsWriteResult, error) {
			writes++
			return SettingsWriteResult{}, nil
		},
	})
	model.modelCatalogOpts = append([]codextui.ModelPickerOption(nil), options...)
	return model, &writes
}

func TestModelCommandArgsParsingLikeRust(t *testing.T) {
	accepted := []struct {
		args   string
		model  string
		effort string
		speed  string
	}{
		{args: ""},
		{args: "gpt-5.5", model: "gpt-5.5"},
		{args: "gpt-5.5 --reasoning high", model: "gpt-5.5", effort: "high"},
		{args: "--reasoning=high --speed=standard", effort: "high", speed: "standard"},
		{args: "--speed fast", speed: "fast"},
		{args: "--reasoning  high   --speed standard", effort: "high", speed: "standard"},
		{args: "'gpt-5.5' --reasoning high", model: "gpt-5.5", effort: "high"},
		{args: "-- --literal", model: "--literal"},
	}
	for _, testCase := range accepted {
		parsed, failure := parseModelCommandArgs(testCase.args)
		if failure != "" {
			t.Fatalf("parseModelCommandArgs(%q) failed: %s", testCase.args, failure)
		}
		if parsed.model != testCase.model || parsed.effort != testCase.effort || parsed.speed != testCase.speed {
			t.Fatalf("parseModelCommandArgs(%q) = %#v, want model=%q effort=%q speed=%q",
				testCase.args, parsed, testCase.model, testCase.effort, testCase.speed)
		}
	}

	rejected := []struct {
		args    string
		message string
	}{
		{"--bogus", "Unknown command setting `--bogus`."},
		{"--reasoning", "Command setting `--reasoning` requires a value."},
		{"--reasoning=", "Command setting `--reasoning` requires a value."},
		{"--reasoning --speed fast", "Command setting `--reasoning` requires a value."},
		{"--speed", "Command setting `--speed` requires a value."},
		{"--reasoning high --reasoning low", "Command setting `--reasoning` was provided more than once."},
		{"--speed standard --speed flex", "Command setting `--speed` was provided more than once."},
		{"gpt-5.5 gpt-6", "Unexpected argument 'gpt-6'. Use '--reasoning=<level>' or '--speed=<tier>'."},
		{"'unfinished", "Invalid command arguments. Check the quoting."},
	}
	for _, testCase := range rejected {
		if _, failure := parseModelCommandArgs(testCase.args); failure != testCase.message {
			t.Fatalf("parseModelCommandArgs(%q) failure = %q, want %q", testCase.args, failure, testCase.message)
		}
	}
}

func TestModelCommandAppliesSessionModelReasoningAndSpeedLikeRust(t *testing.T) {
	model, writes := modelCommandTestModel(t, modelCommandTestOptions())

	model.applyModelCommandArgs("gpt-5.5 --reasoning high --speed standard")
	if model.State.Model != "gpt-5.5" || model.State.ReasoningEffort != "high" {
		t.Fatalf("session model/effort = %q/%q, want gpt-5.5/high", model.State.Model, model.State.ReasoningEffort)
	}
	if model.State.ServiceTier != codextui.ServiceTierDefaultRequestValue {
		t.Fatalf("session service tier = %q, want %q", model.State.ServiceTier, codextui.ServiceTierDefaultRequestValue)
	}
	if want := "Model changed to gpt-5.5 high standard for this session only"; model.notice != want {
		t.Fatalf("notice = %q, want %q", model.notice, want)
	}

	// A speed-only update keeps the effort and accepts the tier display name
	// (Rust matches by request value or by name).
	model.applyModelCommandArgs("--speed fast")
	if model.State.ServiceTier != codextui.ServiceTierFastRequestValue {
		t.Fatalf("speed-only tier = %q, want %q", model.State.ServiceTier, codextui.ServiceTierFastRequestValue)
	}
	if model.State.ReasoningEffort != "high" {
		t.Fatalf("speed-only update changed the effort to %q", model.State.ReasoningEffort)
	}
	if want := "Model changed to gpt-5.5 high fast for this session only"; model.notice != want {
		t.Fatalf("notice = %q, want %q", model.notice, want)
	}

	// The auto models carry no reasoning label (App::reasoning_label_for).
	model.applyModelCommandArgs("codex-auto-compact")
	if want := "Model changed to codex-auto-compact for this session only"; model.notice != want {
		t.Fatalf("auto-model notice = %q, want %q", model.notice, want)
	}

	if *writes != 0 {
		t.Fatalf("a session-only /model wrote saved defaults %d time(s)", *writes)
	}
}

func TestModelSettingsOnlyCommandAcceptsTheCurrentHiddenModelLikeRust(t *testing.T) {
	options := []codextui.ModelPickerOption{{
		ID:                        "gpt-5.5",
		Label:                     "gpt-5.5",
		ShowInPicker:              false,
		DefaultReasoningEffort:    "medium",
		SupportedReasoningEfforts: []codextui.ReasoningEffortOption{{Effort: "high"}},
		ServiceTiers:              []string{"priority"},
	}}
	model, writes := modelCommandTestModel(t, options)

	model.applyModelCommandArgs("--reasoning high --speed standard")
	if model.State.Model != "gpt-5.5" || model.State.ReasoningEffort != "high" {
		t.Fatalf("hidden current model settings = %q/%q, want gpt-5.5/high", model.State.Model, model.State.ReasoningEffort)
	}
	if model.State.ServiceTier != codextui.ServiceTierDefaultRequestValue {
		t.Fatalf("hidden current model tier = %q, want %q", model.State.ServiceTier, codextui.ServiceTierDefaultRequestValue)
	}

	// Naming the hidden model explicitly stays rejected (Rust requires
	// show_in_picker for an explicit name).
	model.applyModelCommandArgs("gpt-5.5 --reasoning=high")
	if want := "Model 'gpt-5.5' is unavailable. Use /model to see available models."; model.notice != want {
		t.Fatalf("explicit hidden model notice = %q, want %q", model.notice, want)
	}
	if *writes != 0 {
		t.Fatal("a session-only /model wrote saved defaults")
	}
}

func TestModelCommandRejectsUnavailableReasoningAndSpeedLikeRust(t *testing.T) {
	model, _ := modelCommandTestModel(t, modelCommandTestOptions())

	model.applyModelCommandArgs("--reasoning ultra")
	if want := "Reasoning level 'ultra' is unavailable for gpt-5.5. Available: low, high."; model.notice != want {
		t.Fatalf("reasoning notice = %q, want %q", model.notice, want)
	}
	model.applyModelCommandArgs("--speed ultrafast")
	if want := "Speed 'ultrafast' is unavailable for gpt-5.5. Available: standard, flex, priority, flex."; model.notice != want {
		t.Fatalf("speed notice = %q, want %q", model.notice, want)
	}
	model.applyModelCommandArgs("gpt-missing")
	if want := "Model 'gpt-missing' is unavailable. Use /model to see available models."; model.notice != want {
		t.Fatalf("model notice = %q, want %q", model.notice, want)
	}
	if model.State.Model != "gpt-5.5" || model.State.ReasoningEffort != "medium" || model.State.ServiceTier != "" {
		t.Fatalf("a rejected command changed the session: %#v", model.State)
	}
}

func TestModelCommandRequiresAnActiveThreadLikeRust(t *testing.T) {
	options := modelCommandTestOptions()
	state := codextui.NewState(&codextui.Options{Model: "gpt-5.5", ReasoningEffort: "medium"})
	model := NewModel(state, Options{Width: 90, Height: 24, ModelPickerOptions: options})
	model.modelCatalogOpts = append([]codextui.ModelPickerOption(nil), options...)

	model.applyModelCommandArgs("gpt-5.5 --reasoning high")
	if want := "Session is still starting; try /model again in a moment."; model.notice != want {
		t.Fatalf("notice = %q, want %q", model.notice, want)
	}
	if model.State.ReasoningEffort != "medium" {
		t.Fatalf("a still-starting session changed the effort to %q", model.State.ReasoningEffort)
	}
}
