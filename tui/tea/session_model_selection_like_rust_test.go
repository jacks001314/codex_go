package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

func sessionModelSelectionTestModel(t *testing.T, state *codextui.State, options []codextui.ModelPickerOption, extra Options, responses *[]ModalResponse) *Model {
	t.Helper()
	extra.Width = 100
	extra.Height = 30
	extra.ModelPickerOptions = options
	extra.OnModalResponse = func(response ModalResponse) bubbletea.Cmd {
		*responses = append(*responses, response)
		return nil
	}
	return NewModel(state, extra)
}

func TestSessionModelSelectionShortcutCoversFinalPickersLikeRust(t *testing.T) {
	tests := []struct {
		name       string
		state      *codextui.State
		options    []codextui.ModelPickerOption
		openChild  bool
		wantModel  string
		wantEffort string
		wantFooter string
	}{
		{
			name:  "direct model default",
			state: codextui.NewState(&codextui.Options{Model: "gpt-current"}),
			options: []codextui.ModelPickerOption{{
				ID: "gpt-direct", Label: "Direct", DefaultReasoningEffort: "high",
			}},
			wantModel:  "gpt-direct",
			wantEffort: "high",
			wantFooter: "enter default \u00b7 s session \u00b7 esc back",
		},
		{
			name:  "reasoning submenu",
			state: codextui.NewState(&codextui.Options{Model: "gpt-reasoning", ReasoningEffort: "medium"}),
			options: []codextui.ModelPickerOption{{
				ID:                     "gpt-reasoning",
				Label:                  "Reasoning",
				DefaultReasoningEffort: "high",
				SupportedReasoningEfforts: []codextui.ReasoningEffortOption{
					{Effort: "low", Label: "Low"},
					{Effort: "high", Label: "High", IsDefault: true},
				},
			}},
			openChild:  true,
			wantModel:  "gpt-reasoning",
			wantEffort: "high",
			wantFooter: "enter default \u00b7 s session \u00b7 esc back",
		},
		{
			name:  "advanced reasoning submenu",
			state: codextui.NewState(&codextui.Options{Model: "gpt-current"}),
			options: []codextui.ModelPickerOption{{
				ID: "gpt-advanced", Label: "Advanced", DefaultReasoningEffort: "ultra",
			}},
			openChild:  true,
			wantModel:  "gpt-advanced",
			wantEffort: "ultra",
			wantFooter: "enter apply \u00b7 s session \u00b7 esc back",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			var responses []ModalResponse
			model := sessionModelSelectionTestModel(t, testCase.state, testCase.options, Options{}, &responses)
			model.openModelPicker()
			if testCase.openChild {
				model.Update(key(bubbletea.KeyEnter))
			}
			if view := model.View(); !strings.Contains(view, testCase.wantFooter) {
				t.Fatalf("picker footer missing %q:\n%s", testCase.wantFooter, view)
			}
			model.Update(bubbletea.KeyMsg(bubbletea.Key{Type: bubbletea.KeyRunes, Runes: []rune{'s'}}))
			if len(responses) != 1 || responses[0].Picker == nil {
				t.Fatalf("responses = %#v, want one session-model decision", responses)
			}
			decision := responses[0].Picker
			if decision.Kind != "session_model" || decision.Value != testCase.wantModel || decision.ReasoningEffort != testCase.wantEffort {
				t.Fatalf("decision = %#v", decision)
			}
			if model.State.Model != testCase.wantModel || model.State.EffectiveReasoningEffort() != testCase.wantEffort {
				t.Fatalf("state model/effort = %q/%q", model.State.Model, model.State.EffectiveReasoningEffort())
			}
			if view := model.View(); !strings.Contains(view, "for this session only") {
				t.Fatalf("session-only confirmation missing:\n%s", view)
			}
		})
	}
}

func TestSessionModelSelectionShortcutHonorsListKeymapConflictLikeRust(t *testing.T) {
	keymap := codextui.NewKeymapConfig()
	if err := keymap.Set("list", "accept", []string{"s"}); err != nil {
		t.Fatalf("configure list accept: %v", err)
	}
	var responses []ModalResponse
	model := sessionModelSelectionTestModel(t,
		codextui.NewState(&codextui.Options{Model: "gpt-current"}),
		[]codextui.ModelPickerOption{{ID: "gpt-direct", Label: "Direct", DefaultReasoningEffort: "high"}},
		Options{KeymapConfig: keymap},
		&responses,
	)
	model.openModelPicker()
	if view := model.View(); strings.Contains(view, "s session") {
		t.Fatalf("conflicting secondary shortcut was rendered:\n%s", view)
	}
	model.Update(bubbletea.KeyMsg(bubbletea.Key{Type: bubbletea.KeyRunes, Runes: []rune{'s'}}))
	if len(responses) != 0 || model.State.Model != "gpt-current" {
		t.Fatalf("conflicting s triggered a selection: responses=%#v model=%q", responses, model.State.Model)
	}
}

func TestSessionModelSelectionKeepsPlanAndNonPlanEffortsSeparateLikeRust(t *testing.T) {
	t.Run("plan mode non-ultra changes only the plan override", func(t *testing.T) {
		state := codextui.NewState(&codextui.Options{
			Model:                   "gpt-plan",
			ReasoningEffort:         "high",
			PlanMode:                true,
			PlanModeReasoningEffort: "low",
		})
		var responses []ModalResponse
		model := sessionModelSelectionTestModel(t, state, []codextui.ModelPickerOption{{
			ID: "gpt-plan", Label: "Plan", DefaultReasoningEffort: "medium",
		}}, Options{}, &responses)
		model.openModelPicker()
		model.Update(bubbletea.KeyMsg(bubbletea.Key{Type: bubbletea.KeyRunes, Runes: []rune{'s'}}))
		if model.State.PlanModeReasoningEffort != "medium" || model.State.ReasoningEffort != "high" {
			t.Fatalf("plan/current effort = %q/%q", model.State.PlanModeReasoningEffort, model.State.ReasoningEffort)
		}
	})

	t.Run("ultra updates both efforts", func(t *testing.T) {
		state := codextui.NewState(&codextui.Options{
			Model:                   "gpt-plan",
			ReasoningEffort:         "high",
			PlanMode:                true,
			PlanModeReasoningEffort: "low",
		})
		var responses []ModalResponse
		model := sessionModelSelectionTestModel(t, state, []codextui.ModelPickerOption{{
			ID: "gpt-plan", Label: "Plan", DefaultReasoningEffort: "ultra",
		}}, Options{}, &responses)
		model.openModelPicker()
		model.Update(key(bubbletea.KeyEnter))
		model.Update(bubbletea.KeyMsg(bubbletea.Key{Type: bubbletea.KeyRunes, Runes: []rune{'s'}}))
		if model.State.PlanModeReasoningEffort != "ultra" || model.State.ReasoningEffort != "ultra" {
			t.Fatalf("plan/current effort = %q/%q", model.State.PlanModeReasoningEffort, model.State.ReasoningEffort)
		}
	})
}

func TestResolveSessionModelSelectionNeverPersistsDefaultsLikeRust(t *testing.T) {
	plan, ok := ResolveModelSelection(&PickerDecision{
		Kind:            "session_model",
		Value:           "gpt-5.5",
		ReasoningEffort: "high",
	})
	if !ok {
		t.Fatal("session model decision was not resolved")
	}
	if plan.ThreadModel == nil || *plan.ThreadModel != "gpt-5.5" || plan.ThreadEffort == nil || *plan.ThreadEffort != "high" {
		t.Fatalf("thread plan = %#v", plan)
	}
	if len(plan.Persist) != 0 {
		t.Fatalf("session model persisted defaults: %#v", plan.Persist)
	}
}

func TestSessionModelSelectionWarnsForHighUltraConcurrencyLikeRust(t *testing.T) {
	tests := []struct {
		name       string
		maxThreads int
		want       []string
	}{
		{
			name:       "threshold reached",
			maxThreads: 8,
			want: []string{
				"Ultra reasoning may proactively use multiple agents",
				"8 concurrent threads",
				"7 subagents",
				"below 8",
			},
		},
		{
			name:       "below threshold",
			maxThreads: 7,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			var responses []ModalResponse
			model := sessionModelSelectionTestModel(t,
				codextui.NewState(&codextui.Options{Model: "gpt-current"}),
				[]codextui.ModelPickerOption{{
					ID: "gpt-advanced", Label: "Advanced", DefaultReasoningEffort: "ultra",
				}},
				Options{MultiAgentV2MaxConcurrentThreads: testCase.maxThreads},
				&responses,
			)
			model.openModelPicker()
			model.Update(key(bubbletea.KeyEnter))
			model.Update(bubbletea.KeyMsg(bubbletea.Key{Type: bubbletea.KeyRunes, Runes: []rune{'s'}}))
			if len(responses) != 1 || responses[0].Picker == nil || responses[0].Picker.ReasoningEffort != "ultra" {
				t.Fatalf("responses = %#v, want one Ultra session-model decision", responses)
			}
			view := strings.Join(strings.Fields(model.View()), " ")
			for _, want := range testCase.want {
				if !strings.Contains(view, want) {
					t.Fatalf("Ultra concurrency warning missing %q:\n%s", want, view)
				}
			}
			if len(testCase.want) == 0 && strings.Contains(view, "Ultra reasoning may proactively use multiple agents") {
				t.Fatalf("Ultra concurrency warning rendered below threshold:\n%s", view)
			}
		})
	}
}
