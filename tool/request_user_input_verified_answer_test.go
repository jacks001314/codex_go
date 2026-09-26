package tool

import (
	"context"
	"testing"

	"codex_go/retainedctx"
)

const verifiedAnswerQuestionsArgs = `{"questions":[` +
	`{"header":"choice","id":"pick_one","question":"Pick one?","options":[{"label":"A","description":"First"},{"label":"B","description":"Second"}]},` +
	`{"header":"blank","id":"blank","question":"Anything?"},` +
	`{"header":"absent","id":"absent","question":"Nothing?"}]}`

// Mirrors Rust's request_user_input capture: the recorded question text carries
// one line per *selected* option, the answer joins the non-blank submissions, and
// unanswered or blank questions contribute nothing.
func TestRequestUserInputHandlerRecordsVerifiedAnswersLikeRust(t *testing.T) {
	var recordedCallID string
	var recorded []retainedctx.VerifiedQuestionAnswer
	handler := NewRequestUserInputHandler(func(_ context.Context, args *RequestUserInputArgs) (*UserInputResponse, error) {
		return &UserInputResponse{
			Answers: map[string]string{"pick_one": "B"},
			StructuredAnswers: map[string][]string{
				"pick_one": {"B", "extra note"},
				"blank":    {"   "},
			},
		}, nil
	})
	handler.verifiedAnswer = func(callID string, questions []retainedctx.VerifiedQuestionAnswer) {
		recordedCallID = callID
		recorded = questions
	}
	if _, err := handler.Execute(context.Background(), &Invocation{
		CallID:  "call-1",
		Payload: Payload{Kind: PayloadFunction, Arguments: verifiedAnswerQuestionsArgs},
	}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if recordedCallID != "call-1" {
		t.Fatalf("recorded call id = %q", recordedCallID)
	}
	if len(recorded) != 1 {
		t.Fatalf("recorded answers = %#v", recorded)
	}
	if recorded[0].Question != "Pick one?\nB: Second" {
		t.Fatalf("recorded question = %q", recorded[0].Question)
	}
	if recorded[0].Answer != "B\nextra note" {
		t.Fatalf("recorded answer = %q", recorded[0].Answer)
	}
}

// A responder that only fills the single-answer map still verifies its answers,
// and a timed-out response is not host verification.
func TestRequestUserInputHandlerSkipsUnacceptedVerifiedAnswersLikeRust(t *testing.T) {
	recorded := 0
	handler := NewRequestUserInputHandler(func(_ context.Context, args *RequestUserInputArgs) (*UserInputResponse, error) {
		return &UserInputResponse{Answers: map[string]string{"pick_one": "A"}}, nil
	})
	handler.verifiedAnswer = func(callID string, questions []retainedctx.VerifiedQuestionAnswer) {
		recorded++
		if len(questions) != 1 || questions[0].Question != "Pick one?\nA: First" || questions[0].Answer != "A" {
			t.Fatalf("single-answer capture = %#v", questions)
		}
	}
	if _, err := handler.Execute(context.Background(), &Invocation{
		CallID:  "call-2",
		Payload: Payload{Kind: PayloadFunction, Arguments: verifiedAnswerQuestionsArgs},
	}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if recorded != 1 {
		t.Fatalf("single-answer responder recorded %d verified answers, want 1", recorded)
	}

	timedOut := NewRequestUserInputHandler(func(_ context.Context, args *RequestUserInputArgs) (*UserInputResponse, error) {
		return &UserInputResponse{Answers: map[string]string{"pick_one": "A"}, TimedOut: true}, nil
	})
	timedOut.verifiedAnswer = func(callID string, questions []retainedctx.VerifiedQuestionAnswer) {
		t.Fatal("a timed-out response was recorded as verified")
	}
	if _, err := timedOut.Execute(context.Background(), &Invocation{
		CallID:  "call-3",
		Payload: Payload{Kind: PayloadFunction, Arguments: verifiedAnswerQuestionsArgs},
	}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
}

// The core-handler registration carries the verified-answer recorder into the
// request_user_input handler, so the app-server's per-turn hook is installed by
// the same options struct that supplies the responder.
func TestRegisterCoreHandlersInstallsTheVerifiedAnswerRecorderLikeRust(t *testing.T) {
	recorded := 0
	registry := NewRegistry()
	if err := RegisterCoreHandlersWithOptions(registry, &CoreHandlerOptions{
		UserInputResponder: func(_ context.Context, args *RequestUserInputArgs) (*UserInputResponse, error) {
			return &UserInputResponse{Answers: map[string]string{"pick_one": "B"}}, nil
		},
		VerifiedAnswerRecorder: func(callID string, questions []retainedctx.VerifiedQuestionAnswer) {
			recorded++
			if callID != "call-4" || len(questions) != 1 || questions[0].Answer != "B" {
				t.Fatalf("recorded verified answer = %q %#v", callID, questions)
			}
		},
	}); err != nil {
		t.Fatalf("RegisterCoreHandlersWithOptions() error = %v", err)
	}
	executor, ok := registry.Lookup(PlainName("request_user_input"))
	if !ok {
		t.Fatal("request_user_input handler was not registered")
	}
	if _, err := executor.Execute(context.Background(), &Invocation{
		CallID:  "call-4",
		Payload: Payload{Kind: PayloadFunction, Arguments: verifiedAnswerQuestionsArgs},
	}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if recorded != 1 {
		t.Fatalf("verified answers recorded = %d, want 1", recorded)
	}
}
