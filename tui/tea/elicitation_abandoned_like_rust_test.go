package tea

import (
	"testing"
)

// Mirrors Rust #51611 (Signal abandonment of unanswered MCP elicitations): the
// consumer drops a published elicitation prompt once the request can no longer
// be answered, and never touches an unrelated prompt.
func TestElicitationAbandonedDismissesMatchingModalLikeRust(t *testing.T) {
	model := NewModel(nil, Options{})
	model.Update(ElicitationRequestMsg{
		ID:         "elicitation-1",
		ServerName: "docs",
		RequestID:  "req-1",
		Message:    "Allow docs search?",
		RequestedSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"q": map[string]any{"type": "string"}},
		},
	})
	if model.modal == nil || model.modal.kind != ModalKindElicitation {
		t.Fatalf("modal after request = %#v, want an elicitation modal", model.modal)
	}
	model.Update(ElicitationAbandonedMsg{ID: "elicitation-1", ServerName: "docs", RequestID: "req-1"})
	if model.modal != nil {
		t.Fatalf("modal after abandonment = %#v, want nil", model.modal)
	}
}

func TestElicitationAbandonedLeavesUnrelatedModalsLikeRust(t *testing.T) {
	model := NewModel(nil, Options{})
	model.Update(ElicitationRequestMsg{
		ID:         "elicitation-9",
		ServerName: "docs",
		Message:    "Allow docs search?",
		RequestedSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"q": map[string]any{"type": "string"}},
		},
	})
	if model.modal == nil {
		t.Fatal("expected an elicitation modal")
	}
	// An abandonment for a different request must not close this prompt.
	model.Update(ElicitationAbandonedMsg{ID: "elicitation-1", ServerName: "docs", RequestID: "req-1"})
	if model.modal == nil || model.modal.kind != ModalKindElicitation {
		t.Fatalf("unrelated abandonment closed the modal: %#v", model.modal)
	}
	// Matching by the request token dismisses it as well.
	model.Update(ElicitationAbandonedMsg{RequestID: "elicitation-9"})
	if model.modal != nil {
		t.Fatalf("modal after token match = %#v, want nil", model.modal)
	}
}
