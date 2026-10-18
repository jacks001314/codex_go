package tea

import (
	"strings"
	"testing"
	"time"

	codextui "codex_go/tui"
)

func renameSuggestionModel(generate RecapGenerateFunc) *Model {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-a")
	state.AddMessage(codextui.RoleUser, "Fix the login timeout")
	state.AddMessage(codextui.RoleAssistant, "I am checking the logs.")
	state.Messages[len(state.Messages)-1].Phase = "commentary"
	state.AddMessage(codextui.RoleAssistant, "The timeout is fixed.")
	return NewModel(state, Options{
		Width:               100,
		Height:              30,
		OnSuggestThreadName: generate,
	})
}

// TestRenameSuggestionPrefillsPromptLikeRust covers Rust #40495: /rename opens
// the editable prompt with a loading label and pre-fills the generated title.
func TestRenameSuggestionPrefillsPromptLikeRust(t *testing.T) {
	var prompt string
	var options RecapThreadOptions
	model := renameSuggestionModel(func(threadID string, opts RecapThreadOptions, request string, schema map[string]any) (string, error) {
		if threadID != "thread-a" {
			t.Fatalf("thread id = %q", threadID)
		}
		prompt = request
		options = opts
		if schema["type"] != "object" {
			t.Fatalf("schema = %#v", schema)
		}
		return `{"title":"Fix login timeout"}`, nil
	})

	cmd := model.openRenamePrompt()
	if model.modal == nil || model.modal.customPrompt == nil {
		t.Fatal("rename prompt did not open")
	}
	if model.modal.customPrompt.ContextLabel != "Generating a title suggestion\u2026" {
		t.Fatalf("loading label = %q", model.modal.customPrompt.ContextLabel)
	}
	runTeaCmd(t, model, cmd)

	if !strings.Contains(prompt, "Fix the login timeout") || !strings.Contains(prompt, "The timeout is fixed.") {
		t.Fatalf("title prompt missing substantive messages: %q", prompt)
	}
	if strings.Contains(prompt, "I am checking the logs.") {
		t.Fatalf("title prompt included commentary: %q", prompt)
	}
	if options.ThreadSource != "thread_title" {
		t.Fatalf("thread source = %q", options.ThreadSource)
	}
	if model.modal.customPrompt.Text != "Fix login timeout" {
		t.Fatalf("prefilled title = %q", model.modal.customPrompt.Text)
	}
	if model.modal.customPrompt.ContextLabel != "Suggested from this conversation" {
		t.Fatalf("applied label = %q", model.modal.customPrompt.ContextLabel)
	}
}

// TestRenameSuggestionPreservesManualEditLikeRust covers text entered before
// the asynchronous suggestion arrives.
func TestRenameSuggestionPreservesManualEditLikeRust(t *testing.T) {
	model := renameSuggestionModel(func(string, RecapThreadOptions, string, map[string]any) (string, error) {
		return `{"title":"Generated title"}`, nil
	})
	cmd := model.openRenamePrompt()
	model.modal.customPrompt.HandleRuneAt('m', time.Unix(1, 0))
	model.modal.customPrompt.HandleRuneAt('y', time.Unix(2, 0))
	runTeaCmd(t, model, cmd)
	if model.modal.customPrompt.Text != "my" {
		t.Fatalf("manual edit was overwritten: %q", model.modal.customPrompt.Text)
	}
}

// TestRenameSuggestionUsesDedicatedTitleModelLikeRust covers the low-effort
// title model preference when ChatGPT auth and the catalog make it available.
func TestRenameSuggestionUsesDedicatedTitleModelLikeRust(t *testing.T) {
	var options RecapThreadOptions
	state := codextui.NewState(nil)
	state.SetThreadID("thread-a")
	state.Provider = "openai"
	state.Model = "gpt-5"
	state.AddMessage(codextui.RoleUser, "Fix the login timeout")
	model := NewModel(state, Options{
		Width:             100,
		Height:            30,
		HasChatGPTAccount: true,
		ModelPickerOptions: []codextui.ModelPickerOption{{
			ID: threadTitleSuggestionModel,
		}},
		OnSuggestThreadName: func(_ string, opts RecapThreadOptions, _ string, _ map[string]any) (string, error) {
			options = opts
			return `{"title":"Fix login timeout"}`, nil
		},
	})
	runTeaCmd(t, model, model.openRenamePrompt())
	if options.Model != threadTitleSuggestionModel || options.Effort != "low" {
		t.Fatalf("title model options = %#v", options)
	}
}

// TestRenameSuggestionIgnoresStaleThreadLikeRust covers response correlation.
func TestRenameSuggestionIgnoresStaleThreadLikeRust(t *testing.T) {
	model := renameSuggestionModel(func(string, RecapThreadOptions, string, map[string]any) (string, error) {
		return `{"title":"Generated title"}`, nil
	})
	model.openRenamePrompt()
	model.State.SetThreadID("thread-b")
	model.applyThreadNameSuggested(ThreadNameSuggestedMsg{
		ThreadID:   "thread-a",
		RequestID:  model.nextThreadNameSuggestionID,
		Suggestion: "Stale title",
		OK:         true,
	})
	if model.modal.customPrompt.Text != "" {
		t.Fatalf("stale suggestion changed the prompt: %q", model.modal.customPrompt.Text)
	}
}
