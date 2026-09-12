package tool

import (
	"context"
	"strings"
	"testing"
)

func TestRequestUserInputAsyncHandlerSpecAndOutput(t *testing.T) {
	handler := &RequestUserInputAsyncHandler{}
	spec := handler.Spec()
	if spec.Name.Key() != DefaultRequestUserInputAsyncToolName {
		t.Fatalf("tool name = %q", spec.Name.Key())
	}
	if spec.Exposure != ExposureDirectModelOnly {
		t.Fatalf("exposure = %q, want direct_model_only", spec.Exposure)
	}
	if spec.Description != defaultRequestUserInputAsyncDescription {
		t.Fatalf("default description = %q", spec.Description)
	}
	properties, _ := spec.InputSchema["properties"].(map[string]any)
	questions, _ := properties["questions"].(map[string]any)
	if questions == nil || questions["minItems"] != 1 {
		t.Fatalf("questions schema = %#v", questions)
	}
	items, _ := questions["items"].(map[string]any)
	if items == nil {
		t.Fatalf("questions items = %#v", questions["items"])
	}
	required, _ := items["required"].([]string)
	if len(required) != 1 || required[0] != "title" {
		t.Fatalf("question required = %#v", items["required"])
	}

	var emittedQuestions []RequestUserInputAsyncQuestion
	handler.EmitAsyncQuestions = func(message string, questions []RequestUserInputAsyncQuestion) {
		emittedQuestions = questions
	}
	output, err := handler.Execute(context.Background(), &Invocation{
		Payload: Payload{Kind: PayloadFunction, Arguments: `{"questions":[{"title":"Which database?","options":["Postgres","SQLite"]},{"title":"Deadline?"}]}`},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(emittedQuestions) != 2 || emittedQuestions[1].Options != nil {
		t.Fatalf("emitted questions = %#v", emittedQuestions)
	}
	if !strings.Contains(output.Body, `"accepted":true`) {
		t.Fatalf("output body = %q", output.Body)
	}
	data, _ := output.Data["async_questions"].(map[string]any)
	if data == nil || data["delivery"] != "async" {
		t.Fatalf("async_questions data = %#v", output.Data["async_questions"])
	}
	if data["message"] != "Which database?\n- Postgres\n- SQLite\n\nDeadline?" {
		t.Fatalf("message = %q", data["message"])
	}
	questionsJSON, _ := data["questions"].([]any)
	if len(questionsJSON) != 2 {
		t.Fatalf("questions JSON = %#v", data["questions"])
	}
	first, _ := questionsJSON[0].(map[string]any)
	if first["title"] != "Which database?" {
		t.Fatalf("first question = %#v", first)
	}
	options, _ := first["options"].([]any)
	if len(options) != 2 || options[0] != "Postgres" {
		t.Fatalf("first question options = %#v", first["options"])
	}
	if _, present := questionsJSON[1].(map[string]any)["options"]; present {
		t.Fatalf("free-text question carried options: %#v", questionsJSON[1])
	}
}

func TestRequestUserInputAsyncHandlerValidation(t *testing.T) {
	handler := &RequestUserInputAsyncHandler{}
	cases := []struct {
		name      string
		arguments string
		want      string
	}{
		{name: "empty questions", arguments: `{"questions":[]}`, want: "questions must not be empty"},
		{name: "blank title", arguments: `{"questions":[{"title":"  "}]}`, want: "question titles must not be empty"},
		{name: "empty options", arguments: `{"questions":[{"title":"Q","options":[]}]}`, want: "options must contain at least one non-empty answer"},
		{name: "blank option", arguments: `{"questions":[{"title":"Q","options":["ok"," "]}]}`, want: "options must contain at least one non-empty answer"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := handler.Execute(context.Background(), &Invocation{
				Payload: Payload{Kind: PayloadFunction, Arguments: testCase.arguments},
			})
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %v, want %q", err, testCase.want)
			}
		})
	}
}

func TestRequestUserInputAsyncHandlerDescriptionOverride(t *testing.T) {
	override := "Ask the user."
	if got := (&RequestUserInputAsyncHandler{Description: &override}).Spec().Description; got != override {
		t.Fatalf("override description = %q, want %q", got, override)
	}
}
