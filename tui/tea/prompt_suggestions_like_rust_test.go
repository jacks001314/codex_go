package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

func TestParsePromptSuggestionMatchesRust(t *testing.T) {
	if got, ok := ParsePromptSuggestion(`{"suggestion":"  Continue with tests  "}`); !ok || got != "Continue with tests" {
		t.Fatalf("valid suggestion = %q, %v", got, ok)
	}
	invalid := []string{
		`{}`,
		`{"suggestion":1}`,
		`{"suggestion":"ok","extra":true}`,
		`{"suggestion":"line\nbreak"}`,
		`{"suggestion":"separator\u2028here"}`,
		`{"suggestion":"` + strings.Repeat("x", maxPromptSuggestionRunes+1) + `"}`,
		`{"suggestion":"ok"} trailing`,
		`not json`,
	}
	for _, input := range invalid {
		if got, ok := ParsePromptSuggestion(input); ok {
			t.Errorf("ParsePromptSuggestion(%q) = %q, want rejection", input, got)
		}
	}
}

func promptSuggestionTestModel(t *testing.T, enabled bool) *Model {
	t.Helper()
	model := NewModel(nil, Options{Width: 100, Height: 24})
	model.promptSuggestionsEnabled = enabled
	model.composerPlaceholder = "Ask gcode"
	return model
}

func promptSuggestionTestRequest() PromptSuggestionRequest {
	return PromptSuggestionRequest{ThreadID: "thread-1", TurnID: "turn-1", Summary: "auto", ID: 7}
}

func applyPromptSuggestionText(t *testing.T, model *Model, request PromptSuggestionRequest, text string) {
	t.Helper()
	model.setPromptSuggestion(request)
	model.applyPromptSuggestionResult(PromptSuggestionResultMsg{
		Request:  request,
		Response: `{"suggestion":"` + text + `"}`,
	})
}

func TestPromptSuggestionVisibilityAndTabAcceptanceLikeRust(t *testing.T) {
	model := promptSuggestionTestModel(t, true)
	request := promptSuggestionTestRequest()
	applyPromptSuggestionText(t, model, request, "Add focused tests")
	if got := model.visiblePromptSuggestion(); got != "Add focused tests" {
		t.Fatalf("visible suggestion = %q", got)
	}
	if view := model.View(); !strings.Contains(view, "Add focused tests") {
		t.Fatalf("view missing suggestion:\n%s", view)
	}

	model.Update(key(bubbletea.KeyTab))
	if got := model.composerText(); got != "Add focused tests" {
		t.Fatalf("composer after Tab = %q", got)
	}
	if model.promptSuggestion != nil {
		t.Fatal("suggestion was not consumed")
	}
	// The duplicate Tab from the same key cycle must not queue the draft.
	model.Update(key(bubbletea.KeyTab))
	if len(model.queued) != 0 {
		t.Fatalf("duplicate Tab queued the prompt: %#v", model.queued)
	}
	if got := model.composerText(); got != "Add focused tests" {
		t.Fatalf("duplicate Tab changed composer = %q", got)
	}
}

func TestPromptSuggestionEscapeAndStaleResultsLikeRust(t *testing.T) {
	model := promptSuggestionTestModel(t, true)
	request := promptSuggestionTestRequest()
	model.setPromptSuggestion(request)
	stale := request
	stale.ID++
	model.applyPromptSuggestionResult(PromptSuggestionResultMsg{
		Request:  stale,
		Response: `{"suggestion":"stale"}`,
	})
	if got := model.visiblePromptSuggestion(); got != "" {
		t.Fatalf("stale suggestion became visible: %q", got)
	}

	model.applyPromptSuggestionResult(PromptSuggestionResultMsg{
		Request:  request,
		Response: `{"suggestion":"Cancel me"}`,
	})
	if got := model.visiblePromptSuggestion(); got != "Cancel me" {
		t.Fatalf("current suggestion = %q", got)
	}
	model.Update(key(bubbletea.KeyEsc))
	if model.promptSuggestion != nil || model.composerText() != "" {
		t.Fatalf("Escape did not cancel: suggestion=%#v composer=%q", model.promptSuggestion, model.composerText())
	}
}

func TestPromptSuggestionGatesAndConfigLikeRust(t *testing.T) {
	disabled := promptSuggestionTestModel(t, false)
	request := promptSuggestionTestRequest()
	disabled.setPromptSuggestion(request)
	disabled.applyPromptSuggestionResult(PromptSuggestionResultMsg{
		Request:  request,
		Response: `{"suggestion":"hidden"}`,
	})
	if got := disabled.visiblePromptSuggestion(); got != "" {
		t.Fatalf("disabled suggestion = %q", got)
	}

	model := promptSuggestionTestModel(t, true)
	applyPromptSuggestionText(t, model, request, "hidden while running")
	model.setStatus("running")
	if got := model.visiblePromptSuggestion(); got != "" {
		t.Fatalf("running suggestion = %q", got)
	}

	model = promptSuggestionTestModel(t, true)
	model.composer.InsertString("draft")
	applyPromptSuggestionText(t, model, request, "hidden by draft")
	if got := model.visiblePromptSuggestion(); got != "" {
		t.Fatalf("nonempty draft suggestion = %q", got)
	}

	model = promptSuggestionTestModel(t, true)
	applyPromptSuggestionText(t, model, request, "hidden for other thread")
	model.State.ThreadID = "thread-2"
	if got := model.visiblePromptSuggestion(); got != "" {
		t.Fatalf("other-thread suggestion = %q", got)
	}
}

func TestPromptSuggestionYieldsToConfiguredTabBindingLikeRust(t *testing.T) {
	keymap := codextui.NewKeymapConfig()
	if err := keymap.Set("editor", "move_left", []string{"tab"}); err != nil {
		t.Fatal(err)
	}
	model := NewModel(nil, Options{Width: 100, Height: 24, KeymapConfig: keymap})
	model.promptSuggestionsEnabled = true
	request := promptSuggestionTestRequest()
	applyPromptSuggestionText(t, model, request, "do not accept")
	if got := model.visiblePromptSuggestion(); got != "do not accept" {
		t.Fatalf("visible suggestion = %q", got)
	}
	model.Update(key(bubbletea.KeyTab))
	if model.promptSuggestion == nil || model.composerText() != "" {
		t.Fatalf("configured Tab binding accepted the suggestion: composer=%q", model.composerText())
	}
}
