package tool

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// request_user_input used to advertise an empty parameter object, so models
// called it without questions. The schema now mirrors Rust's
// create_request_user_input_tool.
func TestRequestUserInputSpecIncludesQuestionsSchemaLikeRust(t *testing.T) {
	spec := NewRequestUserInputHandler(nil).Spec()
	if spec.InputSchema == nil {
		t.Fatal("request_user_input spec has no input schema")
	}
	if spec.InputSchema["type"] != "object" {
		t.Fatalf("schema type = %#v", spec.InputSchema["type"])
	}
	if want := []string{"questions"}; !reflect.DeepEqual(spec.InputSchema["required"], want) {
		t.Fatalf("schema required = %#v, want %#v", spec.InputSchema["required"], want)
	}
	if spec.InputSchema["additionalProperties"] != false {
		t.Fatalf("schema additionalProperties = %#v", spec.InputSchema["additionalProperties"])
	}
	properties, ok := spec.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema properties = %#v", spec.InputSchema["properties"])
	}
	questions, ok := properties["questions"].(map[string]any)
	if !ok {
		t.Fatalf("questions schema = %#v", properties["questions"])
	}
	if questions["type"] != "array" {
		t.Fatalf("questions type = %#v", questions["type"])
	}
	items, ok := questions["items"].(map[string]any)
	if !ok {
		t.Fatalf("question item schema = %#v", questions["items"])
	}
	if want := []string{"id", "header", "question", "options"}; !reflect.DeepEqual(items["required"], want) {
		t.Fatalf("question required = %#v, want %#v", items["required"], want)
	}
	if items["additionalProperties"] != false {
		t.Fatalf("question additionalProperties = %#v", items["additionalProperties"])
	}
	questionProperties, ok := items["properties"].(map[string]any)
	if !ok {
		t.Fatalf("question properties = %#v", items["properties"])
	}
	for _, name := range []string{"id", "header", "question", "options"} {
		if _, ok := questionProperties[name]; !ok {
			t.Fatalf("question properties missing %q: %#v", name, questionProperties)
		}
	}
	// The Go tool description advertises autoResolutionMs, so the schema must
	// accept it even though Rust deprecated the field.
	if _, ok := properties["autoResolutionMs"]; !ok {
		t.Fatalf("schema properties missing autoResolutionMs: %#v", properties)
	}
}

// Rust maps argument parsing and normalization failures to
// FunctionCallError::RespondToModel. Returning a bare error here escalates to a
// fatal turn error, which is what made the session stop after a schema-less
// request_user_input call.
func TestRequestUserInputInvalidArgumentsStayModelVisible(t *testing.T) {
	cases := map[string]string{
		"empty arguments": "",
		"empty object":    "{}",
		"no questions":    `{"questions":[]}`,
		"too many questions": `{"questions":[` +
			`{"id":"a","header":"A","question":"a?"},` +
			`{"id":"b","header":"B","question":"b?"},` +
			`{"id":"c","header":"C","question":"c?"},` +
			`{"id":"d","header":"D","question":"d?"}]}`,
		"missing id":     `{"questions":[{"header":"H","question":"q?"}]}`,
		"malformed json": `{"questions":`,
	}
	for name, arguments := range cases {
		t.Run(name, func(t *testing.T) {
			responderCalled := false
			handler := NewRequestUserInputHandler(func(context.Context, *RequestUserInputArgs) (*UserInputResponse, error) {
				responderCalled = true
				return &UserInputResponse{Answers: map[string]string{}}, nil
			})
			_, err := handler.Execute(context.Background(), &Invocation{
				Payload: Payload{Kind: PayloadFunction, Arguments: arguments},
			})
			if responderCalled {
				t.Fatal("responder ran for invalid arguments")
			}
			var callErr *FunctionCallError
			if !errors.As(err, &callErr) {
				t.Fatalf("error = %v, want *FunctionCallError", err)
			}
			if callErr.IsFatal() || !callErr.RespondsToModel() {
				t.Fatalf("error = %#v, want model-visible (non-fatal)", callErr)
			}
		})
	}
}
