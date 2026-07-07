package tool

import (
	"fmt"
	"testing"
)

func TestFunctionCallErrorFormatting(t *testing.T) {
	modelErr := RespondToModel("bad args")
	if modelErr.Error() != "bad args" {
		t.Fatalf("unexpected model error: %q", modelErr.Error())
	}
	if !modelErr.RespondsToModel() || modelErr.IsFatal() {
		t.Fatalf("expected model-visible error flags")
	}

	fatalErr := Fatal("disk failed")
	if fatalErr.Error() != "Fatal error: disk failed" {
		t.Fatalf("unexpected fatal error: %q", fatalErr.Error())
	}
	if !fatalErr.IsFatal() || fatalErr.RespondsToModel() {
		t.Fatalf("expected fatal error flags")
	}
}

func TestFromErrorPreservesFunctionCallError(t *testing.T) {
	original := RespondToModel("show this")
	wrapped := fmt.Errorf("wrapper: %w", original)
	converted := FromError(wrapped)
	if converted != original {
		t.Fatalf("expected original error to be preserved")
	}

	plain := FromError(fmt.Errorf("plain"))
	if plain == nil || !plain.IsFatal() || plain.ModelMessage() != "plain" {
		t.Fatalf("expected plain errors to become fatal, got %#v", plain)
	}
}
