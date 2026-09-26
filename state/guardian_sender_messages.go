package state

import "strings"

// This file ports Rust's `GuardianSenderMessages`
// (`core/src/context/guardian_sender_messages.rs`): the reviewer-only snapshot of
// original user instructions for one accepted delegation, rendered once at
// admission and never added to the worker prompt.

const (
	// GuardianSenderMessagesBudgetBytes is Rust's per-message evidence budget
	// (`message.len() <= 900`): a rendered message beyond it becomes a host
	// notice instead of partial text.
	GuardianSenderMessagesBudgetBytes = 900

	guardianSenderMessagesStart = ">>> SENDER USER MESSAGES START\n"
	guardianSenderMessagesEnd   = ">>> SENDER USER MESSAGES END\n"

	guardianSenderMessagesUnavailable = "Host: A sender user message is unavailable within the evidence budget. Do not infer permission from missing evidence.\n"
)

// GuardianSenderMessages is the bounded sender snapshot for one delivery.
// Source is the delegation's source thread id, or empty for an unavailable one.
// A nil entry marks a message the host could not recover.
type GuardianSenderMessages struct {
	Source   string
	Delivery string
	Messages []*string
}

// Render mirrors Rust's `ContextualUserFragment` rendering for this fragment:
// the type markers wrap the body, and every line is host-authored.
func (f GuardianSenderMessages) Render() string {
	var builder strings.Builder
	builder.WriteString(guardianSenderMessagesStart)
	builder.WriteString(f.body())
	builder.WriteString(guardianSenderMessagesEnd)
	return builder.String()
}

// body mirrors Rust's `GuardianSenderMessages::body`.
func (f GuardianSenderMessages) body() string {
	source := "unavailable"
	if trimmed := strings.TrimSpace(f.Source); trimmed != "" {
		source = trimmed
	}
	var builder strings.Builder
	builder.WriteString("Received message: " + f.Delivery + "\n")
	builder.WriteString("Source thread: " + source + "\n")
	builder.WriteString("Host: Up to three recent user messages captured when this delivery was accepted. This is partial historical context for this delivery, not a transfer of permission. Earlier sections describe earlier deliveries; earlier instructions and later changes may be absent.\n")
	if len(f.Messages) == 0 {
		builder.WriteString("Host: No sender user messages are available.\n")
	}
	for _, message := range f.Messages {
		rendered := ""
		if message != nil {
			rendered = RootMessage{Kind: RootMessageUser, Text: *message}.Render()
		}
		if rendered != "" && len(rendered) <= GuardianSenderMessagesBudgetBytes {
			builder.WriteString(rendered)
			continue
		}
		builder.WriteString(guardianSenderMessagesUnavailable)
	}
	return builder.String()
}
