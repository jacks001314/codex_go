package state

import (
	"strings"
	"testing"
)

// Mirrors Rust's `GuardianSenderMessages::body` and its type markers
// (core/src/context/guardian_sender_messages.rs): the snapshot is reviewer-only,
// host-authored, and every recovered message keeps its `user: ` role label.
func TestGuardianSenderMessagesRenderLikeRust(t *testing.T) {
	first := "Inspect the experiment."
	multiline := "Only use staging.\nNever production."
	rendered := GuardianSenderMessages{
		Source:   "thread-sender",
		Delivery: "delivery-1",
		Messages: []GuardianSenderExchange{{User: &first}, {User: &multiline}},
	}.Render()

	want := ">>> SENDER USER MESSAGES START\n" +
		"Received message: delivery-1\n" +
		"Source thread: thread-sender\n" +
		guardianSenderMessagesHeader +
		"user: Inspect the experiment.\n" +
		// A multi-line message keeps one role labe per line, so content cannot
		// impersonate another role.
		"user: Only use staging.\n" +
		"user: Never production.\n" +
		">>> SENDER USER MESSAGES END\n"
	if rendered != want {
		t.Fatalf("rendered = %q, want %q", rendered, want)
	}
}

// Rust #49951 records the assistant context preceding each sender instruction.
// The text is untrusted context, an incomplete assistant message becomes the
// host's unavailable-context notice, and one exchange shares one budget with user
// evidence taking priority.
func TestGuardianSenderMessagesRenderAssistantContextLikeRust(t *testing.T) {
	reply := "👍"
	assistant := "Rerun only the staging task?\nPreserve its checkpoints?"
	rendered := GuardianSenderMessages{
		Source:   "thread-sender",
		Delivery: "delivery-4",
		Messages: []GuardianSenderExchange{{
			User:      &reply,
			Assistant: &RootMessage{Kind: RootMessageAssistant, Text: assistant},
		}},
	}.Render()
	want := ">>> SENDER USER MESSAGES START\n" +
		"Received message: delivery-4\n" +
		"Source thread: thread-sender\n" +
		guardianSenderMessagesHeader +
		// Preceding assistant context precedes the reply it explains, and both
		// sides keep one role label per line.
		"assistant: Rerun only the staging task?\n" +
		"assistant: Preserve its checkpoints?\n" +
		"user: 👍\n" +
		">>> SENDER USER MESSAGES END\n"
	if rendered != want {
		t.Fatalf("rendered = %q, want %q", rendered, want)
	}

	incomplete := GuardianSenderMessages{
		Delivery: "delivery-5",
		Messages: []GuardianSenderExchange{{
			Assistant: &RootMessage{Kind: RootMessageIncompleteAssistantContext},
		}},
	}.Render()
	if !strings.Contains(incomplete, rootIncompleteAssistantContextNotice) {
		t.Fatalf("incomplete assistant context kept no host notice:\n%s", incomplete)
	}

	// The rendered user message still fits the budget, but the assistant context
	// no longer shares it, so the whole exchange drops it and reports the gap.
	padded := "Inspect the experiment." + strings.Repeat("x", 850)
	oversized := GuardianSenderMessages{
		Delivery: "delivery-6",
		Messages: []GuardianSenderExchange{{
			User:      &padded,
			Assistant: &RootMessage{Kind: RootMessageAssistant, Text: "LATER CONTEXT"},
		}},
	}.Render()
	if !strings.Contains(oversized, "user: "+padded+"\n") {
		t.Fatalf("user evidence lost to assistant context:\n%s", oversized)
	}
	if strings.Contains(oversized, "assistant: LATER CONTEXT") {
		t.Fatalf("assistant context exceeded the shared exchange budget:\n%s", oversized)
	}
	if !strings.Contains(oversized, rootIncompleteAssistantContextNotice) {
		t.Fatalf("dropped assistant context left no host notice:\n%s", oversized)
	}
}

// A recognized delivery without usable provenance still gets a snapshot, and an
// unrecoverable message becomes a host notice instead of partial text.
func TestGuardianSenderMessagesRenderUsesHostNoticesLikeRust(t *testing.T) {
	unavailable := GuardianSenderMessages{Delivery: "delivery-2"}.Render()
	if !strings.Contains(unavailable, "Source thread: unavailable\n") {
		t.Fatalf("missing unavailable source:\n%s", unavailable)
	}
	if !strings.Contains(unavailable, guardianSenderMessagesNone) {
		t.Fatalf("missing no-messages notice:\n%s", unavailable)
	}

	oversized := strings.Repeat("x", GuardianSenderMessagesBudgetBytes)
	rendered := GuardianSenderMessages{
		Source:   "thread-sender",
		Delivery: "delivery-3",
		Messages: []GuardianSenderExchange{{}, {User: &oversized}},
	}.Render()
	if got := strings.Count(rendered, guardianSenderMessagesUnavailable); got != 2 {
		t.Fatalf("host notices = %d, want 2:\n%s", got, rendered)
	}
	if strings.Contains(rendered, "xxx") {
		t.Fatalf("oversized message leaked partial text:\n%s", rendered)
	}
}
