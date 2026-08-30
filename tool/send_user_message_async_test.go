package tool

import (
	"context"
	"strings"
	"testing"
)

func TestSendUserMessageAsyncHandlerEmitsAndReturnsAccepted(t *testing.T) {
	var emitted string
	handler := &SendUserMessageAsyncHandler{EmitAsyncMessage: func(message string) {
		emitted = message
	}}
	spec := handler.Spec()
	if spec.Name.Key() != "send_user_message_async" {
		t.Fatalf("tool name = %q", spec.Name.Key())
	}
	if spec.Exposure != ExposureDirectModelOnly {
		t.Fatalf("exposure = %q, want direct_model_only", spec.Exposure)
	}
	output, err := handler.Execute(context.Background(), &Invocation{
		Payload: Payload{Kind: PayloadFunction, Arguments: `{"message":"still working"}`},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if emitted != "still working" {
		t.Fatalf("emitted message = %q, want still working", emitted)
	}
	if !strings.Contains(output.Body, `"accepted":true`) {
		t.Fatalf("output body = %q, want accepted", output.Body)
	}
	if got, _ := output.Data["async_message"].(map[string]any); got == nil || got["delivery"] != "async" {
		t.Fatalf("async_message data = %#v", output.Data["async_message"])
	}
}

func TestSendUserMessageAsyncHandlerRejectsEmpty(t *testing.T) {
	handler := &SendUserMessageAsyncHandler{}
	if _, err := handler.Execute(context.Background(), &Invocation{
		Payload: Payload{Kind: PayloadFunction, Arguments: `{"message":"   "}`},
	}); err == nil || !strings.Contains(err.Error(), "must not be empty") {
		t.Fatalf("empty message error = %v", err)
	}
}

func TestSendUserMessageAsyncHandlerDescriptionOverride(t *testing.T) {
	// A nil description falls back to the built-in description (no catalog value).
	if got := (&SendUserMessageAsyncHandler{}).Spec().Description; got != defaultSendUserMessageAsyncDescription {
		t.Fatalf("default description = %q", got)
	}
	// A catalog description replaces the built-in.
	override := "Ask the user a clarifying question."
	if got := (&SendUserMessageAsyncHandler{Description: &override}).Spec().Description; got != override {
		t.Fatalf("override description = %q, want %q", got, override)
	}
	// An explicit empty string is preserved (not replaced by the built-in).
	empty := ""
	if got := (&SendUserMessageAsyncHandler{Description: &empty}).Spec().Description; got != "" {
		t.Fatalf("empty description = %q, want empty", got)
	}
}
