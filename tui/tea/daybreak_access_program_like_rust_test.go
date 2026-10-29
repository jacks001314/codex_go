package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

func cyberProgramTestModel(t *testing.T, daybreak bool, programs []string) *Model {
	t.Helper()
	state := codextui.NewState(&codextui.Options{
		Model:             "gpt-5",
		Provider:          "openai",
		AccountDisplay:    "user@example.com (Plus)",
		HasChatGPTAccount: true,
	})
	model := NewModel(state, Options{
		Width:             80,
		Height:            24,
		HasChatGPTAccount: true,
		FeatureSettings:   map[string]bool{"cli_daybreak": true},
		ModelPickerOptions: []codextui.ModelPickerOption{{
			ID:                     "gpt-5",
			AvailableCyberPrograms: programs,
		}},
	})
	model.daybreakEnabled = daybreak
	return model
}

// Rust #49859: eligible ChatGPT turns select standard with Daybreak off and
// daybreak_blue with it on, validating model support.
func TestCyberAccessProgramForTurnLikeRust(t *testing.T) {
	programs := []string{"standard", "daybreak_blue"}
	off := cyberProgramTestModel(t, false, programs)
	if got, err := off.cyberAccessProgramForTurn(); err != nil || got != "standard" {
		t.Fatalf("daybreak off program = %q err=%v", got, err)
	}
	on := cyberProgramTestModel(t, true, programs)
	if got, err := on.cyberAccessProgramForTurn(); err != nil || got != "daybreak_blue" {
		t.Fatalf("daybreak on program = %q err=%v", got, err)
	}
	unsupported := cyberProgramTestModel(t, true, []string{"standard"})
	if _, err := unsupported.cyberAccessProgramForTurn(); err == nil ||
		!strings.Contains(err.Error(), "could not be confirmed by the connected server") {
		t.Fatal("daybreak on unsupported model did not error")
	}
	red := cyberProgramTestModel(t, true, []string{"standard", "daybreak_red"})
	if got, err := red.cyberAccessProgramForTurn(); err != nil || got != "daybreak_red" {
		t.Fatalf("daybreak_red program = %q err=%v", got, err)
	}
}

// Rust #53717: missing or empty discovery metadata preserves the server's
// automatic treatment instead of failing the turn, for both a listed model
// without programs and an explicit model ID absent from the catalog.
func TestCyberAccessProgramMissingMetadataPreservesServerTreatmentLikeRust(t *testing.T) {
	empty := cyberProgramTestModel(t, true, []string{})
	if got, err := empty.cyberAccessProgramForTurn(); err != nil || got != "" {
		t.Fatalf("empty metadata program = %q err=%v", got, err)
	}
	unlisted := cyberProgramTestModel(t, true, nil)
	unlisted.State.Model = "unlisted-model"
	if got, err := unlisted.cyberAccessProgramForTurn(); err != nil || got != "" {
		t.Fatalf("unlisted model program = %q err=%v", got, err)
	}
}

// Rust ModelAccessPrograms::daybreak also accepts daybreak_red, so the toggle
// validates against either supported program.
func TestDaybreakCommandAcceptsRedProgramLikeRust(t *testing.T) {
	model := cyberProgramTestModel(t, false, []string{"standard", "daybreak_red"})
	model.State.SetThreadID("thread-red")
	model.applyDaybreakCommand()
	if !model.daybreakEnabled {
		t.Fatal("daybreak_red catalog did not enable Daybreak")
	}
}

func TestCyberAccessProgramAPIKeyGateLikeRust(t *testing.T) {
	state := codextui.NewState(&codextui.Options{
		Model:          "gpt-5",
		Provider:       "openai",
		AccountDisplay: "API key configured (run codex login to use ChatGPT)",
	})
	model := NewModel(state, Options{
		Width:  80,
		Height: 24,
		FeatureSettings: map[string]bool{
			"cli_daybreak":                  true,
			"api_key_cyber_access_programs": false,
		},
		ModelPickerOptions: []codextui.ModelPickerOption{{
			ID:                     "gpt-5",
			AvailableCyberPrograms: []string{"standard", "daybreak_blue"},
		}},
	})
	if got, err := model.cyberAccessProgramForTurn(); err != nil || got != "" {
		t.Fatalf("disabled API-key program = %q err=%v", got, err)
	}
	model.featureSettings["api_key_cyber_access_programs"] = true
	if got, err := model.cyberAccessProgramForTurn(); err != nil || got != "standard" {
		t.Fatalf("enabled API-key program = %q err=%v", got, err)
	}
}

func TestTurnSubmissionCarriesCyberAccessProgramLikeRust(t *testing.T) {
	var submitted []SubmitRequest
	state := codextui.NewState(&codextui.Options{
		Model:             "gpt-5",
		Provider:          "openai",
		AccountDisplay:    "user@example.com (Plus)",
		HasChatGPTAccount: true,
	})
	model := NewModel(state, Options{
		Width:             80,
		Height:            24,
		HasChatGPTAccount: true,
		FeatureSettings:   map[string]bool{"cli_daybreak": true},
		ModelPickerOptions: []codextui.ModelPickerOption{{
			ID:                     "gpt-5",
			AvailableCyberPrograms: []string{"standard", "daybreak_blue"},
		}},
		OnSubmitRequest: func(request SubmitRequest) bubbletea.Cmd {
			submitted = append(submitted, request)
			return nil
		},
	})
	model.daybreakEnabled = true
	model.submitRequest(SubmitRequest{Prompt: "hello"}, false)
	if len(submitted) != 1 || submitted[0].CyberAccessProgram != "daybreak_blue" {
		t.Fatalf("submitted requests = %#v", submitted)
	}

	unsupported := cyberProgramTestModel(t, true, []string{"standard"})
	if cmd := unsupported.submitRequest(SubmitRequest{Prompt: "hello"}, false); cmd != nil {
		t.Fatal("unsupported Daybreak submission was dispatched")
	}
	if unsupported.notice == "" {
		t.Fatal("unsupported Daybreak submission did not surface an error")
	}
}
