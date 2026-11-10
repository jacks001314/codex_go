package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
)

// Rust #60462: an answer submitted while a turn runs steers that turn and keeps
// the question-answer source so Cloud can admit it with start-or-steer
// semantics; the same answer on an idle thread still starts a turn.
func TestAsyncQuestionAnswerDuringTurnCarriesQuestionAnswerSourceLikeRust(t *testing.T) {
	var steered []SubmitRequest
	state := codextui.NewState(nil)
	state.SetThreadID("thread-question-cloud")
	state.SetStatus("running")
	model := NewModel(state, Options{
		Width:  100,
		Height: 30,
		OnSteerRequest: func(request SubmitRequest, _ string) error {
			steered = append(steered, request)
			return nil
		},
	})
	model = feedAsyncQuestions(t, model, "question-cloud", []any{map[string]any{"title": "Which region?"}})
	updated, _ := model.Update(keymsg.KeyMsg{Type: keymsg.KeyUp, Alt: true})
	model = updated.(*Model)
	typeText(t, model, "Europe")
	updated, cmd := model.Update(keymsg.KeyMsg{Type: keymsg.KeyEnter})
	model = updated.(*Model)
	if cmd != nil {
		if message := cmd(); message != nil {
			model.Update(message)
		}
	}
	if len(model.SubmittedRequests()) != 0 {
		t.Fatalf("a running-turn answer started a new turn: %#v", model.SubmittedRequests())
	}
	if len(steered) != 1 {
		t.Fatalf("steered submissions = %#v", steered)
	}
	if steered[0].Source != SubmitSourceQuestionAnswer || !steered[0].DuringTurn {
		t.Fatalf("steer source = %q duringTurn = %v", steered[0].Source, steered[0].DuringTurn)
	}
	if !strings.Contains(steered[0].Prompt, "Europe") {
		t.Fatalf("steered answer = %q", steered[0].Prompt)
	}
	if steered[0].ClientID == "" {
		t.Fatal("steered answer lost its client message id")
	}
}

func TestAsyncQuestionAnswerWhileIdleStartsTurnLikeRust(t *testing.T) {
	model := newAsyncQuestionModel()
	model = feedAsyncQuestions(t, model, "question-idle", []any{map[string]any{"title": "Which region?"}})
	updated, _ := model.Update(keymsg.KeyMsg{Type: keymsg.KeyUp, Alt: true})
	model = updated.(*Model)
	typeText(t, model, "Europe")
	updated, _ = model.Update(keymsg.KeyMsg{Type: keymsg.KeyEnter})
	model = updated.(*Model)
	requests := model.SubmittedRequests()
	if len(requests) != 1 {
		t.Fatalf("submitted requests = %#v", requests)
	}
	if requests[0].Source != SubmitSourceQuestionAnswer || requests[0].DuringTurn {
		t.Fatalf("submit source = %q duringTurn = %v", requests[0].Source, requests[0].DuringTurn)
	}
}

func TestPromptSubmissionKeepsPromptSource(t *testing.T) {
	model := newAsyncQuestionModel()
	model.composer.SetValue("hello")
	cmd := model.submitComposer()
	if cmd != nil {
		_ = cmd()
	}
	requests := model.SubmittedRequests()
	if len(requests) != 1 || requests[0].Source != SubmitSourcePrompt {
		t.Fatalf("submitted requests = %#v", requests)
	}
}
