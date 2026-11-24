package agentboard

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"codex_go/utils"
)

// Rust #62136 Conversation::deliver injects each admitted batch into the
// retained reviewer thread as guardian_original_evidence, a function-call
// output without a call id.
func TestGuardianEvidenceItemRendersInjectedResponseItemLikeRust(t *testing.T) {
	item := GuardianEvidenceItem{
		Name:      GuardianEvidenceContextName,
		Namespace: GuardianEvidenceNamespace,
		Text:      `{"sources":[]}`,
	}
	rendered, err := item.ResponseItem()
	if err != nil {
		t.Fatalf("ResponseItem() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(rendered, &decoded); err != nil {
		t.Fatalf("decode rendered item: %v", err)
	}
	if got := decoded["type"]; got != "function_call_output" {
		t.Fatalf("type = %#v, want function_call_output", got)
	}
	if got := decoded["name"]; got != GuardianEvidenceContextName {
		t.Fatalf("name = %#v, want %q", got, GuardianEvidenceContextName)
	}
	if got := decoded["namespace"]; got != GuardianEvidenceNamespace {
		t.Fatalf("namespace = %#v, want %q", got, GuardianEvidenceNamespace)
	}
	if got := decoded["output"]; got != `{"sources":[]}` {
		t.Fatalf("output = %#v, want the batch body verbatim", got)
	}
	for _, absent := range []string{"call_id", "id"} {
		if _, ok := decoded[absent]; ok {
			t.Fatalf("rendered item carries %q, want none", absent)
		}
	}

	// A namespace-less batch still belongs to the guardian tool namespace.
	rendered, err = (GuardianEvidenceItem{Name: GuardianEvidenceContextName, Text: "{}"}).ResponseItem()
	if err != nil {
		t.Fatalf("ResponseItem(no namespace) error = %v", err)
	}
	if !strings.Contains(string(rendered), `"namespace":"guardian"`) {
		t.Fatalf("rendered item = %s, want the guardian namespace", rendered)
	}
}

func TestGuardianEvidenceItemRejectsUnroutableInjectionLikeRust(t *testing.T) {
	if _, err := (GuardianEvidenceItem{Text: "{}"}).ResponseItem(); err == nil {
		t.Fatalf("ResponseItem(no name) succeeded")
	}
	if _, err := (GuardianEvidenceItem{Name: GuardianEvidenceContextName}).ResponseItem(); err == nil {
		t.Fatalf("ResponseItem(no body) succeeded")
	}
}

// TestGuardianInboxAdmitInjectsBeforeAdvancingCursorLikeRust mirrors Rust
// Conversation::deliver: the injector records the batch, and only then does the
// conversation cursor advance. A failed injection leaves the evidence queued.
func TestGuardianInboxAdmitInjectsBeforeAdvancingCursorLikeRust(t *testing.T) {
	ctx, board, delivery, root, actor := guardianInboxFixture(t)
	inbox := delivery.Inbox(actor)
	if err := inbox.SetSubscription("a", true); err != nil {
		t.Fatalf("subscribe inbox: %v", err)
	}
	first := guardianInboxAppend(t, ctx, board, root, 0, "a", "one")
	if err := delivery.Publish(GuardianEvidenceID{Ordinal: 0}, map[string]string{actor: first}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	var injected []json.RawMessage
	injector := guardianTestInjector(func(_ context.Context, items []json.RawMessage) error {
		injected = append(injected, items...)
		return nil
	})
	conversation := NewGuardianReviewerConversation(actor, inbox, utils.BytesPolicy(64*1024))
	if err := conversation.Deliver(ctx, actor, board, GuardianInboxAdmit(injector)); err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}
	if len(injected) != 1 {
		t.Fatalf("injected items = %d, want 1", len(injected))
	}
	if !strings.Contains(string(injected[0]), GuardianEvidenceContextName) {
		t.Fatalf("injected item = %s, want the evidence name", injected[0])
	}
	if got := conversation.Delivered(); got != 1 {
		t.Fatalf("delivered = %d, want 1", got)
	}

	failing := guardianTestInjector(func(context.Context, []json.RawMessage) error {
		return errors.New("thread unavailable")
	})
	second := guardianInboxAppend(t, ctx, board, root, 1, "a", "two")
	if err := delivery.Publish(GuardianEvidenceID{Ordinal: 1}, map[string]string{actor: second}); err != nil {
		t.Fatalf("Publish(second) error = %v", err)
	}
	if err := conversation.Deliver(ctx, actor, board, GuardianInboxAdmit(failing)); err == nil {
		t.Fatalf("Deliver() with a failing injector succeeded")
	}
	if got := conversation.Delivered(); got != 1 {
		t.Fatalf("delivered after failed injection = %d, want the cursor unmoved", got)
	}
	if GuardianInboxAdmit(nil)(ctx, GuardianEvidenceItem{Name: GuardianEvidenceContextName, Text: "{}"}) == nil {
		t.Fatalf("GuardianInboxAdmit(nil) succeeded")
	}
}

type guardianTestInjector func(context.Context, []json.RawMessage) error

func (f guardianTestInjector) InjectResponseItems(ctx context.Context, items []json.RawMessage) error {
	return f(ctx, items)
}
