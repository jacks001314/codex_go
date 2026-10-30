package state

import (
	"strings"
	"testing"

	"codex_go/retainedctx"
)

// Mirrors Rust #51627's cache-prefix contract for independent snapshots: the
// retained user instructions move ahead of the transcript, leaving only the
// rolling assistant context behind it.
func TestAsyncGuardianRetainedInstructionsPrecedeTranscriptLikeRust(t *testing.T) {
	instruction := "Inspect staging only. Do not publish."
	context := &retainedctx.RetainedContext{}
	context.RecordUserMessage(retainedctx.RetainedUserMessage{
		TurnID:    "turn-1",
		MessageID: retainedStringPtr("user-1"),
		Text:      instruction,
		Complete:  true,
	}, retainedctx.LocalInputSource(retainedUint64Ptr(0)))
	context.RecordAssistantMessage(retainedctx.RetainedUserMessage{
		TurnID:    "turn-1",
		MessageID: retainedStringPtr("assistant-1"),
		Text:      "Understood.",
		Complete:  true,
	}, retainedctx.LocalInputSource(retainedUint64Ptr(1)))
	prompt, err := BuildPromptWithOptions(
		Action{Type: "command", Command: "ls", CWD: "/repo"},
		[]string{"user: current request"},
		BuildPromptOptions{Presentation: ActionPresentationAsync, RetainedContext: context},
	)
	if err != nil {
		t.Fatalf("BuildPromptWithOptions() error = %v", err)
	}
	retainedAt := strings.Index(prompt, retainedUserInstructionsStart)
	transcriptAt := strings.Index(prompt, "Recent transcript:")
	assistantAt := strings.Index(prompt, retainedAssistantContextStart)
	if retainedAt < 0 || transcriptAt < 0 || assistantAt < 0 {
		t.Fatalf("prompt is missing a retained or transcript section:\n%s", prompt)
	}
	if !(retainedAt < transcriptAt && transcriptAt < assistantAt) {
		t.Fatalf("order = retained:%d transcript:%d assistant:%d\n%s", retainedAt, transcriptAt, assistantAt, prompt)
	}
	if strings.Contains(prompt[:transcriptAt], "Understood.") {
		t.Fatalf("assistant context leaked into the instruction prefix:\n%s", prompt)
	}
}

// Mirrors Rust #51627's split-omission rule: an assistant omission notice in its
// own section cannot be suppressed by the instruction section's earlier proof.
func TestAsyncGuardianSplitAssistantOmissionSkipsPreviousOmissionDedupLikeRust(t *testing.T) {
	markersOnly := RetainedInstructionSections{
		Instructions:     []string{"retained instruction\n"},
		AssistantContext: []string{retainedAssistantContextStart + "\n", retainedAssistantContextEnd + "\n"},
	}
	suppressed := ApplyRetainedOmissionDeduplication(markersOnly, &GuardianRetainedOmissions{}, false)
	if suppressed.AssistantContext != nil {
		t.Fatalf("an omission-only assistant section was not suppressed: %#v", suppressed.AssistantContext)
	}

	context := &retainedctx.RetainedContext{}
	context.RecordUserMessage(retainedctx.RetainedUserMessage{
		TurnID:    "turn-1",
		MessageID: retainedStringPtr("user-1"),
		Text:      "Inspect staging only.",
		Complete:  true,
	}, retainedctx.LocalInputSource(retainedUint64Ptr(0)))
	context.RecordAssistantMessage(retainedctx.RetainedUserMessage{
		TurnID:    "turn-1",
		MessageID: retainedStringPtr("assistant-1"),
		Text:      "Partial assistant evidence.",
		Complete:  false,
	}, retainedctx.LocalInputSource(retainedUint64Ptr(1)))
	sections := RenderRetainedInstructionSections(context)
	if !hasSplitAssistantOmissionSection(sections.AssistantContext) {
		t.Fatalf("the split assistant omission was not detected: %#v", sections.AssistantContext)
	}
	deduplicated := ApplyRetainedOmissionDeduplication(
		sections,
		&GuardianRetainedOmissions{AssistantContext: true},
		false,
	)
	if len(deduplicated.AssistantContext) == 0 {
		t.Fatalf("a split assistant omission was suppressed by an earlier proof: %#v", deduplicated)
	}
	if !strings.Contains(strings.Join(deduplicated.AssistantContext, ""), strings.TrimRight(RetainedAssistantOmissionNotice(), "\n")) {
		t.Fatalf("the split assistant omission lost its host notice: %#v", deduplicated.AssistantContext)
	}
}

// Mirrors Rust #51627's metadata clearing: when the notice is split, the review
// records no guardian_retained_omissions proof for the next snapshot.
func TestCurrentRetainedOmissionsClearsSplitAssistantOmissionLikeRust(t *testing.T) {
	context := &retainedctx.RetainedContext{}
	context.RecordUserMessage(retainedctx.RetainedUserMessage{
		TurnID:    "turn-1",
		MessageID: retainedStringPtr("user-1"),
		Text:      "Inspect staging only.",
		Complete:  true,
	}, retainedctx.LocalInputSource(retainedUint64Ptr(0)))
	context.RecordAssistantMessage(retainedctx.RetainedUserMessage{
		TurnID:    "turn-1",
		MessageID: retainedStringPtr("assistant-1"),
		Text:      "Partial assistant evidence.",
		Complete:  false,
	}, retainedctx.LocalInputSource(retainedUint64Ptr(1)))
	if got := CurrentRetainedOmissions(context, ActionPresentationAsync); got != (GuardianRetainedOmissions{}) {
		t.Fatalf("async split omission recorded delivery proof: %#v", got)
	}
	if got := CurrentRetainedOmissions(context, ActionPresentationSyncFull); !got.AssistantContext {
		t.Fatalf("the synchronous layout dropped the assistant omission: %#v", got)
	}
}
